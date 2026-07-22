package com.clambhook.linux.license

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.*
import com.clambhook.linux.model.ApiJson
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
const val LICENSE_VALIDATION_BASE_URL = "https://store.swiphtgroup.com/clambhook/license"
const val LICENSE_BUY_URL = "https://store.swiphtgroup.com/clambhook/buy/"
const val LICENSE_PORTAL_URL = "https://store.swiphtgroup.com/clambhook/portal/"

@Serializable
data class LicenseDecision(
    val reason: String = "locked",
    @SerialName("trialStartDate") val trialStartDate: String = "",
    @SerialName("trialEndsAt") val trialEndsAt: String = "",
    @SerialName("trialDaysRemaining") val trialDaysRemaining: Int = 0,
    @SerialName("hasLifetimeUnlock") val hasLifetimeUnlock: Boolean = false,
    @SerialName("updateCutoffDate") val updateCutoffDate: String = "",
    @SerialName("offlineGraceEndsAt") val offlineGraceEndsAt: String = ""
) {
    fun canUseApp(): Boolean = reason != "locked"
    fun title(): String = when (reason) {
        "trial" -> "Trial active"
        "lifetime" -> "Licensed"
        "offlineGrace" -> "Licensed (offline grace)"
        else -> "License required"
    }
    fun detail(): String = when (reason) {
        "trial" -> "$trialDaysRemaining days left in the one-month trial"
        "lifetime" -> if (updateCutoffDate.isEmpty()) "Updates included during your entitlement window" else "Updates included through ${shortDate(updateCutoffDate)}"
        "offlineGrace" -> if (offlineGraceEndsAt.isEmpty()) "Using cached license while verification is unavailable" else "Offline grace until ${shortDate(offlineGraceEndsAt)}"
        else -> "Activate a license key to continue using ClambHook."
    }
}

@Serializable
data class LicenseProductState(
    val kind: String = "",
    val title: String = "",
    val detail: String = "",
    val active: Boolean = false
)

@Serializable
data class LicenseRecoveryState(
    val kind: String = "",
    val severity: String = "",
    val title: String = "",
    val detail: String = "",
    @SerialName("primaryAction") val primaryAction: String = ""
)

@Serializable
data class LicenseDevice(
    @SerialName("device_id") val deviceId: String = "",
    @SerialName("install_id") val installId: String = "",
    @SerialName("display_name") val displayName: String = "",
    val platform: String = "",
    val architecture: String = "",
    @SerialName("app_version") val appVersion: String = "",
    @SerialName("activated_at") val activatedAt: String = "",
    @SerialName("last_seen_at") val lastSeenAt: String = "",
    @SerialName("deactivated_at") val deactivatedAt: String = ""
) {
    fun active(): Boolean = deactivatedAt.isEmpty()
}

@Serializable
data class LicenseDeviceState(
    @SerialName("current_install_id") val currentInstallId: String = "",
    @SerialName("current_device_id") val currentDeviceId: String = "",
    @SerialName("max_active_devices") val maxActiveDevices: Int = 10,
    @SerialName("payment_provider") val paymentProvider: String = "",
    val devices: List<LicenseDevice> = emptyList()
) {
    fun activeCount(): Int = devices.count { it.active() }
}

@Serializable
data class LicenseStatus(
    val decision: LicenseDecision = LicenseDecision(),
    @SerialName("productStates") val productStates: List<LicenseProductState> = emptyList(),
    @SerialName("expiredTrial") val expiredTrial: LicenseRecoveryState? = null,
    @SerialName("licenseExpiredForUpdates") val licenseExpiredForUpdates: LicenseRecoveryState? = null
) {
    companion object {
        fun fromJson(json: String): LicenseStatus = try {
            ApiJson.decodeFromString(serializer(), json)
        } catch (e: Exception) { LicenseStatus() }
    }
}

@Serializable
data class LicensePersistedState(
    @SerialName("installId") val installId: String = "",
    @SerialName("email") val email: String = "",
    @SerialName("snapshotJson") val snapshotJson: String = "",
    @SerialName("grantJson") val grantJson: String = "",
    @SerialName("deviceStateJson") val deviceStateJson: String = ""
)

interface LicenseStateStore {
    fun load(): LicensePersistedState
    fun save(state: LicensePersistedState)
    fun daemonSnapshotPath(): String
}

class FileLicenseStateStore(private val path: String = defaultPath()) : LicenseStateStore {
    override fun load(): LicensePersistedState = try {
        if (java.nio.file.Files.exists(java.nio.file.Paths.get(path))) {
            ApiJson.decodeFromString(LicensePersistedState.serializer(), java.nio.file.Files.readString(java.nio.file.Paths.get(path)))
        } else LicensePersistedState()
    } catch (e: Exception) { LicensePersistedState() }

    override fun save(state: LicensePersistedState) {
        val parent = java.nio.file.Paths.get(path).parent
        java.nio.file.Files.createDirectories(parent)
        java.nio.file.Files.writeString(java.nio.file.Paths.get(path), ApiJson.encodeToString(LicensePersistedState.serializer(), state))
        exportDaemonSnapshot(state)
    }

    override fun daemonSnapshotPath(): String =
        java.nio.file.Paths.get(path).resolveSibling("license-snapshot.json").toString()

    private fun exportDaemonSnapshot(state: LicensePersistedState) {
        val snapshot = state.snapshotJson.trim().ifEmpty { "{}" }
        java.nio.file.Files.writeString(java.nio.file.Paths.get(daemonSnapshotPath()), snapshot)
    }

    companion object {
        fun defaultPath(): String =
            java.nio.file.Paths.get(System.getenv("XDG_CONFIG_HOME") ?: System.getProperty("user.home") + "/.config")
                .resolve("clambhook").resolve("linux-license.json").toString()
    }
}

interface LicenseKeyVault {
    suspend fun readLicenseKey(): String
    suspend fun saveLicenseKey(licenseKey: String)
}

class SecretLicenseKeyVault : LicenseKeyVault {
    override suspend fun readLicenseKey(): String = readSecret()
    override suspend fun saveLicenseKey(licenseKey: String) {
        val trimmed = licenseKey.trim()
        if (trimmed.isEmpty()) clearSecret() else storeSecret(trimmed)
    }
    private fun storeSecret(value: String) {
        try {
            val p = ProcessBuilder("secret-tool", "store", "--label", "ClambHook license key", "account", ACCOUNT, SCHEMA_NAME).start()
            p.outputStream.use { it.write(value.toByteArray()); it.flush() }
            p.waitFor()
        } catch (e: Exception) {}
    }
    private fun readSecret(): String = try {
        val p = ProcessBuilder("secret-tool", "lookup", "account", ACCOUNT, SCHEMA_NAME).redirectError(ProcessBuilder.Redirect.DISCARD).start()
        val r = p.inputStream.bufferedReader().readText().trim(); p.waitFor(); r
    } catch (e: Exception) { "" }
    private fun clearSecret() {
        try { ProcessBuilder("secret-tool", "clear", "account", ACCOUNT, SCHEMA_NAME).redirectError(ProcessBuilder.Redirect.DISCARD).start().waitFor() } catch (e: Exception) {}
    }
    companion object {
        private const val SCHEMA_NAME = "com.clambhook.Clambhook.LicenseKey"
        private const val ACCOUNT = "default"
    }
}

class LicenseHelperClient(private val helperPath: String) {
    suspend fun call(command: String, request: JsonObject): String {
        if (helperPath.trim().isEmpty()) throw IllegalStateException("clambhook-license helper was not found")
        val process = ProcessBuilder(helperPath).start()
        val requestStr = request.toString()
        process.outputStream.use { it.write(requestStr.toByteArray()); it.flush() }
        val stdout = process.inputStream.bufferedReader().readText()
        val stderr = process.errorStream.bufferedReader().readText()
        process.waitFor()
        if (process.exitValue() != 0) throw IllegalStateException(stderr.ifEmpty { "clambhook-license failed" })
        val response = ApiJson.parseToJsonElement(stdout).jsonObject
        if (!response["ok"]?.jsonPrimitive?.booleanOrNull!!) {
            val message = response["error"]?.jsonPrimitive?.content ?: ""
            throw IllegalStateException(message.ifEmpty { "$command failed" })
        }
        return response["result"]?.jsonPrimitive?.content ?: ""
    }

    companion object {
        fun resolveHelperPath(appBaseDir: String, searchPath: Boolean = true): String? {
            if (searchPath) {
                val pathExec = System.getenv("PATH").split(java.io.File.pathSeparator).map { java.io.File(it, "clambhook-license") }
                    .firstOrNull { it.exists() }?.absolutePath
                if (pathExec != null && pathExec.isNotEmpty()) return pathExec
            }
            val adjacent = java.io.File(appBaseDir, "clambhook-license"); if (adjacent.exists()) return adjacent.absolutePath
            val siblingLibexec = java.io.File(java.io.File(appBaseDir).parentFile, "libexec/clambhook-license")
            if (siblingLibexec.exists()) return siblingLibexec.absolutePath
            val usrLibexec = java.io.File("/usr/libexec/clambhook-license"); if (usrLibexec.exists()) return usrLibexec.absolutePath
            return null
        }
    }
}

data class LicenseManagerState(
    val status: LicenseStatus = LicenseStatus(),
    val deviceState: LicenseDeviceState = LicenseDeviceState(),
    val hasLicenseKey: Boolean = false,
    val loading: Boolean = false,
    val initialized: Boolean = false,
    val email: String = "",
    val message: String = ""
)

class LicenseManager(
    private val stateStore: LicenseStateStore,
    private val keyVault: LicenseKeyVault,
    private val helper: LicenseHelperClient
) {
    private var persisted: LicensePersistedState = stateStore.load()
    private val _state = kotlinx.coroutines.flow.MutableStateFlow(LicenseManagerState(email = persisted.email))
    val state: kotlinx.coroutines.flow.StateFlow<LicenseManagerState> = _state.asStateFlow()

    fun daemonSnapshotPath(): String = stateStore.daemonSnapshotPath()

    suspend fun start() {
        update { it.copy(loading = true) }
        try {
            if (persisted.installId.isEmpty()) persisted = persisted.copy(installId = installId())
            persisted = persisted.copy(snapshotJson = ensureTrial(persisted.snapshotJson))
            stateStore.save(persisted)
            val hasKey = keyVault.readLicenseKey().trim().isNotEmpty()
            refresh()
            update { it.copy(hasLicenseKey = hasKey, initialized = true) }
        } catch (e: Exception) {
            update { it.copy(message = e.message ?: "error", initialized = true) }
        }
        update { it.copy(loading = false) }
    }

    suspend fun refresh() {
        try {
            val status = LicenseStatus.fromJson(statusJson(persisted.snapshotJson))
            val deviceState = if (persisted.deviceStateJson.isEmpty()) LicenseDeviceState()
                else ApiJson.decodeFromString(LicenseDeviceState.serializer(), persisted.deviceStateJson)
            update { it.copy(status = status, deviceState = deviceState, email = persisted.email) }
        } catch (e: Exception) {
            update { it.copy(message = e.message ?: "error") }
        }
    }

    suspend fun activate(licenseKey: String, email: String) {
        update { it.copy(loading = true, message = "Activating license...") }
        try {
            val result = activateJson(licenseKey, email)
            applyAppliedPayload(result)
            persisted = persisted.copy(email = email.trim())
            keyVault.saveLicenseKey(licenseKey)
            stateStore.save(persisted)
            update { it.copy(hasLicenseKey = true, message = "License activated on this GNU/Linux device.") }
            refresh()
        } catch (e: Exception) {
            try {
                persisted = persisted.copy(snapshotJson = markVerificationFailure(persisted.snapshotJson))
                stateStore.save(persisted); refresh()
            } catch (ignored: Exception) {}
            update { it.copy(message = e.message ?: "error") }
        }
        update { it.copy(loading = false) }
    }

    suspend fun deactivateCurrentDevice() = deviceAction("deactivate", "This device was deactivated.")
    suspend fun reactivateCurrentDevice() = deviceAction("reactivate", "This device was reactivated.")
    suspend fun transferCurrentDevice() = deviceAction("transfer", "This device was deactivated; the seat is available to transfer.")

    private suspend fun deviceAction(action: String, successMessage: String) {
        update { it.copy(loading = true, message = "Updating device seat...") }
        try {
            val key = keyVault.readLicenseKey()
            if (key.trim().isEmpty()) throw IllegalStateException("Enter a license key before managing devices.")
            val result = deviceActionJson(action, key)
            applyAppliedPayload(result)
            stateStore.save(persisted)
            update { it.copy(message = successMessage) }
            refresh()
        } catch (e: Exception) {
            update { it.copy(message = e.message ?: "error") }
        }
        update { it.copy(loading = false) }
    }

    private suspend fun installId(): String {
        val req = buildJsonObject { put("command", "install-id") }
        return helper.call("install-id", req)
    }
    private suspend fun ensureTrial(snapshotJson: String): String {
        val req = buildJsonObject { put("command", "ensure-trial"); put("snapshot", snapshotJson) }
        return helper.call("ensure-trial", req)
    }
    private suspend fun statusJson(snapshotJson: String): String {
        val req = buildJsonObject { put("command", "status"); put("snapshot", snapshotJson) }
        return helper.call("status", req)
    }
    private suspend fun activateJson(licenseKey: String, email: String): String {
        val req = buildJsonObject {
            put("command", "activate")
            put("baseURL", LICENSE_VALIDATION_BASE_URL)
            put("licenseKey", licenseKey)
            put("email", email)
            put("deviceRegistration", deviceRegistrationJson())
        }
        return helper.call("activate", req)
    }
    private suspend fun deviceActionJson(action: String, licenseKey: String): String {
        val req = buildJsonObject {
            put("command", "device-action")
            put("baseURL", LICENSE_VALIDATION_BASE_URL)
            put("action", action)
            put("licenseKey", licenseKey)
            put("installID", persisted.installId)
            put("deviceID", _state.value.deviceState.currentDeviceId)
            put("deviceRegistration", deviceRegistrationJson())
        }
        return helper.call("device-action", req)
    }
    private suspend fun markVerificationFailure(snapshotJson: String): String {
        val req = buildJsonObject { put("command", "mark-verification-failure"); put("snapshot", snapshotJson) }
        val result = helper.call("mark-verification-failure", req)
        try {
            val obj = ApiJson.parseToJsonElement(result).jsonObject
            if (obj["snapshot"] != null) return obj["snapshot"].toString()
        } catch (e: Exception) {}
        return snapshotJson
    }

    private fun applyAppliedPayload(result: String) {
        val obj = ApiJson.parseToJsonElement(result).jsonObject
        if (obj["snapshot"] != null) persisted = persisted.copy(snapshotJson = obj["snapshot"].toString())
        if (obj["grant"] != null) persisted = persisted.copy(grantJson = obj["grant"].toString())
        if (obj["deviceState"] != null) persisted = persisted.copy(deviceStateJson = obj["deviceState"].toString())
    }

    private fun deviceRegistrationJson(): String = buildJsonObject {
        put("install_id", persisted.installId)
        put("display_name", deviceDisplayName())
        put("platform", "linux")
        put("architecture", deviceArchitecture())
        put("app_version", "0.1.0")
    }.toString()

    private fun update(f: (LicenseManagerState) -> LicenseManagerState) { _state.value = f(_state.value) }
}

private fun shortDate(iso: String): String = if (iso.length >= 10) iso.substring(0, 10) else iso
private fun deviceDisplayName(): String {
    val host = System.getenv("HOSTNAME") ?: ""
    return if (host.isBlank()) "GNU/Linux device" else host.trim()
}
private fun deviceArchitecture(): String {
    val arch = (System.getenv("HOSTTYPE") ?: "").ifBlank { System.getenv("MACHTYPE") ?: "" }
    return if (arch.isBlank()) "unknown" else arch.trim()
}
