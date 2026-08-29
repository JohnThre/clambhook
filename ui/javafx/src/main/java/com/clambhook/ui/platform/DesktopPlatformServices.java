// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.platform;

import com.clambhook.ui.json.Json;
import com.clambhook.ui.runtime.RuntimeClient;
import javafx.application.Platform;
import javafx.scene.input.Clipboard;
import javafx.scene.input.ClipboardContent;

import java.io.IOException;
import java.net.URI;
import java.nio.file.AtomicMoveNotSupportedException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.StandardOpenOption;
import java.nio.file.attribute.PosixFilePermissions;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Objects;
import java.util.Set;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CompletionException;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.FutureTask;
import java.util.concurrent.TimeUnit;
import java.util.function.Supplier;

/** GNU/Linux platform services for a self-contained Gluon native image. */
public final class DesktopPlatformServices implements PlatformServices {
    private static final int PROCESS_OUTPUT_LIMIT = 8 * 1024 * 1024;

    private final RuntimeClient runtime;
    private final ExecutorService executor;
    private final CommandRunner commandRunner;
    private final Path licenseStatePath;
    private final Object licenseLock = new Object();

    public DesktopPlatformServices(RuntimeClient runtime) {
        this(runtime, DesktopPlatformServices::runProcess, defaultLicenseStatePath());
    }

    DesktopPlatformServices(RuntimeClient runtime, CommandRunner commandRunner) {
        this(runtime, commandRunner, defaultLicenseStatePath());
    }

    DesktopPlatformServices(RuntimeClient runtime, CommandRunner commandRunner,
                            Path licenseStatePath) {
        this.runtime = Objects.requireNonNull(runtime, "runtime");
        this.commandRunner = Objects.requireNonNull(commandRunner, "commandRunner");
        this.licenseStatePath = Objects.requireNonNull(
                licenseStatePath, "licenseStatePath").toAbsolutePath().normalize();
        executor = Executors.newFixedThreadPool(2, runnable -> {
            Thread thread = new Thread(runnable, "clambhook-platform");
            thread.setDaemon(true);
            return thread;
        });
    }

    @Override
    public Set<Capability> capabilities() {
        return Set.of(Capability.FILES, Capability.SECURE_STORAGE,
                Capability.CLIPBOARD, Capability.BROWSER,
                Capability.NOTIFICATIONS, Capability.LICENSING,
                Capability.UPDATES, Capability.DAEMON_SUPERVISION);
    }

    @Override
    public CompletableFuture<Boolean> requestVpnConsent() {
        return CompletableFuture.completedFuture(true);
    }

    @Override
    public CompletableFuture<Void> startVpn() {
        return ensureDaemonAvailable()
                .thenCompose(ignored -> runtime.connect())
                .thenApply(ignored -> null);
    }

    @Override
    public CompletableFuture<Void> stopVpn() {
        return runtime.disconnect().thenApply(ignored -> null);
    }

    @Override
    public CompletableFuture<String> readTextFile(Path path, int maximumBytes) {
        return async(() -> {
            Path safePath = Objects.requireNonNull(path, "path").toAbsolutePath().normalize();
            int limit = Math.max(1, Math.min(maximumBytes, PROCESS_OUTPUT_LIMIT));
            try (var stream = Files.newInputStream(safePath)) {
                byte[] value = stream.readNBytes(limit + 1);
                if (value.length > limit) {
                    throw new IllegalArgumentException("file exceeds " + limit + " byte limit");
                }
                return new String(value, StandardCharsets.UTF_8);
            } catch (IOException error) {
                throw new PlatformServiceException("read " + safePath + ": " + error.getMessage(), error);
            }
        });
    }

    @Override
    public CompletableFuture<Void> writeTextFile(Path path, String value) {
        return async(() -> {
            Path safePath = Objects.requireNonNull(path, "path").toAbsolutePath().normalize();
            try {
                Path parent = safePath.getParent();
                if (parent != null) Files.createDirectories(parent);
                Files.writeString(safePath, Objects.requireNonNullElse(value, ""),
                        StandardCharsets.UTF_8, StandardOpenOption.CREATE,
                        StandardOpenOption.TRUNCATE_EXISTING,
                        StandardOpenOption.WRITE);
                return null;
            } catch (IOException error) {
                throw new PlatformServiceException("write " + safePath + ": " + error.getMessage(), error);
            }
        });
    }

    @Override
    public CompletableFuture<String> scanQrCode() {
        return unsupported("QR scanning requires an Android camera");
    }

    @Override
    public CompletableFuture<Void> shareQrCode(String value) {
        return unsupported("QR sharing requires an Android share sheet");
    }

    @Override
    public CompletableFuture<String> secureRead(String key) {
        return async(() -> runChecked(
                new String[]{"secret-tool", "lookup", "service", "clambhook", "account", safeKey(key)},
                "", false).stripTrailing());
    }

    @Override
    public CompletableFuture<Void> secureWrite(String key, String value) {
        return async(() -> {
            runChecked(new String[]{"secret-tool", "store", "--label=ClambHook " + safeKey(key),
                    "service", "clambhook", "account", safeKey(key)},
                    Objects.requireNonNullElse(value, ""), true);
            return null;
        });
    }

    @Override
    public CompletableFuture<Void> secureDelete(String key) {
        return async(() -> {
            runChecked(new String[]{"secret-tool", "clear", "service", "clambhook",
                    "account", safeKey(key)}, "", false);
            return null;
        });
    }

    @Override
    public CompletableFuture<String> clipboardRead() {
        return onFxThread(() -> Objects.requireNonNullElse(
                Clipboard.getSystemClipboard().getString(), ""));
    }

    @Override
    public CompletableFuture<Void> clipboardWrite(String value) {
        return onFxThread(() -> {
            ClipboardContent content = new ClipboardContent();
            content.putString(Objects.requireNonNullElse(value, ""));
            if (!Clipboard.getSystemClipboard().setContent(content)) {
                throw new PlatformServiceException("system clipboard rejected content");
            }
            return null;
        });
    }

    @Override
    public CompletableFuture<Void> openBrowser(String uri) {
        return async(() -> {
            URI target;
            try {
                target = URI.create(Objects.requireNonNullElse(uri, ""));
            } catch (IllegalArgumentException error) {
                throw new PlatformServiceException("invalid browser URI", error);
            }
            String scheme = Objects.requireNonNullElse(target.getScheme(), "");
            if (!(scheme.equalsIgnoreCase("https") || scheme.equalsIgnoreCase("http"))) {
                throw new PlatformServiceException("browser URI must use HTTP or HTTPS");
            }
            runChecked(new String[]{"xdg-open", target.toString()}, "", false);
            return null;
        });
    }

    @Override
    public CompletableFuture<Void> notify(String title, String body) {
        return async(() -> {
            runChecked(new String[]{"notify-send", "--app-name=ClambHook",
                    Objects.requireNonNullElse(title, "ClambHook"),
                    Objects.requireNonNullElse(body, "")}, "", false);
            return null;
        });
    }

    @Override
    public CompletableFuture<List<InstalledApplication>> installedApplications() {
        return unsupported("per-application routing is available only on Android");
    }

    @Override
    public CompletableFuture<AppRoutingSettings> appRoutingSettings() {
        return unsupported("per-application routing is available only on Android");
    }

    @Override
    public CompletableFuture<AppRoutingSettings> updateAppRoutingSettings(
            String mode, Set<String> packageNames) {
        return unsupported("per-application routing is available only on Android");
    }

    @Override
    public CompletableFuture<Result> licensing(String operation, String requestJson) {
        return async(() -> {
            synchronized (licenseLock) {
                return performLicenseOperation(operation, requestJson);
            }
        });
    }

    @Override
    public CompletableFuture<Result> updates(String operation, String requestJson) {
        return async(() -> performPackageUpdate(operation, requestJson));
    }

    @Override
    public String platformName() {
        return "GNU/Linux";
    }

    @Override
    public void close() {
        executor.shutdownNow();
    }

    private static String licenseHelper() {
        String configured = System.getProperty("clambhook.licenseHelper", "").trim();
        if (!configured.isBlank()) return configured;
        String environment = System.getenv("CLAMBHOOK_LICENSE_HELPER");
        return environment == null || environment.isBlank()
                ? "clambhook-license" : environment.trim();
    }

    private static String safeKey(String key) {
        String value = Objects.requireNonNullElse(key, "").trim();
        if (value.isBlank() || value.length() > 128 ||
                !value.chars().allMatch(character -> Character.isLetterOrDigit(character)
                        || character == '.' || character == '_' || character == '-')) {
            throw new PlatformServiceException("secure-storage key is invalid");
        }
        return value;
    }

    private Result performLicenseOperation(String operation, String requestJson) {
        String normalized = Objects.requireNonNullElse(operation, "")
                .trim().toLowerCase(Locale.ROOT);
        Json.Node request = Json.parse(Objects.requireNonNullElse(requestJson, "{}"));
        if (!request.isObject()) {
            throw new PlatformServiceException("license request must be an object");
        }
        DesktopLicenseState state = ensureLicenseState(loadLicenseState());
        return switch (normalized) {
            case "status" -> licenseStatus(state);
            case "activate" -> activateLicense(state, request);
            case "deactivate", "reactivate", "transfer" ->
                    performDeviceAction(state, normalized);
            default -> new Result(false,
                    Json.object(Map.of("operation", normalized)),
                    "unsupported license operation");
        };
    }

    private DesktopLicenseState ensureLicenseState(DesktopLicenseState state) {
        boolean changed = false;
        if (state.installId().isBlank()) {
            state = state.withInstallId(callLicenseHelper(Map.of(
                    "command", "install-id")));
            changed = true;
        }
        if (state.snapshotJson().isBlank()) {
            state = state.withSnapshotJson(callLicenseHelper(Map.of(
                    "command", "ensure-trial", "snapshot", "")));
            changed = true;
        }
        if (changed) saveLicenseState(state);
        return state;
    }

    private Result licenseStatus(DesktopLicenseState state) {
        String statusJson = callLicenseHelper(Map.of(
                "command", "status", "snapshot", state.snapshotJson()));
        Json.Node status = Json.parse(statusJson);
        Json.Node deviceState = state.deviceStateJson().isBlank()
                ? Json.parse("{}") : Json.parse(state.deviceStateJson());
        String key = readLicenseKey();
        String payload = Json.object(Map.of(
                "status", status,
                "device_state", deviceState,
                "has_license_key", !key.isBlank(),
                "email", state.email(),
                "initialized", true));
        return new Result(true, payload, "");
    }

    private Result activateLicense(DesktopLicenseState state, Json.Node request) {
        String licenseKey = request.get("license_key").text(
                request.get("licenseKey").text()).trim();
        String email = request.get("email").text().trim();
        if (licenseKey.isBlank()) {
            return new Result(false, "{}", "enter a license key");
        }
        try {
            String applied = callLicenseHelper(Map.of(
                    "command", "activate",
                    "baseURL", "",
                    "licenseKey", licenseKey,
                    "email", email,
                    "deviceRegistration", deviceRegistration(state.installId())));
            DesktopLicenseState updated = applyLicensePayload(state, applied)
                    .withEmail(email);
            writeLicenseKey(licenseKey);
            saveLicenseState(updated);
            return licenseStatus(updated);
        } catch (RuntimeException error) {
            markVerificationFailure(state);
            throw error;
        }
    }

    private Result performDeviceAction(DesktopLicenseState state, String action) {
        String licenseKey = readLicenseKey();
        if (licenseKey.isBlank()) {
            return new Result(false, "{}",
                    "activate a license key before managing devices");
        }
        Json.Node deviceState = state.deviceStateJson().isBlank()
                ? Json.parse("{}") : Json.parse(state.deviceStateJson());
        String applied = callLicenseHelper(Map.of(
                "command", "device-action",
                "baseURL", "",
                "action", action,
                "licenseKey", licenseKey,
                "installID", state.installId(),
                "deviceID", deviceState.get("current_device_id").text(),
                "deviceRegistration", deviceRegistration(state.installId())));
        DesktopLicenseState updated = applyLicensePayload(state, applied);
        saveLicenseState(updated);
        return licenseStatus(updated);
    }

    private void markVerificationFailure(DesktopLicenseState state) {
        try {
            String marked = callLicenseHelper(Map.of(
                    "command", "mark-verification-failure",
                    "snapshot", state.snapshotJson()));
            Json.Node result = Json.parse(marked);
            String snapshot = result.get("snapshot").exists()
                    ? result.get("snapshot").toString() : state.snapshotJson();
            saveLicenseState(state.withSnapshotJson(snapshot));
        } catch (RuntimeException ignored) {
            // Preserve the original activation error when offline marking fails.
        }
    }

    private DesktopLicenseState applyLicensePayload(
            DesktopLicenseState state, String appliedJson) {
        Json.Node applied = Json.parse(appliedJson);
        if (!applied.isObject()) {
            throw new PlatformServiceException("license helper returned an invalid payload");
        }
        String snapshot = applied.get("snapshot").exists()
                ? applied.get("snapshot").toString() : state.snapshotJson();
        String grant = applied.get("grant").exists()
                ? applied.get("grant").toString() : state.grantJson();
        String deviceState = applied.get("deviceState").exists()
                ? applied.get("deviceState").toString() : state.deviceStateJson();
        return new DesktopLicenseState(state.installId(), state.email(),
                snapshot, grant, deviceState);
    }

    private String callLicenseHelper(Map<String, ?> request) {
        String response = runChecked(new String[]{licenseHelper()},
                Json.object(request), true);
        Json.Node envelope = Json.parse(response);
        if (!envelope.isObject() || !envelope.get("ok").bool(false)) {
            String message = envelope.get("error").text("license helper failed");
            throw new PlatformServiceException(message);
        }
        String result = envelope.get("result").text();
        if (!envelope.get("result").exists()) {
            throw new PlatformServiceException("license helper returned no result");
        }
        return result;
    }

    private String readLicenseKey() {
        ProcessResult result = commandRunner.run(new String[]{
                "secret-tool", "lookup", "service", "clambhook",
                "account", "license-key"}, "", false);
        return result.exitCode() == 0 ? result.output().stripTrailing() : "";
    }

    private void writeLicenseKey(String value) {
        runChecked(new String[]{"secret-tool", "store",
                "--label=ClambHook license key", "service", "clambhook",
                "account", "license-key"}, value, true);
    }

    private DesktopLicenseState loadLicenseState() {
        if (!Files.exists(licenseStatePath)) return DesktopLicenseState.empty();
        try {
            Json.Node root = Json.parse(Files.readString(
                    licenseStatePath, StandardCharsets.UTF_8));
            if (!root.isObject()) {
                throw new PlatformServiceException("license state must be a JSON object");
            }
            return new DesktopLicenseState(
                    root.get("installId").text(),
                    root.get("email").text(),
                    root.get("snapshotJson").text(),
                    root.get("grantJson").text(),
                    root.get("deviceStateJson").text());
        } catch (IOException error) {
            throw new PlatformServiceException(
                    "read license state: " + error.getMessage(), error);
        }
    }

    private void saveLicenseState(DesktopLicenseState state) {
        String persisted = Json.object(Map.of(
                "installId", state.installId(),
                "email", state.email(),
                "snapshotJson", state.snapshotJson(),
                "grantJson", state.grantJson(),
                "deviceStateJson", state.deviceStateJson()));
        writePrivateFile(licenseStatePath, persisted);
        writePrivateFile(licenseStatePath.resolveSibling("license-snapshot.json"),
                state.snapshotJson().isBlank() ? "{}" : state.snapshotJson());
    }

    private static void writePrivateFile(Path destination, String value) {
        Path temporary = null;
        try {
            Path parent = destination.getParent();
            if (parent != null) Files.createDirectories(parent);
            temporary = Files.createTempFile(parent, ".clambhook-license-", ".tmp");
            Files.writeString(temporary, value, StandardCharsets.UTF_8,
                    StandardOpenOption.TRUNCATE_EXISTING);
            try {
                Files.setPosixFilePermissions(temporary,
                        PosixFilePermissions.fromString("rw-------"));
            } catch (UnsupportedOperationException ignored) {
                // POSIX permissions are available on supported GNU/Linux hosts.
            }
            try {
                Files.move(temporary, destination, StandardCopyOption.ATOMIC_MOVE,
                        StandardCopyOption.REPLACE_EXISTING);
            } catch (AtomicMoveNotSupportedException ignored) {
                Files.move(temporary, destination, StandardCopyOption.REPLACE_EXISTING);
            }
            temporary = null;
        } catch (IOException error) {
            throw new PlatformServiceException(
                    "write license state: " + error.getMessage(), error);
        } finally {
            if (temporary != null) {
                try {
                    Files.deleteIfExists(temporary);
                } catch (IOException ignored) {
                    // Best effort cleanup after the original failure.
                }
            }
        }
    }

    private static String deviceRegistration(String installId) {
        String hostname = Objects.requireNonNullElse(
                System.getenv("HOSTNAME"), "").trim();
        if (hostname.isBlank()) hostname = "GNU/Linux device";
        String version = Objects.requireNonNullElse(
                DesktopPlatformServices.class.getPackage().getImplementationVersion(), "dev");
        return Json.object(Map.of(
                "install_id", installId,
                "display_name", hostname,
                "platform", "linux",
                "architecture", System.getProperty("os.arch", "unknown"),
                "app_version", version));
    }

    private static Path defaultLicenseStatePath() {
        String configHome = Objects.requireNonNullElse(
                System.getenv("XDG_CONFIG_HOME"), "").trim();
        Path base = configHome.isBlank()
                ? Path.of(System.getProperty("user.home"), ".config")
                : Path.of(configHome);
        return base.resolve("clambhook").resolve("linux-license.json");
    }

    private CompletableFuture<Void> ensureDaemonAvailable() {
        CompletableFuture<Throwable> probe = runtime.status()
                .handle((ignored, error) -> error == null ? null : unwrap(error));
        return probe.thenCompose(error -> {
            if (error == null) {
                return CompletableFuture.completedFuture(null);
            }
            if (!usesLoopbackDaemon()) {
                return CompletableFuture.failedFuture(new PlatformServiceException(
                        "configured ClambHook daemon is unavailable: " + error.getMessage(), error));
            }
            return async(() -> {
                runChecked(new String[]{"systemctl", "start", "clambhook-daemon.service"},
                        "", false);
                return null;
            }).thenCompose(ignored -> waitForDaemon(8, error));
        });
    }

    private CompletableFuture<Void> waitForDaemon(int attempts, Throwable firstError) {
        return runtime.status().thenApply(ignored -> (Void) null)
                .exceptionallyCompose(error -> {
                    if (attempts <= 1) {
                        Throwable cause = unwrap(error);
                        return CompletableFuture.failedFuture(new PlatformServiceException(
                                "clambhook-daemon.service started but its API did not become ready: "
                                        + cause.getMessage(), firstError));
                    }
                    return CompletableFuture.runAsync(
                                    () -> { },
                                    CompletableFuture.delayedExecutor(
                                            250, TimeUnit.MILLISECONDS, executor))
                            .thenCompose(ignored -> waitForDaemon(attempts - 1, firstError));
                });
    }

    private boolean usesLoopbackDaemon() {
        return runtime.endpointSettings().map(settings -> {
            try {
                String host = URI.create(settings.baseUrl()).getHost();
                return host != null && (host.equalsIgnoreCase("localhost")
                        || host.equals("::1") || host.equals("[::1]")
                        || host.matches("127(?:\\.[0-9]{1,3}){3}"));
            } catch (IllegalArgumentException error) {
                return false;
            }
        }).orElse(false);
    }

    private Result performPackageUpdate(String operation, String requestJson) {
        String normalized = Objects.requireNonNullElse(operation, "")
                .trim().toLowerCase(Locale.ROOT);
        String request = Objects.requireNonNullElse(requestJson, "{}");
        String provider = packageProvider();
        if (provider.isBlank()) {
            return new Result(false, Json.object(Map.of(
                    "operation", normalized, "request", request)),
                    "no supported signed package repository client was found");
        }

        if (normalized.equals("check")) {
            ProcessResult result = provider.equals("apt")
                    ? commandRunner.run(
                            new String[]{"apt-cache", "policy", "clambhook"}, "", false)
                    : commandRunner.run(
                            new String[]{"dnf", "--quiet", "check-upgrade", "clambhook"},
                            "", false);
            boolean accepted = result.exitCode() == 0
                    || (provider.equals("dnf") && result.exitCode() == 100);
            if (!accepted) {
                throw processFailure(provider, result);
            }
            boolean available = provider.equals("dnf")
                    ? result.exitCode() == 100 : aptUpdateAvailable(result.output());
            return new Result(true, Json.object(Map.of(
                    "provider", provider,
                    "operation", "check",
                    "update_available", available,
                    "output", result.output().strip())),
                    available ? "a signed package update is available"
                            : "the installed package is current");
        }
        if (normalized.equals("install")) {
            String[] command = provider.equals("apt")
                    ? new String[]{"pkexec", "apt-get", "--only-upgrade", "install", "-y", "clambhook"}
                    : new String[]{"pkexec", "dnf", "upgrade", "-y", "clambhook"};
            String output = runChecked(command, "", false);
            return new Result(true, Json.object(Map.of(
                    "provider", provider,
                    "operation", "install",
                    "output", output.strip())),
                    "the signed package update completed");
        }
        return new Result(false, Json.object(Map.of(
                "operation", normalized, "request", request)),
                "unsupported update operation");
    }

    private static boolean aptUpdateAvailable(String output) {
        String installed = "";
        String candidate = "";
        for (String line : Objects.requireNonNullElse(output, "").lines().toList()) {
            String value = line.trim();
            if (value.startsWith("Installed:")) installed = value.substring(10).trim();
            if (value.startsWith("Candidate:")) candidate = value.substring(10).trim();
        }
        return !candidate.isBlank() && !candidate.equals("(none)")
                && !candidate.equals(installed);
    }

    private static String packageProvider() {
        String configured = System.getProperty("clambhook.packageManager", "").trim();
        if (configured.equals("apt") || configured.equals("dnf")) return configured;
        if (Files.isExecutable(Path.of("/usr/bin/apt-cache"))) return "apt";
        if (Files.isExecutable(Path.of("/usr/bin/dnf"))) return "dnf";
        return "";
    }

    private String runChecked(String[] command, String input, boolean inputRequired) {
        ProcessResult result = commandRunner.run(command, input, inputRequired);
        if (result.exitCode() != 0) throw processFailure(command[0], result);
        return result.output();
    }

    private static PlatformServiceException processFailure(
            String command, ProcessResult result) {
        return new PlatformServiceException(command + " failed with exit code "
                + result.exitCode() + ": " + result.output().strip());
    }

    private static Throwable unwrap(Throwable throwable) {
        Throwable current = throwable;
        while (current instanceof CompletionException && current.getCause() != null) {
            current = current.getCause();
        }
        return current;
    }

    private static ProcessResult runProcess(String[] command, String input,
                                            boolean inputRequired) {
        Process process;
        try {
            process = new ProcessBuilder(command).redirectErrorStream(true).start();
            if (inputRequired || !input.isEmpty()) {
                try (var output = process.getOutputStream()) {
                    output.write(input.getBytes(StandardCharsets.UTF_8));
                    output.write('\n');
                }
            } else {
                process.getOutputStream().close();
            }
            FutureTask<byte[]> outputReader = new FutureTask<>(() -> {
                try (var stream = process.getInputStream()) {
                    return stream.readNBytes(PROCESS_OUTPUT_LIMIT + 1);
                }
            });
            Thread outputThread = new Thread(outputReader,
                    "clambhook-command-output");
            outputThread.setDaemon(true);
            outputThread.start();
            if (!process.waitFor(20, TimeUnit.SECONDS)) {
                process.destroyForcibly();
                outputThread.interrupt();
                throw new PlatformServiceException(command[0] + " timed out");
            }
            byte[] bytes;
            try {
                bytes = outputReader.get(5, TimeUnit.SECONDS);
            } catch (java.util.concurrent.TimeoutException error) {
                process.destroyForcibly();
                throw new PlatformServiceException(
                        "timed out reading " + command[0] + " output", error);
            }
            if (bytes.length > PROCESS_OUTPUT_LIMIT) {
                throw new PlatformServiceException(command[0] + " output exceeds safety limit");
            }
            String output = new String(bytes, StandardCharsets.UTF_8);
            return new ProcessResult(process.exitValue(), output);
        } catch (IOException error) {
            throw new PlatformServiceException("cannot run " + command[0] + ": " + error.getMessage(), error);
        } catch (InterruptedException error) {
            Thread.currentThread().interrupt();
            throw new PlatformServiceException(command[0] + " interrupted", error);
        } catch (ExecutionException error) {
            throw new PlatformServiceException(
                    "cannot read " + command[0] + " output: " + error.getCause(),
                    error.getCause());
        }
    }

    private <T> CompletableFuture<T> async(Supplier<T> operation) {
        return CompletableFuture.supplyAsync(operation, executor);
    }

    private static <T> CompletableFuture<T> onFxThread(Supplier<T> operation) {
        CompletableFuture<T> result = new CompletableFuture<>();
        Runnable run = () -> {
            try {
                result.complete(operation.get());
            } catch (Throwable error) {
                result.completeExceptionally(error);
            }
        };
        if (Platform.isFxApplicationThread()) run.run(); else Platform.runLater(run);
        return result;
    }

    private static <T> CompletableFuture<T> unsupported(String message) {
        return CompletableFuture.failedFuture(new UnsupportedOperationException(message));
    }

    private record DesktopLicenseState(
            String installId,
            String email,
            String snapshotJson,
            String grantJson,
            String deviceStateJson) {
        private DesktopLicenseState {
            installId = Objects.requireNonNullElse(installId, "");
            email = Objects.requireNonNullElse(email, "");
            snapshotJson = Objects.requireNonNullElse(snapshotJson, "");
            grantJson = Objects.requireNonNullElse(grantJson, "");
            deviceStateJson = Objects.requireNonNullElse(deviceStateJson, "");
        }

        private static DesktopLicenseState empty() {
            return new DesktopLicenseState("", "", "", "", "");
        }

        private DesktopLicenseState withInstallId(String value) {
            return new DesktopLicenseState(value, email, snapshotJson,
                    grantJson, deviceStateJson);
        }

        private DesktopLicenseState withEmail(String value) {
            return new DesktopLicenseState(installId, value, snapshotJson,
                    grantJson, deviceStateJson);
        }

        private DesktopLicenseState withSnapshotJson(String value) {
            return new DesktopLicenseState(installId, email, value,
                    grantJson, deviceStateJson);
        }
    }

    @FunctionalInterface
    interface CommandRunner {
        ProcessResult run(String[] command, String input, boolean inputRequired);
    }

    record ProcessResult(int exitCode, String output) {
        ProcessResult {
            output = Objects.requireNonNullElse(output, "");
        }
    }
}
