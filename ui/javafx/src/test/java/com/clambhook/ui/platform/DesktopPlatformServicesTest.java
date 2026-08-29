// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.platform;

import com.clambhook.ui.json.Json;
import com.clambhook.ui.runtime.RuntimeClient;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.file.Files;
import java.nio.file.Path;
import java.lang.reflect.Proxy;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionException;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class DesktopPlatformServicesTest {
    private final String previousPackageManager =
            System.getProperty("clambhook.packageManager");
    private final String previousLicenseHelper =
            System.getProperty("clambhook.licenseHelper");

    @TempDir
    Path temporaryDirectory;

    @AfterEach
    void restorePackageManager() {
        if (previousPackageManager == null) {
            System.clearProperty("clambhook.packageManager");
        } else {
            System.setProperty("clambhook.packageManager", previousPackageManager);
        }
        if (previousLicenseHelper == null) {
            System.clearProperty("clambhook.licenseHelper");
        } else {
            System.setProperty("clambhook.licenseHelper", previousLicenseHelper);
        }
    }

    @Test
    void startsPackagedDaemonBeforeConnectingWhenLoopbackApiIsDown() {
        AtomicInteger statusCalls = new AtomicInteger();
        AtomicInteger connectCalls = new AtomicInteger();
        RuntimeClient runtime = runtime(
                "http://127.0.0.1:9090",
                statusCalls,
                connectCalls,
                true);
        List<List<String>> commands = new ArrayList<>();

        try (DesktopPlatformServices services = new DesktopPlatformServices(
                runtime, (command, input, required) -> {
                    commands.add(List.of(command));
                    return new DesktopPlatformServices.ProcessResult(0, "");
                })) {
            services.startVpn().join();

            assertTrue(services.supports(
                    PlatformServices.Capability.DAEMON_SUPERVISION));
            assertEquals(List.of(List.of(
                    "systemctl", "start", "clambhook-daemon.service")), commands);
            assertEquals(2, statusCalls.get());
            assertEquals(1, connectCalls.get());
        }
    }

    @Test
    void doesNotStartSystemServiceForHealthyOrRemoteDaemons() {
        List<List<String>> commands = new ArrayList<>();
        AtomicInteger healthyConnects = new AtomicInteger();
        try (DesktopPlatformServices services = new DesktopPlatformServices(
                runtime("http://localhost:9090", new AtomicInteger(),
                        healthyConnects, false),
                (command, input, required) -> {
                    commands.add(List.of(command));
                    return new DesktopPlatformServices.ProcessResult(0, "");
                })) {
            services.startVpn().join();
        }
        assertTrue(commands.isEmpty());
        assertEquals(1, healthyConnects.get());

        AtomicInteger remoteConnects = new AtomicInteger();
        try (DesktopPlatformServices services = new DesktopPlatformServices(
                runtime("https://controller.example:9443", new AtomicInteger(),
                        remoteConnects, true),
                (command, input, required) -> {
                    commands.add(List.of(command));
                    return new DesktopPlatformServices.ProcessResult(0, "");
                })) {
            CompletionException failure = org.junit.jupiter.api.Assertions.assertThrows(
                    CompletionException.class, () -> services.startVpn().join());
            assertInstanceOf(PlatformServiceException.class, failure.getCause());
        }
        assertTrue(commands.isEmpty());
        assertEquals(0, remoteConnects.get());
    }

    @Test
    void checksAndInstallsOnlyThroughSignedDistributionRepositories() {
        System.setProperty("clambhook.packageManager", "apt");
        List<List<String>> commands = new ArrayList<>();
        try (DesktopPlatformServices services = new DesktopPlatformServices(
                runtime("http://127.0.0.1:9090", new AtomicInteger(),
                        new AtomicInteger(), false),
                (command, input, required) -> {
                    commands.add(List.of(command));
                    if (command[0].equals("apt-cache")) {
                        return new DesktopPlatformServices.ProcessResult(0,
                                "Installed: 1.0.1\nCandidate: 1.0.2\n");
                    }
                    return new DesktopPlatformServices.ProcessResult(0, "updated\n");
                })) {
            PlatformServices.Result check = services.updates("check", "{}").join();
            PlatformServices.Result install = services.updates("install", "{}").join();

            assertTrue(check.successful());
            assertTrue(check.payload().contains("\"update_available\":true"));
            assertEquals("a signed package update is available", check.message());
            assertTrue(install.successful());
        }

        assertEquals(List.of(
                List.of("apt-cache", "policy", "clambhook"),
                List.of("pkexec", "apt-get", "--only-upgrade", "install", "-y",
                        "clambhook")), commands);
    }

    @Test
    void rejectsUnknownUpdateOperationsWithoutLaunchingACommand() {
        System.setProperty("clambhook.packageManager", "dnf");
        AtomicInteger commands = new AtomicInteger();
        try (DesktopPlatformServices services = new DesktopPlatformServices(
                runtime("http://127.0.0.1:9090", new AtomicInteger(),
                        new AtomicInteger(), false),
                (command, input, required) -> {
                    commands.incrementAndGet();
                    return new DesktopPlatformServices.ProcessResult(0, "");
                })) {
            PlatformServices.Result result = services.updates("remove", "{}").join();
            assertFalse(result.successful());
            assertEquals("unsupported update operation", result.message());
        }
        assertEquals(0, commands.get());
    }

    @Test
    void bootstrapsTrialAndPersistsFrozenLicenseSnapshot() throws Exception {
        System.setProperty("clambhook.licenseHelper", "test-license-helper");
        Path statePath = temporaryDirectory.resolve("linux-license.json");
        List<Json.Node> requests = new ArrayList<>();
        DesktopPlatformServices.CommandRunner runner = (command, input, required) -> {
            if (command[0].equals("secret-tool")) {
                return new DesktopPlatformServices.ProcessResult(1, "not found");
            }
            assertEquals("test-license-helper", command[0]);
            assertTrue(required);
            Json.Node request = Json.parse(input);
            requests.add(request);
            String result = switch (request.get("command").text()) {
                case "install-id" -> "install-1";
                case "ensure-trial" ->
                        "{\"trialStartDate\":\"2026-08-29T00:00:00Z\",\"transactions\":null}";
                case "status" ->
                        "{\"decision\":{\"reason\":\"trial\",\"trialDaysRemaining\":30}}";
                default -> throw new AssertionError(input);
            };
            return helperResult(result);
        };

        try (DesktopPlatformServices services = new DesktopPlatformServices(
                runtime("http://127.0.0.1:9090", new AtomicInteger(),
                        new AtomicInteger(), false), runner, statePath)) {
            PlatformServices.Result result = services.licensing("status", "{}").join();
            assertTrue(result.successful());
            assertTrue(result.payload().contains("\"reason\":\"trial\""));
            assertFalse(result.payload().contains("test-license-helper"));
        }

        assertEquals(List.of("install-id", "ensure-trial", "status"),
                requests.stream().map(request -> request.get("command").text()).toList());
        assertEquals("install-1", Json.parse(Files.readString(statePath))
                .get("installId").text());
        assertTrue(Files.readString(statePath.resolveSibling("license-snapshot.json"))
                .contains("trialStartDate"));
    }

    @Test
    void activatesAndManagesDeviceWithHelperSchemaAndSecretTool() throws Exception {
        System.setProperty("clambhook.licenseHelper", "test-license-helper");
        Path statePath = temporaryDirectory.resolve("linux-license.json");
        List<Json.Node> requests = new ArrayList<>();
        AtomicReference<String> secret = new AtomicReference<>("");
        DesktopPlatformServices.CommandRunner runner = (command, input, required) -> {
            if (command[0].equals("secret-tool")) {
                if (command[1].equals("lookup")) {
                    return new DesktopPlatformServices.ProcessResult(
                            secret.get().isBlank() ? 1 : 0, secret.get());
                }
                assertEquals("store", command[1]);
                assertTrue(required);
                secret.set(input);
                return new DesktopPlatformServices.ProcessResult(0, "");
            }
            Json.Node request = Json.parse(input);
            requests.add(request);
            String result = switch (request.get("command").text()) {
                case "install-id" -> "install-2";
                case "ensure-trial" ->
                        "{\"trialStartDate\":\"2026-08-29T00:00:00Z\"}";
                case "activate", "device-action" -> Json.object(Map.of(
                        "snapshot", Json.parse(
                                "{\"reason\":\"lifetime\",\"transactions\":[]}"),
                        "grant", Json.parse("{\"version\":1}"),
                        "deviceState", Json.parse(
                                "{\"current_device_id\":\"device-2\",\"devices\":[]}")));
                case "status" ->
                        "{\"decision\":{\"reason\":\"lifetime\"}}";
                default -> throw new AssertionError(input);
            };
            return helperResult(result);
        };

        try (DesktopPlatformServices services = new DesktopPlatformServices(
                runtime("http://127.0.0.1:9090", new AtomicInteger(),
                        new AtomicInteger(), false), runner, statePath)) {
            PlatformServices.Result activation = services.licensing(
                    "activate", "{\"email\":\"owner@example.com\",\"license_key\":\"KEY-123\"}")
                    .join();
            assertTrue(activation.successful());
            PlatformServices.Result deactivation = services.licensing(
                    "deactivate", "{}").join();
            assertTrue(deactivation.successful());
        }

        Json.Node activation = requests.stream()
                .filter(request -> request.get("command").text().equals("activate"))
                .findFirst().orElseThrow();
        assertEquals("KEY-123", activation.get("licenseKey").text());
        assertEquals("owner@example.com", activation.get("email").text());
        assertEquals("install-2", Json.parse(
                activation.get("deviceRegistration").text()).get("install_id").text());
        Json.Node deviceAction = requests.stream()
                .filter(request -> request.get("command").text().equals("device-action"))
                .findFirst().orElseThrow();
        assertEquals("deactivate", deviceAction.get("action").text());
        assertEquals("device-2", deviceAction.get("deviceID").text());
        assertEquals("KEY-123", secret.get());
        Json.Node persisted = Json.parse(Files.readString(statePath));
        assertEquals("owner@example.com", persisted.get("email").text());
        assertTrue(persisted.get("snapshotJson").text().contains("lifetime"));
    }

    private static DesktopPlatformServices.ProcessResult helperResult(String result) {
        return new DesktopPlatformServices.ProcessResult(0,
                Json.object(Map.of("ok", true, "result", result)));
    }

    private static RuntimeClient runtime(
            String endpoint,
            AtomicInteger statusCalls,
            AtomicInteger connectCalls,
            boolean failFirstStatus) {
        return (RuntimeClient) Proxy.newProxyInstance(
                RuntimeClient.class.getClassLoader(),
                new Class<?>[]{RuntimeClient.class},
                (proxy, method, arguments) -> switch (method.getName()) {
                    case "status" -> {
                        int call = statusCalls.incrementAndGet();
                        if (failFirstStatus && call == 1) {
                            yield CompletableFuture.failedFuture(
                                    new IllegalStateException("connection refused"));
                        }
                        yield CompletableFuture.completedFuture(RuntimeClient.Status.parse(
                                "{\"running\":false,\"listeners\":[]}"));
                    }
                    case "connect" -> {
                        connectCalls.incrementAndGet();
                        yield CompletableFuture.completedFuture(
                                RuntimeClient.Document.parse("{}"));
                    }
                    case "disconnect" -> CompletableFuture.completedFuture(
                            RuntimeClient.Document.parse("{}"));
                    case "endpointSettings" -> Optional.of(
                            new RuntimeClient.EndpointSettings(endpoint, ""));
                    case "displayName" -> "test runtime";
                    case "supportsConnectionControl" -> true;
                    case "close" -> null;
                    case "toString" -> "test runtime proxy";
                    default -> throw new UnsupportedOperationException(method.getName());
                });
    }
}
