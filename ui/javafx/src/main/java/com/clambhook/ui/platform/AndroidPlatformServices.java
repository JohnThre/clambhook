// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.platform;

import com.clambhook.ui.json.Json;

import java.nio.file.Path;
import java.util.EnumSet;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Set;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** JNI-safe boundary to the Kotlin Android platform AAR. */
public final class AndroidPlatformServices implements PlatformServices {
    private final ExecutorService executor = Executors.newSingleThreadExecutor(runnable -> {
        Thread thread = new Thread(runnable, "clambhook-android-platform");
        thread.setDaemon(true);
        return thread;
    });
    public AndroidPlatformServices() {
    }

    @Override
    public Set<Capability> capabilities() {
        EnumSet<Capability> capabilities = EnumSet.allOf(Capability.class);
        capabilities.remove(Capability.DAEMON_SUPERVISION);
        return Set.copyOf(capabilities);
    }

    @Override
    public CompletableFuture<Boolean> requestVpnConsent() {
        return call("vpn-consent", "{}").thenApply(value -> Json.parse(value).get("granted").bool(false));
    }

    @Override
    public CompletableFuture<Void> startVpn() {
        return call("vpn-start", "{}").thenApply(ignored -> null);
    }

    @Override
    public CompletableFuture<Void> stopVpn() {
        return call("vpn-stop", "{}").thenApply(ignored -> null);
    }

    @Override
    public CompletableFuture<String> readTextFile(Path path, int maximumBytes) {
        return call("file-read", Json.object(Map.of(
                "path", Objects.requireNonNull(path, "path").toString(),
                "maximum_bytes", maximumBytes)));
    }

    @Override
    public CompletableFuture<Void> writeTextFile(Path path, String value) {
        return call("file-write", Json.object(Map.of(
                "path", Objects.requireNonNull(path, "path").toString(),
                "value", Objects.requireNonNullElse(value, "")))).thenApply(ignored -> null);
    }

    @Override
    public CompletableFuture<String> scanQrCode() {
        return call("qr-scan", "{}");
    }

    @Override
    public CompletableFuture<Void> shareQrCode(String value) {
        return call("qr-share", Json.object(Map.of("value", Objects.requireNonNullElse(value, ""))))
                .thenApply(ignored -> null);
    }

    @Override
    public CompletableFuture<String> secureRead(String key) {
        return call("secure-read", keyRequest(key));
    }

    @Override
    public CompletableFuture<Void> secureWrite(String key, String value) {
        return call("secure-write", Json.object(Map.of(
                "key", Objects.requireNonNullElse(key, ""),
                "value", Objects.requireNonNullElse(value, "")))).thenApply(ignored -> null);
    }

    @Override
    public CompletableFuture<Void> secureDelete(String key) {
        return call("secure-delete", keyRequest(key)).thenApply(ignored -> null);
    }

    @Override
    public CompletableFuture<String> clipboardRead() {
        return call("clipboard-read", "{}");
    }

    @Override
    public CompletableFuture<Void> clipboardWrite(String value) {
        return call("clipboard-write", Json.object(Map.of("value", Objects.requireNonNullElse(value, ""))))
                .thenApply(ignored -> null);
    }

    @Override
    public CompletableFuture<String> takePendingOutlineAccessKey() {
        return call("outline-link-consume", "{}");
    }

    @Override
    public CompletableFuture<Void> openBrowser(String uri) {
        return call("browser-open", Json.object(Map.of("uri", Objects.requireNonNullElse(uri, ""))))
                .thenApply(ignored -> null);
    }

    @Override
    public CompletableFuture<Void> notify(String title, String body) {
        return call("notify", Json.object(Map.of(
                "title", Objects.requireNonNullElse(title, ""),
                "body", Objects.requireNonNullElse(body, "")))).thenApply(ignored -> null);
    }

    @Override
    public CompletableFuture<List<InstalledApplication>> installedApplications() {
        return call("installed-apps", "{}").thenApply(value ->
                Json.parse(value).get("applications").elements().stream()
                        .map(application -> new InstalledApplication(
                                application.get("package_name").text(),
                                application.get("label").text(),
                                application.get("system").bool(false)))
                        .toList());
    }

    @Override
    public CompletableFuture<AppRoutingSettings> appRoutingSettings() {
        return call("per-app-routing-status", "{}").thenApply(
                AndroidPlatformServices::decodeRoutingSettings);
    }

    @Override
    public CompletableFuture<AppRoutingSettings> updateAppRoutingSettings(
            String mode, Set<String> packageNames) {
        return call("per-app-routing-update", Json.object(Map.of(
                "mode", Objects.requireNonNullElse(mode, "all"),
                "packages", Set.copyOf(Objects.requireNonNullElse(packageNames, Set.of())))))
                .thenApply(AndroidPlatformServices::decodeRoutingSettings);
    }

    @Override
    public CompletableFuture<Result> licensing(String operation, String requestJson) {
        return call("license-" + Objects.requireNonNullElse(operation, ""), requestJson)
                .thenApply(value -> new Result(true, value, ""));
    }

    @Override
    public CompletableFuture<Result> updates(String operation, String requestJson) {
        return call("update-" + Objects.requireNonNullElse(operation, ""), requestJson)
                .thenApply(value -> new Result(true, value, ""));
    }

    @Override
    public String platformName() {
        return "Android";
    }

    @Override
    public void close() {
        executor.shutdownNow();
    }

    private CompletableFuture<String> call(String operation, String request) {
        return CompletableFuture.supplyAsync(() -> {
            try {
                return AndroidDalvikBridge.safeDispatch(
                        operation, Objects.requireNonNullElse(request, "{}"));
            } catch (RuntimeException error) {
                Throwable cause = error.getCause() == null ? error : error.getCause();
                throw new PlatformServiceException(Objects.requireNonNullElse(
                        cause.getMessage(), "Android platform operation failed"), cause);
            }
        }, executor);
    }

    private static String keyRequest(String key) {
        return Json.object(Map.of("key", Objects.requireNonNullElse(key, "")));
    }

    private static AppRoutingSettings decodeRoutingSettings(String payload) {
        Json.Node root = Json.parse(payload);
        Set<String> packages = new LinkedHashSet<>();
        root.get("packages").elements().forEach(item -> {
            String value = item.text();
            if (!value.isBlank()) packages.add(value);
        });
        return new AppRoutingSettings(root.get("mode").text("all"), packages);
    }
}
