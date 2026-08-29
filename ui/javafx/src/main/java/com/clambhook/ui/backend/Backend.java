// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.backend;

import java.util.concurrent.CompletableFuture;
import java.util.concurrent.Flow;

/** Platform boundary used by the shared JavaFX controller. */
public interface Backend extends AutoCloseable {
    CompletableFuture<String> request(String method, String path, String body);

    default CompletableFuture<String> get(String path) {
        return request("GET", path, "");
    }

    default CompletableFuture<String> post(String path, String body) {
        return request("POST", path, body);
    }

    default CompletableFuture<String> put(String path, String body) {
        return request("PUT", path, body);
    }

    default boolean supportsLiveEvents() {
        return false;
    }

    default Flow.Publisher<String> liveEvents(String path) {
        throw new UnsupportedOperationException("this backend has no live event stream");
    }

    String displayName();

    boolean supportsConnectionControl();

    @Override
    void close();
}
