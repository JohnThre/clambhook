package com.clambhook.android

import android.content.Context
import com.clambhook.mobile.Mobile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.withContext
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json

/** Aggregated license UI state derived from the shared Go domain. */
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
 * store.swiphtgroup.com HTTP calls run in Go via [Mobile]. Mirrors the macOS
 * MacLicenseManager: apply server responses, cache a verified snapshot, and fall
 * back to offline grace on verification failure.
 */
class LicenseManager(context: Context) {
    private val storage = LicenseStorage(context)
    private val json = Json { ignoreUnknownKeys = true }

    private val _state = MutableStateFlow(LicenseUiState())
    val state: StateFlow<LicenseUiState> = _state.asStateFlow()

    /** Store checkout page (Creem / NOWPayments only). */
    val buyUrl: String = "https://store.swiphtgroup.com/clambhook/buy/"

    /** Device-seat management portal. */
    val portalUrl: String = runCatching { Mobile.licensePortalURL() }
        .getOrDefault("https://store.swiphtgroup.com/clambhook/portal/")

    private val validationBaseUrl: String = runCatching { Mobile.licenseValidationBaseURL() }
        .getOrDefault("https://store.swiphtgroup.com/clambhook/license")

    /** Seeds the trial on first run and computes the initial status. */
    suspend fun start() = withContext(Dispatchers.IO) {
        storage.snapshotJson = Mobile.ensureLicenseTrialJSON(storage.snapshotJson, 0)
        recomputeStatus()
        _state.update { it.copy(initialized = true) }
    }

    /** Recomputes license status/banners from the cached snapshot. */
    suspend fun refresh() = withContext(Dispatchers.IO) { recomputeStatus() }

    /** Reports whether the release published at [publishedAtEpochMillis] may be
     * installed under the current license (0 when the date is unknown). */
    suspend fun canInstallUpdate(publishedAtEpochMillis: Long): Boolean =
        withContext(Dispatchers.IO) {
            runCatching { Mobile.licenseUpdateAllowed(storage.snapshotJson, publishedAtEpochMillis, 0) }
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
            val reg = json.encodeToString(storage.deviceRegistration())
            val appliedJson = Mobile.activateLicenseJSON(validationBaseUrl, key, email.trim(), reg, 0)
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
            val reg = json.encodeToString(storage.deviceRegistration())
            val deviceId = _state.value.deviceState.currentDevice?.deviceId.orEmpty()
            val appliedJson = Mobile.licenseDeviceActionJSON(
                validationBaseUrl, action, key, storage.installId(), deviceId, reg, 0,
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
            val vfJson = Mobile.markLicenseVerificationFailureJSON(storage.snapshotJson, 0)
            val vf = json.decodeFromString<VerificationFailureResult>(vfJson)
            storage.snapshotJson = vf.snapshot.toString()
        }
    }

    private fun recomputeStatus() {
        val statusJson = Mobile.licenseStatusJSON(storage.snapshotJson, 0, 0)
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
}
