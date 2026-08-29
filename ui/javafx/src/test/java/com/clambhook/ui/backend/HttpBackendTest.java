// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.backend;

import com.sun.net.httpserver.HttpServer;
import org.junit.jupiter.api.Test;

import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.net.InetAddress;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.util.Base64;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Flow;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class HttpBackendTest {
    @Test
    void requestsRunOffThreadAndPreserveTheAuthenticatedControlContract()
            throws Exception {
        HttpServer server = HttpServer.create(new InetSocketAddress(
                InetAddress.getLoopbackAddress(), 0), 0);
        CountDownLatch entered = new CountDownLatch(1);
        CountDownLatch release = new CountDownLatch(1);
        AtomicReference<String> request = new AtomicReference<>();
        AtomicReference<Thread> handlerThread = new AtomicReference<>();
        server.createContext("/api/v1/test", exchange -> {
            handlerThread.set(Thread.currentThread());
            String body = new String(exchange.getRequestBody().readAllBytes(),
                    StandardCharsets.UTF_8);
            request.set(exchange.getRequestMethod() + " "
                    + exchange.getRequestHeaders().getFirst("Authorization") + " " + body);
            entered.countDown();
            try {
                if (!release.await(5, TimeUnit.SECONDS)) {
                    throw new IOException("test request was not released");
                }
                byte[] response = "{\"ok\":true}".getBytes(StandardCharsets.UTF_8);
                exchange.sendResponseHeaders(200, response.length);
                exchange.getResponseBody().write(response);
            } catch (InterruptedException error) {
                Thread.currentThread().interrupt();
                throw new IOException("test handler interrupted", error);
            } finally {
                exchange.close();
            }
        });
        server.start();

        Thread caller = Thread.currentThread();
        try (HttpBackend backend = new HttpBackend(
                "http://127.0.0.1:" + server.getAddress().getPort(), "secret")) {
            CompletableFuture<String> response = backend.request(
                    "POST", "/api/v1/test", "{\"value\":1}");
            assertTrue(entered.await(5, TimeUnit.SECONDS));
            assertFalse(response.isDone(), "the caller must never block on HTTP I/O");
            assertFalse(caller.equals(handlerThread.get()));
            release.countDown();
            assertEquals("{\"ok\":true}", response.get(5, TimeUnit.SECONDS));
            assertEquals("POST Bearer secret {\"value\":1}", request.get());
        } finally {
            release.countDown();
            server.stop(0);
        }
    }

    @Test
    void endpointConfigurationRejectsNonLoopbackOrNonOriginUrls() {
        assertThrows(BackendException.class,
                () -> new HttpBackend("https://controller.example:9443", ""));
        assertThrows(BackendException.class,
                () -> new HttpBackend("http://user@127.0.0.1:9090", ""));
        assertThrows(BackendException.class,
                () -> new HttpBackend("http://127.0.0.1:9090/control", ""));

        try (HttpBackend ipv6 = new HttpBackend("http://[::1]:9090", "")) {
            assertEquals("http://[::1]:9090", ipv6.baseUrl());
        }
    }

    @Test
    void liveEventsUseAnAuthenticatedWebSocketAndHonorBackpressure()
            throws Exception {
        try (ServerSocket server = new ServerSocket(0, 1,
                InetAddress.getByName("127.0.0.1"))) {
            AtomicReference<String> handshake = new AtomicReference<>();
            AtomicReference<Throwable> serverFailure = new AtomicReference<>();
            ExecutorService serverExecutor = Executors.newSingleThreadExecutor();
            serverExecutor.execute(() -> {
                try (Socket socket = server.accept()) {
                    String request = readHeaders(socket);
                    handshake.set(request);
                    String key = request.lines()
                            .filter(line -> line.regionMatches(
                                    true, 0, "Sec-WebSocket-Key:", 0, 18))
                            .map(line -> line.substring(18).trim())
                            .findFirst().orElseThrow();
                    String accept = Base64.getEncoder().encodeToString(
                            MessageDigest.getInstance("SHA-1").digest(
                                    (key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11")
                                            .getBytes(StandardCharsets.US_ASCII)));
                    socket.getOutputStream().write((
                            "HTTP/1.1 101 Switching Protocols\r\n" +
                                    "Upgrade: websocket\r\n" +
                                    "Connection: Upgrade\r\n" +
                                    "Sec-WebSocket-Accept: " + accept + "\r\n\r\n")
                            .getBytes(StandardCharsets.US_ASCII));
                    writeFrame(socket, 0x1,
                            "{\"type\":\"connection.opened\",\"data\":{}}");
                    writeFrame(socket, 0x8, "");
                    socket.getOutputStream().flush();
                } catch (Throwable error) {
                    serverFailure.set(error);
                }
            });

            CountDownLatch received = new CountDownLatch(1);
            AtomicReference<String> event = new AtomicReference<>();
            try (HttpBackend backend = new HttpBackend(
                    "http://127.0.0.1:" + server.getLocalPort(), "stream-secret")) {
                backend.liveEvents("/api/v1/events?types=connection.*")
                        .subscribe(new Flow.Subscriber<>() {
                            @Override
                            public void onSubscribe(Flow.Subscription subscription) {
                                subscription.request(1);
                            }

                            @Override
                            public void onNext(String item) {
                                event.set(item);
                                received.countDown();
                            }

                            @Override
                            public void onError(Throwable throwable) {
                                received.countDown();
                            }

                            @Override
                            public void onComplete() {
                            }
                        });
                assertTrue(received.await(5, TimeUnit.SECONDS));
                assertEquals("{\"type\":\"connection.opened\",\"data\":{}}",
                        event.get());
                assertTrue(handshake.get().startsWith(
                        "GET /api/v1/events?types=connection.* HTTP/1.1"));
                assertTrue(handshake.get().contains(
                        "Authorization: Bearer stream-secret"));
            } finally {
                serverExecutor.shutdownNow();
            }
            assertEquals(null, serverFailure.get());
        }
    }

    private static String readHeaders(Socket socket) throws IOException {
        ByteArrayOutputStream output = new ByteArrayOutputStream();
        int matched = 0;
        while (output.size() < 32 * 1024) {
            int next = socket.getInputStream().read();
            if (next < 0) throw new IOException("WebSocket handshake ended early");
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
        throw new IOException("WebSocket handshake exceeds safety limit");
    }

    private static void writeFrame(Socket socket, int opcode, String value)
            throws IOException {
        byte[] payload = value.getBytes(StandardCharsets.UTF_8);
        if (payload.length > 125) throw new IOException("test frame is too large");
        socket.getOutputStream().write(0x80 | opcode);
        socket.getOutputStream().write(payload.length);
        socket.getOutputStream().write(payload);
    }
}
