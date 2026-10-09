// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.platform

import com.clambhook.ui.json.Json
import com.clambhook.ui.json.JsonNode
import com.clambhook.ui.platform.PlatformServices.AppRoutingSettings
import com.clambhook.ui.platform.PlatformServices.Capability
import com.clambhook.ui.platform.PlatformServices.InstalledApplication
import com.clambhook.ui.platform.PlatformServices.Result
import com.clambhook.ui.runtime.RuntimeClient
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.awt.GraphicsEnvironment
import java.awt.Toolkit
import java.awt.datatransfer.DataFlavor
import java.awt.datatransfer.StringSelection
import java.io.IOException
import java.net.URI
import java.nio.file.AtomicMoveNotSupportedException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.StandardCopyOption
import java.nio.file.StandardOpenOption
import java.nio.file.attribute.PosixFilePermissions
import java.util.concurrent.TimeUnit

/**
 * GNU/Linux platform services for Ubuntu and Fedora: systemd daemon
 * supervision, Secret Service storage through `secret-tool`, the C license
 * helper, and updates from the signed apt (Ubuntu) or dnf (Fedora) repository.
 */
class DesktopPlatformServices internal constructor(
    private val runtime: RuntimeClient,
    private val commandRunner: CommandRunner,
    licenseStatePath: Path,
    private val osReleasePath: Path = Path.of("/etc/os-release"),
) : PlatformServices {
    constructor(runtime: RuntimeClient) : this(
        runtime,
        CommandRunner { command, input, required -> runProcess(command, input, required) },
        defaultLicenseStatePath(),
    )

    private val licenseStatePath = licenseStatePath.toAbsolutePath().normalize()
    private val licenseLock = Mutex()

    override val capabilities: Set<Capability> = setOf(
        Capability.FILES, Capability.SECURE_STORAGE, Capability.CLIPBOARD, Capability.BROWSER,
        Capability.NOTIFICATIONS, Capability.LICENSING, Capability.UPDATES, Capability.DAEMON_SUPERVISION,
    )

    override suspend fun requestVpnConsent(): Boolean = true

    override suspend fun startVpn() {
        ensureDaemonAvailable()
        runtime.connect()
    }

    override suspend fun stopVpn() {
        runtime.disconnect()
    }

    override suspend fun readTextFile(path: String, maximumBytes: Int): String = io {
        val safePath = Path.of(path).toAbsolutePath().normalize()
        val limit = maximumBytes.coerceIn(1, PROCESS_OUTPUT_LIMIT)
        try {
            Files.newInputStream(safePath).use { stream ->
                val value = stream.readNBytes(limit + 1)
                require(value.size <= limit) { "file exceeds $limit byte limit" }
                value.decodeToString()
            }
        } catch (error: IOException) {
            throw PlatformServiceException("read $safePath: ${error.message}", error)
        }
    }

    override suspend fun writeTextFile(path: String, value: String) = io {
        val safePath = Path.of(path).toAbsolutePath().normalize()
        try {
            safePath.parent?.let { Files.createDirectories(it) }
            Files.writeString(
                safePath, value, StandardOpenOption.CREATE,
                StandardOpenOption.TRUNCATE_EXISTING, StandardOpenOption.WRITE,
            )
            Unit
        } catch (error: IOException) {
            throw PlatformServiceException("write $safePath: ${error.message}", error)
        }
    }

    override suspend fun scanQrCode(): String =
        throw UnsupportedOperationException("QR scanning requires an Android camera")

    override suspend fun shareQrCode(value: String) =
        throw UnsupportedOperationException("QR sharing requires an Android share sheet")

    override suspend fun secureRead(key: String): String = io {
        runChecked(listOf("secret-tool", "lookup", "service", "clambhook", "account", safeKey(key)), "", false)
            .trimEnd()
    }

    override suspend fun secureWrite(key: String, value: String) = io {
        val account = safeKey(key)
        runChecked(
            listOf("secret-tool", "store", "--label=ClambHook $account", "service", "clambhook", "account", account),
            value, true,
        )
        Unit
    }

    override suspend fun secureDelete(key: String) = io {
        runChecked(listOf("secret-tool", "clear", "service", "clambhook", "account", safeKey(key)), "", false)
        Unit
    }

    override suspend fun clipboardRead(): String = io {
        check(!GraphicsEnvironment.isHeadless()) { "no graphical session clipboard" }
        val clipboard = Toolkit.getDefaultToolkit().systemClipboard
        if (clipboard.isDataFlavorAvailable(DataFlavor.stringFlavor)) {
            clipboard.getData(DataFlavor.stringFlavor) as? String ?: ""
        } else {
            ""
        }
    }

    override suspend fun clipboardWrite(value: String) = io {
        check(!GraphicsEnvironment.isHeadless()) { "no graphical session clipboard" }
        val selection = StringSelection(value)
        Toolkit.getDefaultToolkit().systemClipboard.setContents(selection, selection)
    }

    override suspend fun openBrowser(uri: String) = io {
        val target = try {
            URI(uri)
        } catch (error: Exception) {
            throw PlatformServiceException("invalid browser URI", error)
        }
        val scheme = target.scheme.orEmpty()
        if (!scheme.equals("https", true) && !scheme.equals("http", true)) {
            throw PlatformServiceException("browser URI must use HTTP or HTTPS")
        }
        runChecked(listOf("xdg-open", target.toString()), "", false)
        Unit
    }

    override suspend fun notify(title: String, body: String) = io {
        runChecked(listOf("notify-send", "--app-name=ClambHook", title.ifBlank { "ClambHook" }, body), "", false)
        Unit
    }

    override suspend fun installedApplications(): List<InstalledApplication> =
        throw UnsupportedOperationException("per-application routing is available only on Android")

    override suspend fun appRoutingSettings(): AppRoutingSettings =
        throw UnsupportedOperationException("per-application routing is available only on Android")

    override suspend fun updateAppRoutingSettings(mode: String, packageNames: Set<String>): AppRoutingSettings =
        throw UnsupportedOperationException("per-application routing is available only on Android")

    override suspend fun licensing(operation: String, requestJson: String): Result =
        licenseLock.withLock { io { performLicenseOperation(operation, requestJson) } }

    override suspend fun updates(operation: String, requestJson: String): Result =
        io { performPackageUpdate(operation, requestJson) }

    override val platformName: String = "GNU/Linux"

    override fun close() = Unit

    // --- licensing -----------------------------------------------------------

    private fun performLicenseOperation(operation: String, requestJson: String): Result {
        val normalized = operation.trim().lowercase()
        val request = Json.parse(requestJson.ifBlank { "{}" })
        if (!request.isObject) throw PlatformServiceException("license request must be an object")
        val state = ensureLicenseState(loadLicenseState())
        return when (normalized) {
            "status" -> licenseStatus(state)
            "activate" -> activateLicense(state, request)
            "deactivate", "reactivate", "transfer" -> performDeviceAction(state, normalized)
            else -> Result(false, Json.obj("operation" to normalized), "unsupported license operation")
        }
    }

    private fun ensureLicenseState(initial: LicenseState): LicenseState {
        var state = initial
        var changed = false
        if (state.installId.isBlank()) {
            state = state.copy(installId = callLicenseHelper("command" to "install-id"))
            changed = true
        }
        if (state.snapshotJson.isBlank()) {
            state = state.copy(snapshotJson = callLicenseHelper("command" to "ensure-trial", "snapshot" to ""))
            changed = true
        }
        if (changed) saveLicenseState(state)
        return state
    }

    private fun licenseStatus(state: LicenseState): Result {
        val status = Json.parse(callLicenseHelper("command" to "status", "snapshot" to state.snapshotJson))
        val deviceState = Json.parse(state.deviceStateJson.ifBlank { "{}" })
        val payload = Json.obj(
            "status" to status,
            "device_state" to deviceState,
            "has_license_key" to readLicenseKey().isNotBlank(),
            "email" to state.email,
            "initialized" to true,
        )
        return Result(true, payload, "")
    }

    private fun activateLicense(state: LicenseState, request: JsonNode): Result {
        val licenseKey = request["license_key"].text(request["licenseKey"].text()).trim()
        val email = request["email"].text().trim()
        if (licenseKey.isBlank()) return Result(false, "{}", "enter a license key")
        try {
            val applied = callLicenseHelper(
                "command" to "activate",
                "baseURL" to "",
                "licenseKey" to licenseKey,
                "email" to email,
                "deviceRegistration" to deviceRegistration(state.installId),
            )
            val updated = applyLicensePayload(state, applied).copy(email = email)
            writeLicenseKey(licenseKey)
            saveLicenseState(updated)
            return licenseStatus(updated)
        } catch (error: RuntimeException) {
            markVerificationFailure(state)
            throw error
        }
    }

    private fun performDeviceAction(state: LicenseState, action: String): Result {
        val licenseKey = readLicenseKey()
        if (licenseKey.isBlank()) return Result(false, "{}", "activate a license key before managing devices")
        val deviceState = Json.parse(state.deviceStateJson.ifBlank { "{}" })
        val applied = callLicenseHelper(
            "command" to "device-action",
            "baseURL" to "",
            "action" to action,
            "licenseKey" to licenseKey,
            "installID" to state.installId,
            "deviceID" to deviceState["current_device_id"].text(),
            "deviceRegistration" to deviceRegistration(state.installId),
        )
        val updated = applyLicensePayload(state, applied)
        saveLicenseState(updated)
        return licenseStatus(updated)
    }

    private fun markVerificationFailure(state: LicenseState) {
        try {
            val marked = Json.parse(
                callLicenseHelper("command" to "mark-verification-failure", "snapshot" to state.snapshotJson),
            )
            val snapshot = if (marked["snapshot"].exists) marked["snapshot"].toString() else state.snapshotJson
            saveLicenseState(state.copy(snapshotJson = snapshot))
        } catch (_: RuntimeException) {
            // Preserve the original activation error when offline marking fails.
        }
    }

    private fun applyLicensePayload(state: LicenseState, appliedJson: String): LicenseState {
        val applied = Json.parse(appliedJson)
        if (!applied.isObject) throw PlatformServiceException("license helper returned an invalid payload")
        fun member(name: String, fallback: String) = if (applied[name].exists) applied[name].toString() else fallback
        return state.copy(
            snapshotJson = member("snapshot", state.snapshotJson),
            grantJson = member("grant", state.grantJson),
            deviceStateJson = member("deviceState", state.deviceStateJson),
        )
    }

    private fun callLicenseHelper(vararg request: Pair<String, Any?>): String {
        val envelope = Json.parse(runChecked(listOf(licenseHelper()), Json.obj(*request), true))
        if (!envelope.isObject || !envelope["ok"].bool(false)) {
            throw PlatformServiceException(envelope["error"].text("license helper failed"))
        }
        if (!envelope["result"].exists) throw PlatformServiceException("license helper returned no result")
        return envelope["result"].text()
    }

    private fun readLicenseKey(): String {
        val result = commandRunner.run(
            listOf("secret-tool", "lookup", "service", "clambhook", "account", "license-key"), "", false,
        )
        return if (result.exitCode == 0) result.output.trimEnd() else ""
    }

    private fun writeLicenseKey(value: String) {
        runChecked(
            listOf("secret-tool", "store", "--label=ClambHook license key", "service", "clambhook", "account", "license-key"),
            value, true,
        )
    }

    private fun loadLicenseState(): LicenseState {
        if (!Files.exists(licenseStatePath)) return LicenseState()
        val root = try {
            Json.parse(Files.readString(licenseStatePath))
        } catch (error: IOException) {
            throw PlatformServiceException("read license state: ${error.message}", error)
        }
        if (!root.isObject) throw PlatformServiceException("license state must be a JSON object")
        return LicenseState(
            installId = root["installId"].text(),
            email = root["email"].text(),
            snapshotJson = root["snapshotJson"].text(),
            grantJson = root["grantJson"].text(),
            deviceStateJson = root["deviceStateJson"].text(),
        )
    }

    private fun saveLicenseState(state: LicenseState) {
        writePrivateFile(
            licenseStatePath,
            Json.obj(
                "installId" to state.installId,
                "email" to state.email,
                "snapshotJson" to state.snapshotJson,
                "grantJson" to state.grantJson,
                "deviceStateJson" to state.deviceStateJson,
            ),
        )
        writePrivateFile(licenseStatePath.resolveSibling("license-snapshot.json"), state.snapshotJson.ifBlank { "{}" })
    }

    // --- daemon supervision --------------------------------------------------

    private suspend fun ensureDaemonAvailable() {
        val firstError = try {
            runtime.status()
            return
        } catch (error: CancellationException) {
            throw error
        } catch (error: Throwable) {
            error
        }
        if (!usesLoopbackDaemon()) {
            throw PlatformServiceException("configured ClambHook daemon is unavailable: ${firstError.message}", firstError)
        }
        io { runChecked(listOf("systemctl", "start", "clambhook-daemon.service"), "", false) }
        var lastError: Throwable = firstError
        repeat(8) { attempt ->
            try {
                runtime.status()
                return
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                lastError = error
                if (attempt < 7) delay(250)
            }
        }
        throw PlatformServiceException(
            "clambhook-daemon.service started but its API did not become ready: ${lastError.message}", firstError,
        )
    }

    private fun usesLoopbackDaemon(): Boolean {
        val settings = runtime.endpointSettings ?: return false
        val host = runCatching { URI(settings.baseUrl).host }.getOrNull() ?: return false
        return host.equals("localhost", true) || host == "::1" || host == "[::1]" ||
            Regex("127(?:\\.[0-9]{1,3}){3}").matches(host)
    }

    // --- updates (signed apt / dnf repositories only) ------------------------

    private fun performPackageUpdate(operation: String, requestJson: String): Result {
        val normalized = operation.trim().lowercase()
        val request = requestJson.ifBlank { "{}" }
        val provider = packageProvider()
            ?: return Result(
                false,
                Json.obj("operation" to normalized, "request" to request),
                "updates are supported only on Ubuntu (apt) and Fedora (dnf)",
            )
        return when (normalized) {
            "check" -> {
                val result = if (provider == "apt") {
                    commandRunner.run(listOf("apt-cache", "policy", "clambhook"), "", false)
                } else {
                    commandRunner.run(listOf("dnf", "--quiet", "check-upgrade", "clambhook"), "", false)
                }
                val accepted = result.exitCode == 0 || (provider == "dnf" && result.exitCode == 100)
                if (!accepted) throw processFailure(provider, result)
                val available = if (provider == "dnf") result.exitCode == 100 else aptUpdateAvailable(result.output)
                Result(
                    true,
                    Json.obj(
                        "provider" to provider,
                        "operation" to "check",
                        "update_available" to available,
                        "output" to result.output.trim(),
                    ),
                    if (available) "a signed package update is available" else "the installed package is current",
                )
            }
            "install" -> {
                val command = if (provider == "apt") {
                    listOf("pkexec", "apt-get", "--only-upgrade", "install", "-y", "clambhook")
                } else {
                    listOf("pkexec", "dnf", "upgrade", "-y", "clambhook")
                }
                val output = runChecked(command, "", false)
                Result(
                    true,
                    Json.obj("provider" to provider, "operation" to "install", "output" to output.trim()),
                    "the signed package update completed",
                )
            }
            else -> Result(false, Json.obj("operation" to normalized, "request" to request), "unsupported update operation")
        }
    }

    /** `apt` on Ubuntu, `dnf` on Fedora, null on any other distribution. */
    internal fun packageProvider(): String? {
        System.getProperty("clambhook.packageManager", "").trim().let {
            if (it == "apt" || it == "dnf") return it
        }
        return when (supportedDistribution(osReleasePath)) {
            "ubuntu" -> "apt"
            "fedora" -> "dnf"
            else -> null
        }
    }

    private fun runChecked(command: List<String>, input: String, inputRequired: Boolean): String {
        val result = commandRunner.run(command, input, inputRequired)
        if (result.exitCode != 0) throw processFailure(command.first(), result)
        return result.output
    }

    private suspend fun <T> io(block: () -> T): T = withContext(Dispatchers.IO) { block() }

    private data class LicenseState(
        val installId: String = "",
        val email: String = "",
        val snapshotJson: String = "",
        val grantJson: String = "",
        val deviceStateJson: String = "",
    )

    fun interface CommandRunner {
        fun run(command: List<String>, input: String, inputRequired: Boolean): ProcessResult
    }

    data class ProcessResult(val exitCode: Int, val output: String)

    companion object {
        private const val PROCESS_OUTPUT_LIMIT = 8 * 1024 * 1024

        /** The `ID` from os-release when it is `ubuntu` or `fedora`, otherwise null. */
        fun supportedDistribution(osRelease: Path): String? {
            val id = runCatching { Files.readAllLines(osRelease) }.getOrDefault(emptyList())
                .firstOrNull { it.startsWith("ID=") }
                ?.removePrefix("ID=")?.trim()?.trim('"', '\'')?.lowercase()
            return id?.takeIf { it == "ubuntu" || it == "fedora" }
        }

        internal fun aptUpdateAvailable(output: String): Boolean {
            var installed = ""
            var candidate = ""
            output.lines().map { it.trim() }.forEach { line ->
                if (line.startsWith("Installed:")) installed = line.removePrefix("Installed:").trim()
                if (line.startsWith("Candidate:")) candidate = line.removePrefix("Candidate:").trim()
            }
            return candidate.isNotBlank() && candidate != "(none)" && candidate != installed
        }

        private fun licenseHelper(): String {
            System.getProperty("clambhook.licenseHelper", "").trim().let { if (it.isNotBlank()) return it }
            return System.getenv("CLAMBHOOK_LICENSE_HELPER")?.trim()?.takeIf { it.isNotBlank() } ?: "clambhook-license"
        }

        private fun safeKey(key: String): String {
            val value = key.trim()
            if (value.isBlank() || value.length > 128 ||
                !value.all { it.isLetterOrDigit() || it == '.' || it == '_' || it == '-' }) {
                throw PlatformServiceException("secure-storage key is invalid")
            }
            return value
        }

        private fun deviceRegistration(installId: String): String {
            val hostname = System.getenv("HOSTNAME")?.trim().orEmpty().ifBlank { "GNU/Linux device" }
            val version = DesktopPlatformServices::class.java.`package`?.implementationVersion ?: "dev"
            return Json.obj(
                "install_id" to installId,
                "display_name" to hostname,
                "platform" to "linux",
                "architecture" to System.getProperty("os.arch", "unknown"),
                "app_version" to version,
            )
        }

        private fun defaultLicenseStatePath(): Path {
            val configHome = System.getenv("XDG_CONFIG_HOME")?.trim().orEmpty()
            val base = if (configHome.isBlank()) Path.of(System.getProperty("user.home"), ".config") else Path.of(configHome)
            return base.resolve("clambhook").resolve("linux-license.json")
        }

        private fun writePrivateFile(destination: Path, value: String) {
            var temporary: Path? = null
            try {
                val parent = destination.parent
                if (parent != null) Files.createDirectories(parent)
                temporary = Files.createTempFile(parent, ".clambhook-license-", ".tmp")
                Files.writeString(temporary, value, StandardOpenOption.TRUNCATE_EXISTING)
                try {
                    Files.setPosixFilePermissions(temporary, PosixFilePermissions.fromString("rw-------"))
                } catch (_: UnsupportedOperationException) {
                    // POSIX permissions are available on supported GNU/Linux hosts.
                }
                try {
                    Files.move(temporary, destination, StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING)
                } catch (_: AtomicMoveNotSupportedException) {
                    Files.move(temporary, destination, StandardCopyOption.REPLACE_EXISTING)
                }
                temporary = null
            } catch (error: IOException) {
                throw PlatformServiceException("write license state: ${error.message}", error)
            } finally {
                temporary?.let { runCatching { Files.deleteIfExists(it) } }
            }
        }

        private fun processFailure(command: String, result: ProcessResult) =
            PlatformServiceException("$command failed with exit code ${result.exitCode}: ${result.output.trim()}")

        internal fun runProcess(command: List<String>, input: String, inputRequired: Boolean): ProcessResult {
            val process = try {
                ProcessBuilder(command).redirectErrorStream(true).start()
            } catch (error: IOException) {
                throw PlatformServiceException("cannot run ${command.first()}: ${error.message}", error)
            }
            try {
                process.outputStream.use { output ->
                    if (inputRequired || input.isNotEmpty()) {
                        output.write(input.encodeToByteArray())
                        output.write('\n'.code)
                    }
                }
                var bytes = ByteArray(0)
                val reader = Thread({ bytes = process.inputStream.use { it.readNBytes(PROCESS_OUTPUT_LIMIT + 1) } },
                    "clambhook-command-output").apply {
                    isDaemon = true
                    start()
                }
                if (!process.waitFor(20, TimeUnit.SECONDS)) {
                    process.destroyForcibly()
                    reader.interrupt()
                    throw PlatformServiceException("${command.first()} timed out")
                }
                reader.join(5_000)
                if (reader.isAlive) {
                    process.destroyForcibly()
                    throw PlatformServiceException("timed out reading ${command.first()} output")
                }
                if (bytes.size > PROCESS_OUTPUT_LIMIT) {
                    throw PlatformServiceException("${command.first()} output exceeds safety limit")
                }
                return ProcessResult(process.exitValue(), bytes.decodeToString())
            } catch (error: IOException) {
                throw PlatformServiceException("cannot run ${command.first()}: ${error.message}", error)
            } catch (error: InterruptedException) {
                Thread.currentThread().interrupt()
                throw PlatformServiceException("${command.first()} interrupted", error)
            }
        }
    }
}
