package com.clambhook.android

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * Profile-import types mirroring the `pkg/mobile` import review/apply bridge
 * (`TunnelImportReviewJSON` / `ApplyReviewedTunnelImportJSON`). Imports are
 * ClambHook TOML configs supplied from a file, the clipboard, a subscription
 * URL, or a scanned QR code; the bridge parses and validates them.
 */

@Serializable
data class TunnelImportProfileSummary(
    val name: String = "",
    @SerialName("chain_count") val chainCount: Int = 0,
    @SerialName("server_count") val serverCount: Int = 0,
    @SerialName("rule_count") val ruleCount: Int = 0,
    val protocols: List<String> = emptyList(),
)

@Serializable
data class TunnelImportReview(
    @SerialName("active_profile") val activeProfile: String = "",
    val profiles: List<TunnelImportProfileSummary> = emptyList(),
)

@Serializable
data class ReviewedImportProfile(
    @SerialName("source_name") val sourceName: String,
    @SerialName("target_name") val targetName: String,
)

@Serializable
data class ReviewedImportRequest(
    @SerialName("import_text") val importText: String,
    val profiles: List<ReviewedImportProfile>,
    @SerialName("activate_profile") val activateProfile: String = "",
)

/** UI state for the profile-import flow. */
data class ProfileImportUiState(
    val busy: Boolean = false,
    val importText: String = "",
    val review: TunnelImportReview? = null,
    val message: String = "",
)
