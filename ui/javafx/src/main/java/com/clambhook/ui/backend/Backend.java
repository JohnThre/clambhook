package com.clambhook.ui.backend;

import java.util.concurrent.CompletableFuture;

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

    String displayName();

    boolean supportsConnectionControl();

    @Override
    void close();
}
