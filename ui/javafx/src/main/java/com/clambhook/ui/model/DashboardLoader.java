// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.model;

import com.clambhook.ui.runtime.RuntimeClient;

import java.util.concurrent.CompletableFuture;

/** Fetches one coherent dashboard generation without blocking the JavaFX thread. */
public final class DashboardLoader {
    private final RuntimeClient runtime;

    public DashboardLoader(RuntimeClient runtime) {
        this.runtime = runtime;
    }

    public CompletableFuture<DashboardData> load() {
        CompletableFuture<RuntimeClient.Status> status = runtime.status();
        CompletableFuture<RuntimeClient.Profiles> profiles = runtime.profiles();
        CompletableFuture<RuntimeClient.Document> servers = runtime.servers("");
        CompletableFuture<RuntimeClient.Document> rules = runtime.rules("");
        CompletableFuture<RuntimeClient.Document> traffic =
                runtime.traffic(RuntimeClient.TrafficQuery.recent(200));
        CompletableFuture<RuntimeClient.Document> dns = runtime.dns("");
        CompletableFuture<RuntimeClient.Document> developer = runtime.developerStatus();
        CompletableFuture<RuntimeClient.Document> entries =
                runtime.developerEntries("limit=200");
        return CompletableFuture.allOf(
                        status, profiles, servers, rules, traffic, dns, developer, entries)
                .thenApply(ignored -> DashboardMapper.map(
                        status.join().document().rawJson(),
                        profiles.join().document().rawJson(),
                        servers.join().rawJson(), rules.join().rawJson(),
                        traffic.join().rawJson(), dns.join().rawJson(),
                        developer.join().rawJson(), entries.join().rawJson()));
    }
}
