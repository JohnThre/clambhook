// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

import kotlinx.serialization.ExperimentalSerializationApi
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonNames

/**
 * Android update manifest (`clambhook-android-manifest.json`) published on
 * GitHub Releases by `scripts/release-android.sh` and signed with the
 * developer@jpfchang.org key. ClambHook is sideloaded (no Play Store), so the
 * app polls it to detect and install newer signed APKs.
 */
@OptIn(ExperimentalSerializationApi::class)
@Serializable
data class AndroidUpdateManifest(
    val versionCode: Long = 0,
    val versionName: String = "",
    val minSdk: Int = 0,
    val publishedAt: String = "",
    val apkUrl: String = "",
    // The release script publishes the APK digest as `apkSha256`.
    @JsonNames("apkSha256")
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
