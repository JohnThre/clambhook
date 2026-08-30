// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.runtime;

import com.clambhook.ui.backend.Backend;
import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Flow;
import java.util.concurrent.TimeUnit;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class RuntimeClientTest {
    @Test
    void typedQueriesPreserveFrozenPathsAndEncoding() {
        RecordingBackend backend = new RecordingBackend();
        try (RuntimeClient client = new DefaultRuntimeClient(backend)) {
            client.servers("work vpn").join();
            client.traffic(new RuntimeClient.TrafficQuery(
                    "A", "hello world", "active", "udp", "ads", "proxy",
                    "org.example.app", 12, 25)).join();
            client.events(new RuntimeClient.EventCursor(
                    41, 512, List.of("connection.*", "rule.matched"),
                    List.of("conn 1"))).join();
            client.removeTemporaryRule("temp/one").join();
        }

        assertEquals("GET /api/v1/servers?profile=work+vpn ", backend.calls.get(0));
        assertEquals("GET /api/v1/traffic?profile=A&query=hello+world&state=active"
                + "&network=udp&rule=ads&chain=proxy&application=org.example.app"
                + "&after=12&limit=25 ", backend.calls.get(1));
        assertEquals("GET /api/v1/events/snapshot?after=41&limit=512"
                + "&types=connection.*&types=rule.matched&conn_id=conn+1 ",
                backend.calls.get(2));
        assertEquals("DELETE /api/v1/rules/temporary/temp%2Fone {}",
                backend.calls.get(3));
    }

    @Test
    void typedModelsDecodeStatusProfilesAndEventReplay() {
        RuntimeClient.Status status = RuntimeClient.Status.parse(
                "{\"running\":true,\"profile\":\"work\",\"tunnel_mode\":\"tun\","
                        + "\"listeners\":[{\"protocol\":\"socks5\","
                        + "\"addr\":\"127.0.0.1:1080\",\"active_conns\":3}]}");
        assertTrue(status.running());
        assertEquals("work", status.profile());
        assertEquals(3, status.listeners().get(0).activeConnections());

        RuntimeClient.Profiles profiles = RuntimeClient.Profiles.parse(
                "{\"active\":\"work\",\"profiles\":[\"work\",\"mobile\"]}");
        assertEquals(List.of("work", "mobile"), profiles.names());

        RuntimeClient.EventBatch batch = RuntimeClient.EventBatch.parse(
                "{\"complete\":false,\"next_sequence\":9,\"events\":[{"
                        + "\"sequence\":8,\"shard_id\":2,\"lamport\":4,"
                        + "\"ts_ns\":123,\"type\":\"connection.opened\","
                        + "\"data\":{\"conn_id\":\"c1\"}}]}");
        assertFalse(batch.complete());
        assertEquals(9, batch.nextSequence());
        assertEquals("connection.opened", batch.events().get(0).type());
    }

    @Test
    void developerToolingUsesTheFrozenControlSurface() {
        RecordingBackend backend = new RecordingBackend();
        try (RuntimeClient client = new DefaultRuntimeClient(backend)) {
            client.developerPendingBreakpoints().join();
            client.developerCaPem().join();
            client.replaceDeveloperMapRules("{\"rules\":[]}").join();
            client.replaceDeveloperBreakpointRules("{\"rules\":[]}").join();
            client.replaceDeveloperRewriteRules("{\"rules\":[]}").join();
            client.regenerateDeveloperCa().join();
            client.resolveDeveloperBreakpoint("bp/one", "{\"action\":\"drop\"}").join();
        }

        assertEquals(List.of(
                "GET /api/v1/developer/breakpoints/pending ",
                "GET /api/v1/developer/ca.pem ",
                "PUT /api/v1/developer/map-rules {\"rules\":[]}",
                "PUT /api/v1/developer/breakpoint-rules {\"rules\":[]}",
                "PUT /api/v1/developer/rewrite-rules {\"rules\":[]}",
                "POST /api/v1/developer/ca/regenerate {}",
                "POST /api/v1/developer/breakpoints/bp%2Fone/resolve {\"action\":\"drop\"}"),
                backend.calls);
    }

    @Test
    void profileConverterUsesReviewedHashContract() {
        RecordingBackend backend = new RecordingBackend();
        try (RuntimeClient client = new DefaultRuntimeClient(backend)) {
            client.reviewProfileConversion("proxies:\n", "mihomo", "Travel").join();
            client.importProfileConversion(
                    "[Proxy]\n", "surge", "Travel", "abc123", true).join();
        }

        assertTrue(backend.calls.get(0).startsWith(
                "POST /api/v1/config/converter/review "));
        assertTrue(backend.calls.get(0).contains("\"profile_name\":\"Travel\""));
        assertTrue(backend.calls.get(1).startsWith(
                "POST /api/v1/config/converter/import "));
        assertTrue(backend.calls.get(1).contains("\"expected_sha256\":\"abc123\""));
        assertTrue(backend.calls.get(1).contains("\"activate\":true"));
    }

    @Test
    void liveEventsUseTheWebSocketPathAndDecodeTypedFrames()
            throws InterruptedException {
        RecordingBackend backend = new RecordingBackend();
        List<RuntimeClient.Event> received = new ArrayList<>();
        CountDownLatch complete = new CountDownLatch(1);
        try (RuntimeClient client = new DefaultRuntimeClient(backend)) {
            assertTrue(client.supportsLiveEvents());
            client.liveEvents(new RuntimeClient.EventCursor(
                    5, 64, List.of("connection.*"), List.of("c 1")))
                    .subscribe(new Flow.Subscriber<>() {
                        @Override
                        public void onSubscribe(Flow.Subscription subscription) {
                            subscription.request(Long.MAX_VALUE);
                        }

                        @Override
                        public void onNext(RuntimeClient.Event event) {
                            received.add(event);
                        }

                        @Override
                        public void onError(Throwable throwable) {
                            complete.countDown();
                        }

                        @Override
                        public void onComplete() {
                            complete.countDown();
                        }
                    });
            assertTrue(complete.await(2, TimeUnit.SECONDS));
        }

        assertEquals("/api/v1/events?after=5&limit=64&types=connection.*&conn_id=c+1",
                backend.liveEventPath);
        assertEquals(1, received.size());
        assertEquals("connection.opened", received.get(0).type());
        assertEquals("c1", received.get(0).data().get("conn_id").text());
    }

    private static final class RecordingBackend implements Backend {
        private final List<String> calls = new ArrayList<>();
        private String liveEventPath = "";

        @Override
        public CompletableFuture<String> request(String method, String path,
                                                 String body) {
            calls.add(method + " " + path + " " + body);
            return CompletableFuture.completedFuture("{}");
        }

        @Override
        public String displayName() {
            return "recording";
        }

        @Override
        public boolean supportsConnectionControl() {
            return true;
        }

        @Override
        public boolean supportsLiveEvents() {
            return true;
        }

        @Override
        public Flow.Publisher<String> liveEvents(String path) {
            liveEventPath = path;
            return subscriber -> subscriber.onSubscribe(new Flow.Subscription() {
                private boolean sent;

                @Override
                public void request(long count) {
                    if (sent || count <= 0) return;
                    sent = true;
                    subscriber.onNext("{\"sequence\":6,\"shard_id\":1,"
                            + "\"lamport\":2,\"ts_ns\":3,"
                            + "\"type\":\"connection.opened\","
                            + "\"data\":{\"conn_id\":\"c1\"}}");
                    subscriber.onComplete();
                }

                @Override
                public void cancel() {
                    sent = true;
                }
            });
        }

        @Override
        public void close() {
        }
    }
}
