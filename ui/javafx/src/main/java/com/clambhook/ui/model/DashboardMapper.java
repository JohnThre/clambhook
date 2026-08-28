package com.clambhook.ui.model;

import com.clambhook.ui.json.Json;

import java.util.ArrayList;
import java.util.List;
import java.util.Objects;
import java.util.StringJoiner;

/** Maps stable daemon/native JSON contracts into compact JavaFX view state. */
public final class DashboardMapper {
    private DashboardMapper() {
    }

    public static DashboardData map(
            String statusJson,
            String profilesJson,
            String serversJson,
            String rulesJson,
            String trafficJson,
            String dnsJson,
            String developerStatusJson,
            String developerEntriesJson) {
        Json.Node status = Json.parse(statusJson);
        Json.Node profiles = Json.parse(profilesJson);
        Json.Node servers = Json.parse(serversJson);
        Json.Node rules = Json.parse(rulesJson);
        Json.Node traffic = Json.parse(trafficJson);
        Json.Node dns = Json.parse(dnsJson);
        Json.Node developer = Json.parse(developerStatusJson);
        Json.Node entries = Json.parse(developerEntriesJson);

        String activeProfile = firstText(status.get("profile"), profiles.get("active"));
        List<String> profileNames = textList(profiles.get("profiles"));
        List<DashboardData.Listener> listenerRows = new ArrayList<>();
        for (Json.Node listener : status.get("listeners").elements()) {
            listenerRows.add(new DashboardData.Listener(
                    listener.get("protocol").text(),
                    listener.get("addr").text(),
                    listener.get("active_conns").longValue(0)));
        }

        List<DashboardData.Server> serverRows = new ArrayList<>();
        for (Json.Node chain : servers.get("chains").elements()) {
            String chainName = chain.get("name").text();
            for (Json.Node server : chain.get("servers").elements()) {
                Json.Node geo = server.get("geo");
                StringJoiner location = new StringJoiner(", ");
                addIfPresent(location, geo.get("city").text());
                addIfPresent(location, geo.get("country").text());
                serverRows.add(new DashboardData.Server(
                        chainName,
                        server.get("name").text(),
                        server.get("address").text(),
                        server.get("protocol").text(),
                        location.toString()));
            }
        }

        List<DashboardData.Rule> ruleRows = new ArrayList<>();
        for (Json.Node rule : rules.get("rules").elements()) {
            ruleRows.add(new DashboardData.Rule(
                    rule.get("name").text(),
                    rule.get("action").text(),
                    ruleMatches(rule)));
        }

        Json.Node summary = traffic.get("summary");
        DashboardData.TrafficSummary trafficSummary = new DashboardData.TrafficSummary(
                summary.get("active_connections").longValue(0),
                summary.get("rx_bps").doubleValue(0),
                summary.get("tx_bps").doubleValue(0),
                summary.get("rx_total").longValue(0),
                summary.get("tx_total").longValue(0));
        List<DashboardData.Connection> connectionRows = new ArrayList<>();
        for (Json.Node connection : traffic.get("connections").elements()) {
            connectionRows.add(new DashboardData.Connection(
                    connection.get("conn_id").text(),
                    firstText(connection.get("target"), connection.get("target_host")),
                    connection.get("network").text(),
                    connection.get("state").text(),
                    connection.get("rule_action").text(),
                    connection.get("chain_name").text(),
                    connection.get("application").text(),
                    connection.get("rx_bps").doubleValue(0),
                    connection.get("tx_bps").doubleValue(0),
                    connection.get("rx_total").longValue(0),
                    connection.get("tx_total").longValue(0)));
        }

        List<String> upstreams = new ArrayList<>();
        for (Json.Node upstream : dns.get("upstreams").elements()) {
            String name = upstream.get("name").text();
            String protocol = upstream.get("protocol").text();
            String endpoint = firstText(upstream.get("url"), upstream.get("address"), upstream.get("server_name"));
            StringBuilder label = new StringBuilder();
            if (!name.isBlank()) {
                label.append(name);
            }
            if (!protocol.isBlank()) {
                if (!label.isEmpty()) {
                    label.append(" · ");
                }
                label.append(protocol.toUpperCase());
            }
            if (!endpoint.isBlank()) {
                if (!label.isEmpty()) {
                    label.append(" · ");
                }
                label.append(endpoint);
            }
            upstreams.add(label.toString());
        }

        List<DashboardData.Capture> captures = new ArrayList<>();
        Json.Node captureArray = entries.get("entries");
        if (!captureArray.isArray() && entries.isArray()) {
            captureArray = entries;
        }
        for (Json.Node entry : captureArray.elements()) {
            long statusCode = entry.get("status").longValue(
                    entry.get("status_code").longValue(0));
            captures.add(new DashboardData.Capture(
                    entry.get("id").text(),
                    entry.get("method").text(),
                    entry.get("url").text(),
                    statusCode,
                    entry.get("error").text()));
        }

        return new DashboardData(
                status.get("running").bool(false),
                activeProfile,
                profileNames,
                listenerRows,
                serverRows,
                ruleRows,
                trafficSummary,
                connectionRows,
                new DashboardData.Dns(
                        dns.get("enabled").bool(false),
                        dns.get("strategy").text("route"),
                        upstreams),
                new DashboardData.Developer(
                        developer.get("enabled").bool(false),
                        developer.get("capture_count").longValue(captures.size()),
                        captures));
    }

    private static List<String> textList(Json.Node node) {
        List<String> result = new ArrayList<>();
        for (Json.Node item : node.elements()) {
            String value = item.text();
            if (!value.isBlank()) {
                result.add(value);
            }
        }
        return result;
    }

    private static String ruleMatches(Json.Node rule) {
        StringJoiner result = new StringJoiner(" · ");
        appendMatches(result, "domain", rule.get("domains"));
        appendMatches(result, "suffix", rule.get("domain_suffixes"));
        appendMatches(result, "keyword", rule.get("domain_keywords"));
        appendMatches(result, "CIDR", rule.get("cidrs"));
        appendMatches(result, "port", rule.get("ports"));
        appendMatches(result, "network", rule.get("networks"));
        return result.length() == 0 ? "All traffic" : result.toString();
    }

    private static void appendMatches(StringJoiner output, String label, Json.Node values) {
        List<Json.Node> elements = values.elements();
        if (elements.isEmpty()) {
            return;
        }
        StringJoiner items = new StringJoiner(", ");
        int visible = Math.min(elements.size(), 3);
        for (int index = 0; index < visible; index++) {
            Json.Node item = elements.get(index);
            String text = item.text();
            if (text.isBlank()) {
                long number = item.longValue(Long.MIN_VALUE);
                text = number == Long.MIN_VALUE ? item.toString() : Long.toString(number);
            }
            items.add(text);
        }
        if (elements.size() > visible) {
            items.add("+" + (elements.size() - visible));
        }
        output.add(label + ": " + items);
    }

    private static String firstText(Json.Node... values) {
        for (Json.Node value : values) {
            String text = value.text();
            if (!text.isBlank()) {
                return text;
            }
        }
        return "";
    }

    private static void addIfPresent(StringJoiner output, String value) {
        if (!Objects.requireNonNullElse(value, "").isBlank()) {
            output.add(value);
        }
    }
}
