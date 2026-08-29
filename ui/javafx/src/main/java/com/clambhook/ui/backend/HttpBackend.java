// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.backend;

import java.io.ByteArrayOutputStream;
import java.io.EOFException;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.InetSocketAddress;
import java.net.URI;
import java.net.URISyntaxException;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.security.GeneralSecurityException;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.time.Duration;
import java.util.Base64;
import java.util.Objects;
import java.util.Set;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Flow;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;
import javax.net.ssl.SSLParameters;
import javax.net.ssl.SSLSocket;
import javax.net.ssl.SSLSocketFactory;

/** GNU/Linux controller transport for the local C daemon API. */
public final class HttpBackend implements ConfigurableEndpointBackend {
    private static final int MAX_RESPONSE_BYTES = 16 * 1024 * 1024;
    private static final int MAX_HANDSHAKE_BYTES = 32 * 1024;
    private static final SecureRandom WEBSOCKET_RANDOM = new SecureRandom();

    private final ExecutorService executor;
    private final Set<WebSocketEventPublisher> eventPublishers =
            ConcurrentHashMap.newKeySet();
    private volatile URI baseUri;
    private volatile String token;

    public HttpBackend(String baseUrl, String token) {
        this.baseUri = normalizeBaseUri(baseUrl);
        this.token = Objects.requireNonNullElse(token, "").trim();
        this.executor = Executors.newFixedThreadPool(4, runnable -> {
            Thread thread = new Thread(runnable, "clambhook-http");
            thread.setDaemon(true);
            return thread;
        });
    }

    @Override
    public void configure(String baseUrl, String token) {
        this.baseUri = normalizeBaseUri(baseUrl);
        this.token = Objects.requireNonNullElse(token, "").trim();
    }

    @Override
    public String baseUrl() {
        return baseUri.toString().replaceAll("/$", "");
    }

    @Override
    public CompletableFuture<String> request(String method, String path, String body) {
        String normalizedMethod = Objects.requireNonNull(method, "method").trim().toUpperCase();
        URI target = resolvePath(path);
        String requestBody = Objects.requireNonNullElse(body, "");
        return CompletableFuture.supplyAsync(
                () -> send(normalizedMethod, target, requestBody), executor);
    }

    @Override
    public boolean supportsLiveEvents() {
        return true;
    }

    @Override
    public Flow.Publisher<String> liveEvents(String path) {
        URI target = websocketUri(resolvePath(path));
        WebSocketEventPublisher publisher = new WebSocketEventPublisher(
                target, token);
        eventPublishers.add(publisher);
        return publisher;
    }

    private String send(String method, URI target, String body) {
        HttpURLConnection connection = null;
        try {
            connection = (HttpURLConnection) target.toURL().openConnection();
            connection.setConnectTimeout((int) Duration.ofSeconds(4).toMillis());
            connection.setReadTimeout((int) Duration.ofSeconds(15).toMillis());
            connection.setRequestMethod(method);
            connection.setRequestProperty("Accept", "application/json");
            String currentToken = token;
            if (!currentToken.isBlank()) {
                connection.setRequestProperty("Authorization", "Bearer " + currentToken);
            }
            if (!body.isEmpty()) {
                byte[] bytes = body.getBytes(StandardCharsets.UTF_8);
                connection.setDoOutput(true);
                connection.setFixedLengthStreamingMode(bytes.length);
                connection.setRequestProperty("Content-Type", "application/json; charset=utf-8");
                try (var output = connection.getOutputStream()) {
                    output.write(bytes);
                }
            }
            int status = connection.getResponseCode();
            var stream = status >= 200 && status < 300
                    ? connection.getInputStream() : connection.getErrorStream();
            String response = stream == null ? "" : readLimited(stream);
            if (status < 200 || status >= 300) {
                throw new BackendException(status, response.isBlank()
                        ? "ClambHook returned HTTP " + status : response);
            }
            return response;
        } catch (BackendException error) {
            throw error;
        } catch (IOException error) {
            throw new BackendException(0, "Cannot reach " + target + ": " + error.getMessage(), error);
        } finally {
            if (connection != null) {
                connection.disconnect();
            }
        }
    }

    private static String readLimited(java.io.InputStream stream) throws IOException {
        try (stream) {
            byte[] bytes = stream.readNBytes(MAX_RESPONSE_BYTES + 1);
            if (bytes.length > MAX_RESPONSE_BYTES) {
                throw new IOException("response exceeds 16 MiB safety limit");
            }
            return new String(bytes, StandardCharsets.UTF_8);
        }
    }

    private URI resolvePath(String path) {
        String value = Objects.requireNonNullElse(path, "").trim();
        if (!value.startsWith("/")) {
            value = "/" + value;
        }
        return baseUri.resolve(value.substring(1));
    }

    private static URI normalizeBaseUri(String value) {
        String normalized = Objects.requireNonNullElse(value, "").trim();
        if (normalized.isEmpty()) {
            normalized = "http://127.0.0.1:9090";
        }
        while (normalized.endsWith("/")) {
            normalized = normalized.substring(0, normalized.length() - 1);
        }
        URI uri;
        try {
            uri = URI.create(normalized + "/");
        } catch (IllegalArgumentException error) {
            throw new BackendException("Invalid API URL: " + normalized);
        }
        String host = Objects.requireNonNullElse(uri.getHost(), "");
        boolean loopback = host.equalsIgnoreCase("localhost")
                || host.equals("::1") || host.equals("[::1]")
                || host.matches("127(?:\\.[0-9]{1,3}){3}");
        boolean originPath = uri.getPath() == null || uri.getPath().equals("/");
        if (!("http".equalsIgnoreCase(uri.getScheme()) || "https".equalsIgnoreCase(uri.getScheme()))
                || !loopback || uri.getUserInfo() != null || uri.getQuery() != null
                || uri.getFragment() != null || !originPath) {
            throw new BackendException(
                    "API URL must be a loopback HTTP(S) origin without credentials or a path");
        }
        return uri;
    }

    private static URI websocketUri(URI uri) {
        String scheme = uri.getScheme().equalsIgnoreCase("https") ? "wss" : "ws";
        try {
            return new URI(scheme, null, uri.getHost(), uri.getPort(),
                    uri.getPath(), uri.getQuery(), null);
        } catch (URISyntaxException error) {
            throw new BackendException(0,
                    "Invalid event stream URL: " + uri, error);
        }
    }

    @Override
    public String displayName() {
        return "GNU/Linux local daemon";
    }

    @Override
    public boolean supportsConnectionControl() {
        return true;
    }

    @Override
    public void close() {
        for (WebSocketEventPublisher publisher : Set.copyOf(eventPublishers)) {
            publisher.close();
        }
        eventPublishers.clear();
        executor.shutdownNow();
    }

    private final class WebSocketEventPublisher
            implements Flow.Publisher<String>, AutoCloseable {
        private final URI target;
        private final String authorizationToken;
        private final AtomicBoolean subscribed = new AtomicBoolean();
        private final AtomicBoolean terminated = new AtomicBoolean();
        private final AtomicLong demand = new AtomicLong();
        private final Object demandMonitor = new Object();
        private final Object outputLock = new Object();
        private volatile Flow.Subscriber<? super String> subscriber;
        private volatile Socket socket;

        private WebSocketEventPublisher(URI target, String authorizationToken) {
            this.target = target;
            this.authorizationToken = Objects.requireNonNullElse(
                    authorizationToken, "");
        }

        @Override
        public void subscribe(Flow.Subscriber<? super String> value) {
            Objects.requireNonNull(value, "subscriber");
            if (!subscribed.compareAndSet(false, true)) {
                value.onSubscribe(new Flow.Subscription() {
                    @Override
                    public void request(long count) {
                    }

                    @Override
                    public void cancel() {
                    }
                });
                value.onError(new IllegalStateException(
                        "event stream supports one subscriber"));
                return;
            }
            subscriber = value;
            value.onSubscribe(new Flow.Subscription() {
                @Override
                public void request(long count) {
                    requestEvents(count);
                }

                @Override
                public void cancel() {
                    terminate(null, false);
                }
            });
            try {
                executor.execute(this::connectAndRead);
            } catch (RuntimeException error) {
                terminate(error, true);
            }
        }

        private void requestEvents(long count) {
            if (count <= 0) {
                terminate(new IllegalArgumentException(
                        "event demand must be positive"), true);
                return;
            }
            addDemand(count);
            synchronized (demandMonitor) {
                demandMonitor.notifyAll();
            }
        }

        private void addDemand(long count) {
            for (;;) {
                long current = demand.get();
                long next = current > Long.MAX_VALUE - count
                        ? Long.MAX_VALUE : current + count;
                if (demand.compareAndSet(current, next)) return;
            }
        }

        private void connectAndRead() {
            try {
                Socket active = openSocket();
                socket = active;
                if (terminated.get()) {
                    active.close();
                    return;
                }
                performHandshake(active);
                InputStream input = active.getInputStream();
                while (!terminated.get()) {
                    if (!waitForDemand()) return;
                    WebSocketFrame frame = readFrame(input);
                    switch (frame.opcode()) {
                        case 0x1 -> deliverText(frame);
                        case 0x2 -> throw new BackendException(
                                "ClambHook event stream sent an unexpected binary frame");
                        case 0x8 -> {
                            sendClientFrame(active, 0x8, frame.payload());
                            terminate(null, true);
                        }
                        case 0x9 -> sendClientFrame(active, 0xA, frame.payload());
                        case 0xA -> {
                            // A pong is transport bookkeeping, not an application event.
                        }
                        default -> throw new BackendException(
                                "ClambHook event stream sent an unsupported frame");
                    }
                }
            } catch (Throwable error) {
                if (!terminated.get()) terminate(error, true);
            }
        }

        private Socket openSocket() throws IOException {
            String host = target.getHost();
            int port = target.getPort() >= 0 ? target.getPort()
                    : target.getScheme().equalsIgnoreCase("wss") ? 443 : 80;
            Socket plain = new Socket();
            try {
                plain.connect(new InetSocketAddress(host, port),
                        (int) Duration.ofSeconds(4).toMillis());
                plain.setSoTimeout((int) Duration.ofSeconds(45).toMillis());
                if (!target.getScheme().equalsIgnoreCase("wss")) return plain;
                SSLSocket secure = (SSLSocket) ((SSLSocketFactory)
                        SSLSocketFactory.getDefault()).createSocket(
                        plain, host, port, true);
                SSLParameters parameters = secure.getSSLParameters();
                parameters.setEndpointIdentificationAlgorithm("HTTPS");
                secure.setSSLParameters(parameters);
                secure.startHandshake();
                return secure;
            } catch (IOException error) {
                try {
                    plain.close();
                } catch (IOException ignored) {
                    // Preserve the original connection error.
                }
                throw error;
            }
        }

        private void performHandshake(Socket active)
                throws IOException, GeneralSecurityException {
            byte[] nonce = new byte[16];
            WEBSOCKET_RANDOM.nextBytes(nonce);
            String key = Base64.getEncoder().encodeToString(nonce);
            String resource = target.getRawPath();
            if (resource == null || resource.isBlank()) resource = "/";
            if (target.getRawQuery() != null) {
                resource += "?" + target.getRawQuery();
            }
            int port = target.getPort() >= 0 ? target.getPort()
                    : target.getScheme().equalsIgnoreCase("wss") ? 443 : 80;
            boolean defaultPort = port == (target.getScheme().equalsIgnoreCase("wss")
                    ? 443 : 80);
            String host = target.getHost().contains(":")
                    ? "[" + target.getHost() + "]" : target.getHost();
            StringBuilder request = new StringBuilder()
                    .append("GET ").append(resource).append(" HTTP/1.1\r\n")
                    .append("Host: ").append(host);
            if (!defaultPort) request.append(':').append(port);
            request.append("\r\nUpgrade: websocket\r\n")
                    .append("Connection: Upgrade\r\n")
                    .append("Sec-WebSocket-Version: 13\r\n")
                    .append("Sec-WebSocket-Key: ").append(key).append("\r\n");
            if (!authorizationToken.isBlank()) {
                request.append("Authorization: Bearer ")
                        .append(authorizationToken).append("\r\n");
            }
            request.append("\r\n");
            active.getOutputStream().write(
                    request.toString().getBytes(StandardCharsets.US_ASCII));
            active.getOutputStream().flush();

            String response = readHandshake(active.getInputStream());
            String[] lines = response.split("\\r\\n");
            if (lines.length == 0 || !lines[0].matches("HTTP/1\\.[01] 101(?: .*)?")) {
                throw new BackendException("ClambHook rejected the event stream upgrade");
            }
            String expected = Base64.getEncoder().encodeToString(
                    MessageDigest.getInstance("SHA-1").digest(
                            (key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")
                                    .getBytes(StandardCharsets.US_ASCII)));
            String accept = header(lines, "Sec-WebSocket-Accept");
            if (!expected.equals(accept) ||
                    !header(lines, "Upgrade").equalsIgnoreCase("websocket") ||
                    !containsHeaderToken(header(lines, "Connection"), "upgrade")) {
                throw new BackendException(
                        "ClambHook returned an invalid event stream handshake");
            }
        }

        private boolean waitForDemand() throws InterruptedException {
            synchronized (demandMonitor) {
                while (demand.get() == 0 && !terminated.get()) {
                    demandMonitor.wait();
                }
                return !terminated.get();
            }
        }

        private void deliverText(WebSocketFrame frame) {
            if (!frame.finalFrame()) {
                throw new BackendException(
                        "ClambHook event stream fragmented an event frame");
            }
            long current = demand.get();
            if (current <= 0) {
                throw new IllegalStateException(
                        "event arrived without downstream demand");
            }
            if (current != Long.MAX_VALUE) demand.decrementAndGet();
            Flow.Subscriber<? super String> downstream = subscriber;
            if (downstream != null) {
                downstream.onNext(new String(frame.payload(), StandardCharsets.UTF_8));
            }
        }

        private WebSocketFrame readFrame(InputStream input) throws IOException {
            int first = readByte(input);
            int second = readByte(input);
            if ((first & 0x70) != 0) {
                throw new IOException("event frame uses unsupported extensions");
            }
            boolean finalFrame = (first & 0x80) != 0;
            int opcode = first & 0x0F;
            if ((second & 0x80) != 0) {
                throw new IOException("server event frames must not be masked");
            }
            long length = second & 0x7F;
            if (length == 126) {
                length = ((long) readByte(input) << 8) | readByte(input);
            } else if (length == 127) {
                length = 0;
                for (int index = 0; index < 8; ++index) {
                    int next = readByte(input);
                    if (index == 0 && (next & 0x80) != 0) {
                        throw new IOException("event frame length is invalid");
                    }
                    length = (length << 8) | next;
                }
            }
            if (length > MAX_RESPONSE_BYTES ||
                    ((opcode & 0x8) != 0 && length > 125)) {
                throw new IOException("event frame exceeds safety limit");
            }
            byte[] payload = input.readNBytes((int) length);
            if (payload.length != (int) length) {
                throw new EOFException("event frame ended early");
            }
            return new WebSocketFrame(finalFrame, opcode, payload);
        }

        private void sendClientFrame(Socket active, int opcode, byte[] payload)
                throws IOException {
            if (payload.length > 125) {
                throw new IOException("control frame exceeds safety limit");
            }
            byte[] mask = new byte[4];
            WEBSOCKET_RANDOM.nextBytes(mask);
            byte[] frame = new byte[2 + mask.length + payload.length];
            frame[0] = (byte) (0x80 | opcode);
            frame[1] = (byte) (0x80 | payload.length);
            System.arraycopy(mask, 0, frame, 2, mask.length);
            for (int index = 0; index < payload.length; ++index) {
                frame[6 + index] = (byte) (payload[index] ^ mask[index % 4]);
            }
            synchronized (outputLock) {
                OutputStream output = active.getOutputStream();
                output.write(frame);
                output.flush();
            }
        }

        @Override
        public void close() {
            terminate(null, true);
        }

        private void terminate(Throwable error, boolean notifySubscriber) {
            if (!terminated.compareAndSet(false, true)) return;
            eventPublishers.remove(this);
            synchronized (demandMonitor) {
                demandMonitor.notifyAll();
            }
            Socket active = socket;
            if (active != null) {
                try {
                    active.close();
                } catch (IOException ignored) {
                    // The stream is already terminating.
                }
            }
            if (!notifySubscriber) return;
            Flow.Subscriber<? super String> downstream = subscriber;
            if (downstream == null) return;
            if (error == null) downstream.onComplete();
            else downstream.onError(error);
        }

        private String readHandshake(InputStream input) throws IOException {
            ByteArrayOutputStream output = new ByteArrayOutputStream();
            int matched = 0;
            while (output.size() < MAX_HANDSHAKE_BYTES) {
                int next = readByte(input);
                output.write(next);
                matched = switch (matched) {
                    case 0 -> next == '\r' ? 1 : 0;
                    case 1 -> next == '\n' ? 2 : 0;
                    case 2 -> next == '\r' ? 3 : 0;
                    case 3 -> next == '\n' ? 4 : 0;
                    default -> matched;
                };
                if (matched == 4) {
                    return output.toString(StandardCharsets.US_ASCII);
                }
            }
            throw new IOException("event stream handshake exceeds safety limit");
        }

        private String header(String[] lines, String name) {
            String prefix = name + ":";
            for (int index = 1; index < lines.length; ++index) {
                if (lines[index].regionMatches(
                        true, 0, prefix, 0, prefix.length())) {
                    return lines[index].substring(prefix.length()).trim();
                }
            }
            return "";
        }

        private boolean containsHeaderToken(String value, String token) {
            for (String candidate : value.split(",")) {
                if (candidate.trim().equalsIgnoreCase(token)) return true;
            }
            return false;
        }

        private int readByte(InputStream input) throws IOException {
            int value = input.read();
            if (value < 0) throw new EOFException("event stream ended");
            return value;
        }
    }

    private record WebSocketFrame(boolean finalFrame, int opcode, byte[] payload) {
        private WebSocketFrame {
            payload = payload.clone();
        }

        @Override
        public byte[] payload() {
            return payload.clone();
        }
    }
}
