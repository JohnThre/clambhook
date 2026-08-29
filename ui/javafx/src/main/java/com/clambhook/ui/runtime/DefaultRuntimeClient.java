// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.runtime;

import com.clambhook.ui.backend.Backend;
import com.clambhook.ui.backend.ConfigurableEndpointBackend;

import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.util.Objects;
import java.util.Optional;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.Flow;

/** Default transport-neutral runtime client. */
public final class DefaultRuntimeClient implements RuntimeClient {
    private final Backend backend;

    public DefaultRuntimeClient(Backend backend) {
        this.backend = Objects.requireNonNull(backend, "backend");
    }

    @Override
    public CompletableFuture<Status> status() {
        return backend.get("/api/v1/status").thenApply(Status::parse);
    }

    @Override
    public CompletableFuture<Profiles> profiles() {
        return backend.get("/api/v1/profiles").thenApply(Profiles::parse);
    }

    @Override
    public CompletableFuture<Document> servers(String profile) {
        return getProfile("/api/v1/servers", profile);
    }

    @Override
    public CompletableFuture<Document> rules(String profile) {
        return getProfile("/api/v1/rules", profile);
    }

    @Override
    public CompletableFuture<Document> traffic(TrafficQuery query) {
        return get("/api/v1/traffic" + Objects.requireNonNull(query, "query").queryString());
    }

    @Override
    public CompletableFuture<Document> decisions(TrafficQuery query) {
        return get("/api/v1/decisions" + Objects.requireNonNull(query, "query").queryString());
    }

    @Override
    public CompletableFuture<Document> temporaryRules(String profile) {
        return getProfile("/api/v1/rules/temporary", profile);
    }

    @Override
    public CompletableFuture<Document> policyGroups(String profile) {
        return getProfile("/api/v1/policy-groups", profile);
    }

    @Override
    public CompletableFuture<Document> ruleSets(String profile) {
        return getProfile("/api/v1/rule-sets", profile);
    }

    @Override
    public CompletableFuture<Document> ruleSubscriptions(String profile) {
        return getProfile("/api/v1/rule-subscriptions", profile);
    }

    @Override
    public CompletableFuture<Document> dns(String profile) {
        return getProfile("/api/v1/dns", profile);
    }

    @Override
    public CompletableFuture<Document> settings(String profile) {
        return getProfile("/api/v1/config/settings", profile);
    }

    @Override
    public CompletableFuture<Document> conditioner(String profile) {
        return getProfile("/api/v1/conditioner", profile);
    }

    @Override
    public CompletableFuture<Document> pendingPrompts() {
        return get("/api/v1/prompts/pending");
    }

    @Override
    public CompletableFuture<Document> promptDecisions() {
        return get("/api/v1/prompts/decisions");
    }

    @Override
    public CompletableFuture<Document> developerStatus() {
        return get("/api/v1/developer/status");
    }

    @Override
    public CompletableFuture<Document> developerEntries(String query) {
        String suffix = Objects.requireNonNullElse(query, "").trim();
        if (!suffix.isEmpty() && suffix.charAt(0) != '?') suffix = "?" + suffix;
        return get("/api/v1/developer/entries" + suffix);
    }

    @Override
    public CompletableFuture<Document> developerSettings() {
        return get("/api/v1/developer/settings");
    }

    @Override
    public CompletableFuture<Document> developerMapRules(String profile) {
        return getProfile("/api/v1/developer/map-rules", profile);
    }

    @Override
    public CompletableFuture<Document> developerBreakpointRules(String profile) {
        return getProfile("/api/v1/developer/breakpoint-rules", profile);
    }

    @Override
    public CompletableFuture<Document> developerRewriteRules(String profile) {
        return getProfile("/api/v1/developer/rewrite-rules", profile);
    }

    @Override
    public CompletableFuture<Document> developerPendingBreakpoints() {
        return get("/api/v1/developer/breakpoints/pending");
    }

    @Override
    public CompletableFuture<String> developerCaPem() {
        return backend.get("/api/v1/developer/ca.pem");
    }

    @Override
    public CompletableFuture<EventBatch> events(EventCursor cursor) {
        String path = "/api/v1/events/snapshot" +
                Objects.requireNonNull(cursor, "cursor").queryString();
        return backend.get(path).thenApply(EventBatch::parse);
    }

    @Override
    public boolean supportsLiveEvents() {
        return backend.supportsLiveEvents();
    }

    @Override
    public Flow.Publisher<Event> liveEvents(EventCursor cursor) {
        if (!backend.supportsLiveEvents()) {
            throw new UnsupportedOperationException(
                    "this platform has no live event stream");
        }
        String path = "/api/v1/events" +
                Objects.requireNonNull(cursor, "cursor").queryString();
        Flow.Publisher<String> source = backend.liveEvents(path);
        return subscriber -> source.subscribe(new Flow.Subscriber<>() {
            private Flow.Subscription subscription;

            @Override
            public void onSubscribe(Flow.Subscription value) {
                subscription = value;
                subscriber.onSubscribe(value);
            }

            @Override
            public void onNext(String item) {
                Event event;
                try {
                    event = Event.parse(item);
                } catch (RuntimeException error) {
                    subscription.cancel();
                    subscriber.onError(error);
                    return;
                }
                subscriber.onNext(event);
            }

            @Override
            public void onError(Throwable throwable) {
                subscriber.onError(throwable);
            }

            @Override
            public void onComplete() {
                subscriber.onComplete();
            }
        });
    }

    @Override
    public CompletableFuture<String> rawRequest(String method, String path, String body) {
        return backend.request(method, path, body);
    }

    @Override
    public CompletableFuture<Document> request(String method, String path, String body) {
        return backend.request(method, path, body).thenApply(Document::parse);
    }

    private CompletableFuture<Document> get(String path) {
        return backend.get(path).thenApply(Document::parse);
    }

    private CompletableFuture<Document> getProfile(String path, String profile) {
        String value = Objects.requireNonNullElse(profile, "").trim();
        if (!value.isBlank()) {
            path += "?profile=" + URLEncoder.encode(value, StandardCharsets.UTF_8);
        }
        return get(path);
    }

    @Override
    public String displayName() {
        return backend.displayName();
    }

    @Override
    public boolean supportsConnectionControl() {
        return backend.supportsConnectionControl();
    }

    @Override
    public Optional<EndpointSettings> endpointSettings() {
        if (backend instanceof ConfigurableEndpointBackend configurable) {
            return Optional.of(new EndpointSettings(configurable.baseUrl(), ""));
        }
        return Optional.empty();
    }

    @Override
    public void configureEndpoint(EndpointSettings settings) {
        if (!(backend instanceof ConfigurableEndpointBackend configurable)) {
            throw new UnsupportedOperationException("this platform has no configurable endpoint");
        }
        configurable.configure(settings.baseUrl(), settings.bearerToken());
    }

    @Override
    public void close() {
        backend.close();
    }
}
