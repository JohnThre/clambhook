package com.clambhook.android

import android.content.Context
import java.io.ByteArrayOutputStream
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.withContext
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.encodeToJsonElement
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.put
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody

/** Aggregated license UI state derived from the shared C domain. */
data class LicenseUiState(
    val status: LicenseStatus = LicenseStatus(),
    val deviceState: LicenseDeviceState = LicenseDeviceState(),
    val hasLicenseKey: Boolean = false,
    val email: String = "",
    val loading: Boolean = false,
    val message: String = "",
    val initialized: Boolean = false,
) {
    val decision: LicenseDecision get() = status.decision
}

/**
 * Drives ClambHook's direct-sale license flow on Android. Persistence is local
 * (encrypted [LicenseStorage]); evaluation, date math, and the
 * store.swiphtgroup.com calls use Android's OkHttp client and the response is
 * validated and evaluated by C. Mirrors the macOS
 * MacLicenseManager: apply server responses, cache a verified snapshot, and fall
 * back to offline grace on verification failure.
 */
class LicenseManager(context: Context) {
    private val storage = LicenseStorage(context)
    private val json = Json { ignoreUnknownKeys = true }
    private val client = OkHttpClient()

    private val _state = MutableStateFlow(LicenseUiState())
    val state: StateFlow<LicenseUiState> = _state.asStateFlow()

    /** Store checkout page (Creem / NOWPayments only). */
    val buyUrl: String = "https://store.swiphtgroup.com/clambhook/buy/"

    /** Device-seat management portal. */
    val portalUrl: String = "https://store.swiphtgroup.com/clambhook/portal/"

    private val validationBaseUrl: String = "https://store.swiphtgroup.com/clambhook/license"

    /** Seeds the trial on first run and computes the initial status. */
    suspend fun start() = withContext(Dispatchers.IO) {
        storage.snapshotJson = NativeClambhookLicenseBridge.ensureTrial(storage.snapshotJson)
        recomputeStatus()
        _state.update { it.copy(initialized = true) }
    }

    /** Recomputes license status/banners from the cached snapshot. */
    suspend fun refresh() = withContext(Dispatchers.IO) { recomputeStatus() }

    /** Reports whether the release published at [publishedAtEpochMillis] may be
     * installed under the current license (0 when the date is unknown). */
    suspend fun canInstallUpdate(publishedAtEpochMillis: Long): Boolean =
        withContext(Dispatchers.IO) {
            runCatching {
                NativeClambhookLicenseBridge.updateAllowed(
                    storage.snapshotJson,
                    publishedAtEpochMillis,
                )
            }
                .getOrDefault(false)
        }

    /** Activates or refreshes this device with a license key. */
    suspend fun activate(licenseKey: String, email: String) = withContext(Dispatchers.IO) {
        val key = licenseKey.trim()
        if (key.isEmpty()) {
            _state.update { it.copy(message = "Enter a license key to activate this device.") }
            return@withContext
        }
        _state.update { it.copy(loading = true, message = "") }
        try {
            val registration = storage.deviceRegistration()
            val response = postLicense(
                "activate",
                buildJsonObject {
                    put("license_key", key)
                    email.trim().takeIf { it.isNotBlank() }?.let { put("email", it) }
                    put("device", json.encodeToJsonElement(registration))
                },
            )
            val appliedJson = NativeClambhookLicenseBridge.applyServerResponse(
                response,
                registration.installId,
            )
            applyResult(appliedJson)
            storage.licenseKey = key
            storage.email = email.trim()
            recomputeStatus()
            _state.update { it.copy(message = "License activated.") }
        } catch (error: Throwable) {
            markVerificationFailure()
            _state.update { it.copy(message = error.message ?: "License request failed.") }
        } finally {
            _state.update { it.copy(loading = false) }
        }
    }

    suspend fun deactivateCurrentDevice() = deviceAction("deactivate", "This device was deactivated.")

    suspend fun reactivateCurrentDevice() = deviceAction("reactivate", "This device was reactivated.")

    suspend fun transferCurrentDevice() =
        deviceAction("transfer", "This device was deactivated; the seat is available to transfer.")

    private suspend fun deviceAction(action: String, successMessage: String) = withContext(Dispatchers.IO) {
        val key = storage.licenseKey
        if (key.isBlank()) {
            _state.update { it.copy(message = "Activate with a license key before managing this device.") }
            return@withContext
        }
        _state.update { it.copy(loading = true, message = "") }
        try {
            val registration = storage.deviceRegistration()
            val deviceId = _state.value.deviceState.currentDevice?.deviceId.orEmpty()
            val installId = storage.installId()
            val response = postLicense(
                action,
                buildJsonObject {
                    put("license_key", key)
                    put("install_id", installId)
                    deviceId.takeIf { it.isNotBlank() }?.let { put("device_id", it) }
                    put("device", json.encodeToJsonElement(registration))
                },
            )
            val appliedJson = NativeClambhookLicenseBridge.applyServerResponse(
                response,
                installId,
            )
            applyResult(appliedJson)
            recomputeStatus()
            _state.update { it.copy(message = successMessage) }
        } catch (error: Throwable) {
            _state.update { it.copy(message = error.message ?: "License request failed.") }
        } finally {
            _state.update { it.copy(loading = false) }
        }
    }

    fun clearMessage() = _state.update { it.copy(message = "") }

    private fun applyResult(appliedJson: String) {
        val applied = json.decodeFromString<AppliedLicense>(appliedJson)
        storage.grantJson = applied.grant.toString()
        storage.snapshotJson = applied.snapshot.toString()
        storage.deviceStateJson = json.encodeToString(applied.deviceState)
    }

    private fun markVerificationFailure() {
        runCatching {
            val vfJson = NativeClambhookLicenseBridge.markVerificationFailure(storage.snapshotJson)
            val vf = json.decodeFromString<VerificationFailureResult>(vfJson)
            storage.snapshotJson = vf.snapshot.toString()
        }
    }

    private fun recomputeStatus() {
        val statusJson = NativeClambhookLicenseBridge.status(storage.snapshotJson)
        val status = json.decodeFromString<LicenseStatus>(statusJson)
        val deviceState = storage.deviceStateJson
            .takeIf { it.isNotBlank() }
            ?.let { runCatching { json.decodeFromString<LicenseDeviceState>(it) }.getOrNull() }
            ?: LicenseDeviceState()
        _state.update {
            it.copy(
                status = status,
                deviceState = deviceState,
                hasLicenseKey = storage.licenseKey.isNotBlank(),
                email = storage.email,
            )
        }
    }

    private fun postLicense(action: String, body: JsonObject): String {
        val request = Request.Builder()
            .url("${validationBaseUrl.trimEnd('/')}/v1/devices/$action")
            .post(body.toString().toRequestBody(JSON_MEDIA_TYPE))
            .build()
        return client.newCall(request).execute().use { response ->
            val payload = response.body?.byteStream()?.use(::readBoundedResponse).orEmpty()
            if (!response.isSuccessful) {
                val serverMessage = runCatching {
                    json.parseToJsonElement(payload).jsonObject["error"]
                        ?.jsonPrimitive?.contentOrNull
                }.getOrNull()
                error(serverMessage?.takeIf { it.isNotBlank() }
                    ?: "License request failed (${response.code}).")
            }
            payload
        }
    }

    private fun readBoundedResponse(input: java.io.InputStream): String {
        val output = ByteArrayOutputStream()
        val chunk = ByteArray(8192)
        var total = 0
        while (true) {
            val count = input.read(chunk)
            if (count < 0) break
            total += count
            check(total <= MAX_RESPONSE_BYTES) { "License response exceeds 1 MiB." }
            output.write(chunk, 0, count)
        }
        return output.toString(Charsets.UTF_8.name())
    }

    private companion object {
        val JSON_MEDIA_TYPE = "application/json; charset=utf-8".toMediaType()
        const val MAX_RESPONSE_BYTES = 1024 * 1024
    }
}
