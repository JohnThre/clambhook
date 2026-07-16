package com.clambhook.android

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

/**
 * Kotlin mirror of the shared Go license domain (`internal/license`) as exposed
 * through `pkg/mobile`. Field names match the Go JSON tags exactly so the same
 * payloads round-trip across the gomobile boundary. All evaluation, date math,
 * and HTTP live in Go; these types are transport + rendering only.
 */

@Serializable
data class LicenseDecision(
    val reason: String = "locked",
    val trialStartDate: String? = null,
    val trialEndsAt: String? = null,
    val trialDaysRemaining: Int = 0,
    val hasLifetimeUnlock: Boolean = false,
    val updateCutoffDate: String? = null,
    val offlineGraceEndsAt: String? = null,
    val unlockedFeatureIDs: List<String> = emptyList(),
) {
    val canUseApp: Boolean get() = reason != "locked"
    val isTrialActive: Boolean get() = reason == "trial"
    val isOfflineGraceActive: Boolean get() = reason == "offlineGrace"

    fun canUseFeature(id: String): Boolean = canUseApp && unlockedFeatureIDs.contains(id)
}

@Serializable
data class LicenseProductState(
    val kind: String = "",
    val title: String = "",
    val detail: String = "",
    val isActive: Boolean = false,
)

@Serializable
data class LicenseRecoveryState(
    val kind: String = "",
    val severity: String = "warning",
    val title: String = "",
    val message: String = "",
    val primaryAction: String = "",
    val secondaryActions: List<String> = emptyList(),
    val diagnosticText: String = "",
)

@Serializable
data class LicenseStatus(
    val decision: LicenseDecision = LicenseDecision(),
    val productStates: List<LicenseProductState> = emptyList(),
    val expiredTrial: LicenseRecoveryState? = null,
    val licenseExpiredForUpdates: LicenseRecoveryState? = null,
)

@Serializable
data class LicenseDevice(
    @SerialName("device_id") val deviceId: String = "",
    @SerialName("install_id") val installId: String = "",
    @SerialName("display_name") val displayName: String = "",
    val platform: String = "",
    val architecture: String = "",
    @SerialName("activated_at") val activatedAt: String = "",
    @SerialName("last_seen_at") val lastSeenAt: String? = null,
    @SerialName("deactivated_at") val deactivatedAt: String? = null,
) {
    val isActive: Boolean get() = deactivatedAt == null
}

@Serializable
data class LicensePaymentProvider(val raw: String = "")

@Serializable
data class LicenseDeviceState(
    @SerialName("current_install_id") val currentInstallId: String = "",
    @SerialName("current_device_id") val currentDeviceId: String = "",
    @SerialName("max_active_devices") val maxActiveDevices: Int = 10,
    val devices: List<LicenseDevice> = emptyList(),
    @SerialName("payment_provider") val paymentProvider: String? = null,
) {
    val activeDeviceCount: Int get() = devices.count { it.isActive }
    val remainingActivations: Int get() = (maxActiveDevices - activeDeviceCount).coerceAtLeast(0)

    val currentDevice: LicenseDevice?
        get() = devices.firstOrNull { it.deviceId == currentDeviceId }
            ?: devices.firstOrNull { it.installId.isNotEmpty() && it.installId == currentInstallId }

    val isCurrentDeviceActive: Boolean get() = currentDevice?.isActive == true
    val canReactivateCurrentDevice: Boolean
        get() = currentDevice != null && !isCurrentDeviceActive && activeDeviceCount < maxActiveDevices
    val canTransferCurrentDevice: Boolean get() = isCurrentDeviceActive
}

/** Device identity sent to the license backend. */
@Serializable
data class LicenseDeviceRegistration(
    @SerialName("install_id") val installId: String,
    @SerialName("display_name") val displayName: String,
    val platform: String,
    val architecture: String,
    @SerialName("app_version") val appVersion: String = "",
)

/** Result of an activate/deactivate/reactivate/transfer call from the bridge. */
@Serializable
data class AppliedLicense(
    val grant: JsonElement,
    val snapshot: JsonElement,
    val deviceState: LicenseDeviceState = LicenseDeviceState(),
    val decision: LicenseDecision = LicenseDecision(),
)

/** Result of MarkLicenseVerificationFailureJSON. */
@Serializable
data class VerificationFailureResult(
    val snapshot: JsonElement,
    val decision: LicenseDecision = LicenseDecision(),
)
