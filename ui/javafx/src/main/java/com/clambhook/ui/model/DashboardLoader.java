package com.clambhook.ui.model;

import com.clambhook.ui.backend.Backend;

import java.util.concurrent.CompletableFuture;

/** Fetches one coherent dashboard generation without blocking the JavaFX thread. */
public final class DashboardLoader {
    private final Backend backend;

    public DashboardLoader(Backend backend) {
        this.backend = backend;
    }

    public CompletableFuture<DashboardData> load() {
        CompletableFuture<String> status = backend.get("/api/v1/status");
        CompletableFuture<String> profiles = backend.get("/api/v1/profiles");
        CompletableFuture<String> servers = backend.get("/api/v1/servers");
        CompletableFuture<String> rules = backend.get("/api/v1/rules");
        CompletableFuture<String> traffic = backend.get("/api/v1/traffic?limit=200");
        CompletableFuture<String> dns = backend.get("/api/v1/dns");
        CompletableFuture<String> developer = backend.get("/api/v1/developer/status");
        CompletableFuture<String> entries = backend.get("/api/v1/developer/entries?limit=200");
        return CompletableFuture.allOf(
                        status, profiles, servers, rules, traffic, dns, developer, entries)
                .thenApply(ignored -> DashboardMapper.map(
                        status.join(), profiles.join(), servers.join(), rules.join(),
                        traffic.join(), dns.join(), developer.join(), entries.join()));
    }
}
