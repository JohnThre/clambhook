// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.backend;

import com.clambhook.ui.platform.AndroidDalvikBridge;

import java.util.Objects;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** Gluon boundary to the process-global Kotlin platform AAR. */
public final class AndroidBackend implements Backend {
    private final ExecutorService executor = Executors.newSingleThreadExecutor(runnable -> {
        Thread thread = new Thread(runnable, "clambhook-android-backend");
        thread.setDaemon(true);
        return thread;
    });
    public AndroidBackend() {
    }

    @Override
    public CompletableFuture<String> request(String method, String path, String body) {
        String safeBody = Objects.requireNonNullElse(body, "");
        return CompletableFuture.supplyAsync(
                () -> invokeRequest(method, path, safeBody), executor);
    }

    @Override
    public String displayName() {
        return "Android on-device runtime";
    }

    @Override
    public boolean supportsConnectionControl() {
        return true;
    }

    @Override
    public void close() {
        executor.shutdownNow();
    }

    private String invokeRequest(String method, String path, String body) {
        try {
            return AndroidDalvikBridge.safeRequest(method, path, body);
        } catch (RuntimeException error) {
            Throwable cause = error.getCause() == null ? error : error.getCause();
            throw new BackendException(0,
                    Objects.requireNonNullElse(cause.getMessage(), "Android runtime request failed"),
                    cause);
        }
    }
}
