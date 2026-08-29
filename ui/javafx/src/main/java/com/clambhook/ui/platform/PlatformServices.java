// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.platform;

import java.nio.file.Path;
import java.util.List;
import java.util.Objects;
import java.util.Set;
import java.util.concurrent.CompletableFuture;

/**
 * Platform-only capabilities kept outside the shared JavaFX view and frozen
 * runtime client contracts.
 */
public interface PlatformServices extends AutoCloseable {
    enum Capability {
        VPN_CONSENT,
        FILES,
        QR_SCAN,
        QR_SHARE,
        SECURE_STORAGE,
        CLIPBOARD,
        BROWSER,
        NOTIFICATIONS,
        LICENSING,
        UPDATES,
        PER_APP_ROUTING,
        DAEMON_SUPERVISION
    }

    Set<Capability> capabilities();

    CompletableFuture<Boolean> requestVpnConsent();

    CompletableFuture<Void> startVpn();

    CompletableFuture<Void> stopVpn();

    CompletableFuture<String> readTextFile(Path path, int maximumBytes);

    CompletableFuture<Void> writeTextFile(Path path, String value);

    CompletableFuture<String> scanQrCode();

    CompletableFuture<Void> shareQrCode(String value);

    CompletableFuture<String> secureRead(String key);

    CompletableFuture<Void> secureWrite(String key, String value);

    CompletableFuture<Void> secureDelete(String key);

    CompletableFuture<String> clipboardRead();

    CompletableFuture<Void> clipboardWrite(String value);

    CompletableFuture<Void> openBrowser(String uri);

    CompletableFuture<Void> notify(String title, String body);

    CompletableFuture<List<InstalledApplication>> installedApplications();

    CompletableFuture<AppRoutingSettings> appRoutingSettings();

    CompletableFuture<AppRoutingSettings> updateAppRoutingSettings(
            String mode, Set<String> packageNames);

    CompletableFuture<Result> licensing(String operation, String requestJson);

    CompletableFuture<Result> updates(String operation, String requestJson);

    String platformName();

    default boolean supports(Capability capability) {
        return capabilities().contains(Objects.requireNonNull(capability, "capability"));
    }

    @Override
    void close();

    record Result(boolean successful, String payload, String message) {
        public Result {
            payload = Objects.requireNonNullElse(payload, "");
            message = Objects.requireNonNullElse(message, "");
        }
    }

    record InstalledApplication(String packageName, String label, boolean system) {
        public InstalledApplication {
            packageName = Objects.requireNonNullElse(packageName, "");
            label = Objects.requireNonNullElse(label, packageName);
        }
    }

    record AppRoutingSettings(String mode, Set<String> packageNames) {
        public AppRoutingSettings {
            mode = Objects.requireNonNullElse(mode, "all");
            packageNames = Set.copyOf(Objects.requireNonNullElse(packageNames, Set.of()));
        }
    }
}
