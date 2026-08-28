package com.clambhook.ui.backend;

import java.io.IOException;
import java.net.HttpURLConnection;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.util.Objects;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** GNU/Linux controller transport for the local C daemon API. */
public final class HttpBackend implements Backend {
    private static final int MAX_RESPONSE_BYTES = 16 * 1024 * 1024;

    private final ExecutorService executor;
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

    public void configure(String baseUrl, String token) {
        this.baseUri = normalizeBaseUri(baseUrl);
        this.token = Objects.requireNonNullElse(token, "").trim();
    }

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
        if (!("http".equalsIgnoreCase(uri.getScheme()) || "https".equalsIgnoreCase(uri.getScheme()))
                || uri.getHost() == null || uri.getUserInfo() != null) {
            throw new BackendException("API URL must be an HTTP(S) origin without credentials");
        }
        return uri;
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
        executor.shutdownNow();
    }
}
