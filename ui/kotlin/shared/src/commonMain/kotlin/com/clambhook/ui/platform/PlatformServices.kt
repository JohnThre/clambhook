// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.platform

/**
 * Capability boundary for everything outside the runtime control contract:
 * VPN consent/lifecycle, files, QR, secure storage, clipboard, browser,
 * notifications, per-app routing, licensing, updates, and GNU/Linux daemon
 * supervision. Views check [supports] before offering an action.
 */
interface PlatformServices : AutoCloseable {
    enum class Capability {
        VPN_CONSENT,
        FILES,
        QR_SCAN,
        QR_SHARE,
        SECURE_STORAGE,
        CLIPBOARD,
        BROWSER,
        NOTIFICATIONS,
        LICENSING,
        UPDATES,
        PER_APP_ROUTING,
        DAEMON_SUPERVISION,
    }

    val capabilities: Set<Capability>

    fun supports(capability: Capability): Boolean = capability in capabilities

    suspend fun requestVpnConsent(): Boolean
    suspend fun startVpn()
    suspend fun stopVpn()
    suspend fun readTextFile(path: String, maximumBytes: Int): String
    suspend fun writeTextFile(path: String, value: String)
    suspend fun scanQrCode(): String
    suspend fun shareQrCode(value: String)
    suspend fun secureRead(key: String): String
    suspend fun secureWrite(key: String, value: String)
    suspend fun secureDelete(key: String)
    suspend fun clipboardRead(): String
    suspend fun clipboardWrite(value: String)

    /** An `ss://` or `ssconf://` link opened from outside the app, consumed once. */
    suspend fun takePendingOutlineAccessKey(): String = ""

    suspend fun openBrowser(uri: String)
    suspend fun notify(title: String, body: String)
    suspend fun installedApplications(): List<InstalledApplication>
    suspend fun appRoutingSettings(): AppRoutingSettings
    suspend fun updateAppRoutingSettings(mode: String, packageNames: Set<String>): AppRoutingSettings
    suspend fun licensing(operation: String, requestJson: String): Result
    suspend fun updates(operation: String, requestJson: String): Result

    val platformName: String

    override fun close()

    data class Result(val successful: Boolean, val payload: String = "", val message: String = "")

    data class InstalledApplication(val packageName: String, val label: String, val system: Boolean)

    data class AppRoutingSettings(val mode: String = "all", val packageNames: Set<String> = emptySet())
}

class PlatformServiceException(message: String, cause: Throwable? = null) : RuntimeException(message, cause)
