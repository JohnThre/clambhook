package com.clambhook.android

import kotlinx.serialization.Serializable

/**
 * Update manifest served by clambercloud.com's `/api/clambhook/android-manifest`.
 * ClambHook is sideloaded (no Play Store), so the app polls this to detect and
 * install newer signed APKs from clambercloud.com.
 */
@Serializable
data class AndroidUpdateManifest(
    val versionCode: Long = 0,
    val versionName: String = "",
    val minSdk: Int = 0,
    val publishedAt: String = "",
    val apkUrl: String = "",
    val sha256: String = "",
    val notes: String = "",
)

/** A newer release resolved from the manifest and gated against the license. */
data class AvailableUpdate(
    val manifest: AndroidUpdateManifest,
    val publishedAtMillis: Long,
    val installable: Boolean,
)

/** UI state for the in-app updater. */
data class UpdateUiState(
    val checking: Boolean = false,
    val downloading: Boolean = false,
    val available: AvailableUpdate? = null,
    val upToDate: Boolean = false,
    val message: String = "",
)
