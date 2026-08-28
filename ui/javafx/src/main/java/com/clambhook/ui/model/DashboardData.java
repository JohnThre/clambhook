// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.model;

import java.util.List;

/** Immutable view state shared by the Android and GNU/Linux JavaFX layouts. */
public record DashboardData(
        boolean running,
        String activeProfile,
        List<String> profiles,
        List<Listener> listeners,
        List<Server> servers,
        List<Rule> rules,
        TrafficSummary traffic,
        List<Connection> connections,
        Dns dns,
        Developer developer) {

    public DashboardData {
        activeProfile = safe(activeProfile);
        profiles = List.copyOf(profiles);
        listeners = List.copyOf(listeners);
        servers = List.copyOf(servers);
        rules = List.copyOf(rules);
        connections = List.copyOf(connections);
    }

    public static DashboardData empty() {
        return new DashboardData(false, "", List.of(), List.of(), List.of(), List.of(),
                new TrafficSummary(0, 0, 0, 0, 0), List.of(),
                new Dns(false, "route", List.of()), new Developer(false, 0, List.of()));
    }

    private static String safe(String value) {
        return value == null ? "" : value;
    }

    public record Listener(String protocol, String address, long activeConnections) {
    }

    public record Server(String chain, String name, String address, String protocol, String location) {
    }

    public record Rule(String name, String action, String matchSummary) {
    }

    public record TrafficSummary(
            long activeConnections,
            double downloadBytesPerSecond,
            double uploadBytesPerSecond,
            long receivedBytes,
            long transmittedBytes) {
    }

    public record Connection(
            String id,
            String target,
            String network,
            String state,
            String action,
            String chain,
            String application,
            double downloadBytesPerSecond,
            double uploadBytesPerSecond,
            long receivedBytes,
            long transmittedBytes) {
    }

    public record Dns(boolean enabled, String strategy, List<String> upstreams) {
        public Dns {
            upstreams = List.copyOf(upstreams);
        }
    }

    public record Developer(boolean enabled, long captureCount, List<Capture> captures) {
        public Developer {
            captures = List.copyOf(captures);
        }
    }

    public record Capture(String id, String method, String url, long status, String error) {
    }
}
