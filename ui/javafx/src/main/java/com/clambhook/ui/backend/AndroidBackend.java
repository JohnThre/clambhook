package com.clambhook.ui.backend;

import java.util.Objects;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** Gluon/Dalvik boundary. The JNI implementation is linked only for Android. */
public final class AndroidBackend implements Backend {
    private final ExecutorService executor = Executors.newSingleThreadExecutor(runnable -> {
        Thread thread = new Thread(runnable, "clambhook-android-backend");
        thread.setDaemon(true);
        return thread;
    });

    public AndroidBackend() {
        System.loadLibrary("clambhook_gluon_bridge");
        nativeInitialize();
    }

    @Override
    public CompletableFuture<String> request(String method, String path, String body) {
        String safeBody = Objects.requireNonNullElse(body, "");
        return CompletableFuture.supplyAsync(
                () -> nativeRequest(method, path, safeBody), executor);
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
        nativeClose();
    }

    private static native void nativeInitialize();

    private static native String nativeRequest(String method, String path, String body);

    private static native void nativeClose();
}
