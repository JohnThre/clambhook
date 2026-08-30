// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.runtime;

import com.clambhook.ui.json.Json;

import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Optional;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.Flow;

/**
 * Typed, asynchronous view of the frozen C runtime control and event
 * contracts. Views depend on this interface instead of HTTP, JNI, or Android
 * lifecycle classes.
 */
public interface RuntimeClient extends AutoCloseable {
    CompletableFuture<Status> status();

    CompletableFuture<Profiles> profiles();

    CompletableFuture<Document> servers(String profile);

    CompletableFuture<Document> rules(String profile);

    CompletableFuture<Document> traffic(TrafficQuery query);

    CompletableFuture<Document> decisions(TrafficQuery query);

    CompletableFuture<Document> temporaryRules(String profile);

    CompletableFuture<Document> policyGroups(String profile);

    CompletableFuture<Document> ruleSets(String profile);

    CompletableFuture<Document> ruleSubscriptions(String profile);

    CompletableFuture<Document> dns(String profile);

    CompletableFuture<Document> settings(String profile);

    CompletableFuture<Document> conditioner(String profile);

    CompletableFuture<Document> pendingPrompts();

    CompletableFuture<Document> promptDecisions();

    CompletableFuture<Document> developerStatus();

    CompletableFuture<Document> developerEntries(String query);

    CompletableFuture<Document> developerSettings();

    CompletableFuture<Document> developerMapRules(String profile);

    CompletableFuture<Document> developerBreakpointRules(String profile);

    CompletableFuture<Document> developerRewriteRules(String profile);

    CompletableFuture<Document> developerPendingBreakpoints();

    CompletableFuture<String> developerCaPem();

    CompletableFuture<EventBatch> events(EventCursor cursor);

    boolean supportsLiveEvents();

    Flow.Publisher<Event> liveEvents(EventCursor cursor);

    CompletableFuture<String> rawRequest(String method, String path, String body);

    CompletableFuture<Document> request(String method, String path, String body);

    default CompletableFuture<Document> connect() {
        return request("POST", "/api/v1/connect", "{}");
    }

    default CompletableFuture<Document> disconnect() {
        return request("POST", "/api/v1/disconnect", "{}");
    }

    default CompletableFuture<Document> setActiveProfile(String profile) {
        return request("PUT", "/api/v1/profiles/active",
                Json.object(Map.of("name", Objects.requireNonNullElse(profile, ""))));
    }

    default CompletableFuture<String> exportConfig() {
        return rawRequest("GET", "/api/v1/config/export", "");
    }

    default CompletableFuture<Document> importConfig(String toml) {
        return rawRequest("POST", "/api/v1/config/import",
                Objects.requireNonNullElse(toml, "")).thenApply(Document::parse);
    }

    default CompletableFuture<Document> reviewOutlineAccessKey(String accessKey) {
        return request("POST", "/api/v1/outline/review",
                Json.object(Map.of("access_key",
                        Objects.requireNonNullElse(accessKey, ""))));
    }

    default CompletableFuture<Document> importOutlineAccessKey(
            String accessKey, String profileName, boolean activate) {
        return request("POST", "/api/v1/outline/import", Json.object(Map.of(
                "access_key", Objects.requireNonNullElse(accessKey, ""),
                "profile_name", Objects.requireNonNullElse(profileName, ""),
                "activate", activate)));
    }

    default CompletableFuture<Document> refreshOutlineProfile(String profile) {
        return request("POST", "/api/v1/outline/refresh",
                Json.object(Map.of("profile", Objects.requireNonNullElse(profile, ""))));
    }

    default CompletableFuture<Document> reviewProfileConversion(
            String source, String format, String profileName) {
        return request("POST", "/api/v1/config/converter/review",
                Json.object(Map.of(
                        "source", Objects.requireNonNullElse(source, ""),
                        "format", Objects.requireNonNullElse(format, "auto"),
                        "profile_name", Objects.requireNonNullElse(profileName, ""))));
    }

    default CompletableFuture<Document> importProfileConversion(
            String source, String format, String profileName,
            String expectedSha256, boolean activate) {
        return request("POST", "/api/v1/config/converter/import",
                Json.object(Map.of(
                        "source", Objects.requireNonNullElse(source, ""),
                        "format", Objects.requireNonNullElse(format, "auto"),
                        "profile_name", Objects.requireNonNullElse(profileName, ""),
                        "expected_sha256", Objects.requireNonNullElse(expectedSha256, ""),
                        "activate", activate)));
    }

    default CompletableFuture<Document> updateDns(String json) {
        return request("PUT", "/api/v1/dns", json);
    }

    default CompletableFuture<Document> updateSettings(String json) {
        return request("PUT", "/api/v1/config/settings", json);
    }

    default CompletableFuture<Document> updateConditioner(String json) {
        return request("PUT", "/api/v1/conditioner", json);
    }

    default CompletableFuture<Document> replaceRules(String json) {
        return request("PUT", "/api/v1/rules", json);
    }

    default CompletableFuture<Document> createRule(String json) {
        return request("POST", "/api/v1/rules", json);
    }

    default CompletableFuture<Document> replacePolicyGroups(String json) {
        return request("PUT", "/api/v1/policy-groups", json);
    }

    default CompletableFuture<Document> testPolicyGroups(String json) {
        return request("POST", "/api/v1/policy-groups/test", json);
    }

    default CompletableFuture<Document> selectPolicyGroup(String json) {
        return request("PUT", "/api/v1/policy-groups/selection", json);
    }

    default CompletableFuture<Document> replaceRuleSets(String json) {
        return request("PUT", "/api/v1/rule-sets", json);
    }

    default CompletableFuture<Document> refreshRuleSets(String json) {
        return request("POST", "/api/v1/rule-sets/refresh", json);
    }

    default CompletableFuture<Document> replaceRuleSubscriptions(String json) {
        return request("PUT", "/api/v1/rule-subscriptions", json);
    }

    default CompletableFuture<Document> refreshRuleSubscriptions(String json) {
        return request("POST", "/api/v1/rule-subscriptions/refresh", json);
    }

    default CompletableFuture<Document> createRuleFromConnection(String json) {
        return request("POST", "/api/v1/rules/from-connection", json);
    }

    default CompletableFuture<Document> createTemporaryRuleFromConnection(String json) {
        return request("POST", "/api/v1/rules/temporary/from-connection", json);
    }

    default CompletableFuture<Document> removeTemporaryRule(String identifier) {
        return request("DELETE", "/api/v1/rules/temporary/" +
                encodePath(identifier), "{}");
    }

    default CompletableFuture<Document> resolvePrompt(String identifier, String json) {
        return request("POST", "/api/v1/prompts/" + encodePath(identifier) +
                "/resolve", json);
    }

    default CompletableFuture<Document> promotePromptDecision(String identifier,
                                                               String json) {
        return request("POST", "/api/v1/prompts/decisions/" +
                encodePath(identifier) + "/promote", json);
    }

    default CompletableFuture<Document> updateDeveloperSettings(String json) {
        return request("PUT", "/api/v1/developer/settings", json);
    }

    default CompletableFuture<Document> clearDeveloperEntries() {
        return request("DELETE", "/api/v1/developer/entries", "{}");
    }

    default CompletableFuture<Document> importCurl(String json) {
        return request("POST", "/api/v1/developer/curl/import", json);
    }

    default CompletableFuture<Document> sendDeveloperRequest(String json) {
        return request("POST", "/api/v1/developer/send", json);
    }

    default CompletableFuture<Document> replaceDeveloperMapRules(String json) {
        return request("PUT", "/api/v1/developer/map-rules", json);
    }

    default CompletableFuture<Document> replaceDeveloperBreakpointRules(String json) {
        return request("PUT", "/api/v1/developer/breakpoint-rules", json);
    }

    default CompletableFuture<Document> replaceDeveloperRewriteRules(String json) {
        return request("PUT", "/api/v1/developer/rewrite-rules", json);
    }

    default CompletableFuture<Document> regenerateDeveloperCa() {
        return request("POST", "/api/v1/developer/ca/regenerate", "{}");
    }

    default CompletableFuture<Document> resolveDeveloperBreakpoint(
            String identifier, String json) {
        return request("POST", "/api/v1/developer/breakpoints/" +
                encodePath(identifier) + "/resolve", json);
    }

    String displayName();

    boolean supportsConnectionControl();

    Optional<EndpointSettings> endpointSettings();

    void configureEndpoint(EndpointSettings settings);

    @Override
    void close();

    record EndpointSettings(String baseUrl, String bearerToken) {
        public EndpointSettings {
            baseUrl = Objects.requireNonNullElse(baseUrl, "").trim();
            bearerToken = Objects.requireNonNullElse(bearerToken, "").trim();
        }
    }

    record Document(String rawJson, Json.Node root) {
        public Document {
            rawJson = Objects.requireNonNullElse(rawJson, "");
            root = Objects.requireNonNull(root, "root");
        }

        public static Document parse(String rawJson) {
            return new Document(rawJson, Json.parse(rawJson));
        }
    }

    record Listener(String protocol, String address, long activeConnections) {
        public Listener {
            protocol = Objects.requireNonNullElse(protocol, "");
            address = Objects.requireNonNullElse(address, "");
        }
    }

    record Status(boolean running, String profile, String tunnelMode,
                  List<Listener> listeners, Document document) {
        public Status {
            profile = Objects.requireNonNullElse(profile, "");
            tunnelMode = Objects.requireNonNullElse(tunnelMode, "");
            listeners = List.copyOf(listeners);
            document = Objects.requireNonNull(document, "document");
        }

        public static Status parse(String rawJson) {
            Document document = Document.parse(rawJson);
            Json.Node root = document.root();
            List<Listener> listeners = new ArrayList<>();
            for (Json.Node value : root.get("listeners").elements()) {
                listeners.add(new Listener(
                        value.get("protocol").text(),
                        value.get("addr").text(),
                        value.get("active_conns").longValue(0)));
            }
            return new Status(
                    root.get("running").bool(false),
                    root.get("profile").text(),
                    root.get("tunnel_mode").text(),
                    listeners,
                    document);
        }
    }

    record Profiles(List<String> names, String active, Document document) {
        public Profiles {
            names = List.copyOf(names);
            active = Objects.requireNonNullElse(active, "");
            document = Objects.requireNonNull(document, "document");
        }

        public static Profiles parse(String rawJson) {
            Document document = Document.parse(rawJson);
            List<String> names = new ArrayList<>();
            for (Json.Node value : document.root().get("profiles").elements()) {
                String name = value.text();
                if (!name.isBlank()) {
                    names.add(name);
                }
            }
            return new Profiles(names, document.root().get("active").text(), document);
        }
    }

    record TrafficQuery(String profile, String search, String state,
                        String network, String rule, String chain,
                        String application, long after, int limit) {
        public TrafficQuery {
            profile = clean(profile);
            search = clean(search);
            state = clean(state);
            network = clean(network);
            rule = clean(rule);
            chain = clean(chain);
            application = clean(application);
            after = Math.max(0, after);
            limit = Math.max(0, limit);
        }

        public static TrafficQuery recent(int limit) {
            return new TrafficQuery("", "", "", "", "", "", "", 0, limit);
        }

        String queryString() {
            StringBuilder query = new StringBuilder();
            append(query, "profile", profile);
            append(query, "query", search);
            append(query, "state", state);
            append(query, "network", network);
            append(query, "rule", rule);
            append(query, "chain", chain);
            append(query, "application", application);
            if (after > 0) append(query, "after", Long.toString(after));
            if (limit > 0) append(query, "limit", Integer.toString(limit));
            return query.toString();
        }
    }

    record EventCursor(long after, int limit, List<String> types,
                       List<String> connectionIds) {
        public EventCursor {
            after = Math.max(0, after);
            limit = limit <= 0 ? 256 : Math.min(limit, 4096);
            types = List.copyOf(types == null ? List.of() : types);
            connectionIds = List.copyOf(connectionIds == null ? List.of() : connectionIds);
        }

        public static EventCursor beginning() {
            return new EventCursor(0, 256, List.of(), List.of());
        }

        String queryString() {
            StringBuilder query = new StringBuilder();
            append(query, "after", Long.toString(after));
            append(query, "limit", Integer.toString(limit));
            for (String type : types) append(query, "types", clean(type));
            for (String id : connectionIds) append(query, "conn_id", clean(id));
            return query.toString();
        }
    }

    record Event(long sequence, long shardId, long lamport, long timestampNs,
                 String type, Json.Node data) {
        public Event {
            type = Objects.requireNonNullElse(type, "");
            data = Objects.requireNonNull(data, "data");
        }

        public static Event parse(String rawJson) {
            Json.Node value = Json.parse(rawJson);
            if (!value.isObject()) {
                throw new IllegalArgumentException("event must be a JSON object");
            }
            return new Event(
                    value.get("sequence").longValue(0),
                    value.get("shard_id").longValue(0),
                    value.get("lamport").longValue(0),
                    value.get("ts_ns").longValue(
                            value.get("timestamp_ns").longValue(0)),
                    value.get("type").text(),
                    value.get("data"));
        }
    }

    record EventBatch(List<Event> events, long nextSequence, boolean complete,
                      Document document) {
        public EventBatch {
            events = List.copyOf(events);
            document = Objects.requireNonNull(document, "document");
        }

        public static EventBatch parse(String rawJson) {
            Document document = Document.parse(rawJson);
            List<Event> events = new ArrayList<>();
            for (Json.Node value : document.root().get("events").elements()) {
                events.add(new Event(
                        value.get("sequence").longValue(0),
                        value.get("shard_id").longValue(0),
                        value.get("lamport").longValue(0),
                        value.get("ts_ns").longValue(
                                value.get("timestamp_ns").longValue(0)),
                        value.get("type").text(),
                        value.get("data")));
            }
            return new EventBatch(
                    events,
                    document.root().get("next_sequence").longValue(0),
                    document.root().get("complete").bool(true),
                    document);
        }
    }

    private static String clean(String value) {
        return Objects.requireNonNullElse(value, "").trim();
    }

    private static String encodePath(String value) {
        return URLEncoder.encode(clean(value), StandardCharsets.UTF_8)
                .replace("+", "%20");
    }

    private static void append(StringBuilder query, String key, String value) {
        if (value.isBlank()) return;
        query.append(query.isEmpty() ? '?' : '&')
                .append(URLEncoder.encode(key, StandardCharsets.UTF_8))
                .append('=')
                .append(URLEncoder.encode(value, StandardCharsets.UTF_8));
    }
}
