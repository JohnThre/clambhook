// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui

import com.clambhook.ui.platform.PlatformServices
import com.clambhook.ui.platform.PlatformServices.AppRoutingSettings
import com.clambhook.ui.platform.PlatformServices.Capability
import com.clambhook.ui.platform.PlatformServices.InstalledApplication
import com.clambhook.ui.platform.PlatformServices.Result
import com.clambhook.ui.runtime.BackendException
import com.clambhook.ui.runtime.ConfigurableEndpointBackend
import kotlinx.coroutines.CompletableDeferred
import java.util.concurrent.atomic.AtomicInteger

/** Backend answering the frozen GET routes with canned documents. */
open class ScriptedBackend(
    override var baseUrl: String = "http://127.0.0.1:9090",
    var failStatus: Boolean = false,
    var activeProfile: String = "default",
    var running: Boolean = false,
) : ConfigurableEndpointBackend {
    val statusCalls = AtomicInteger()
    val connectCalls = AtomicInteger()
    val calls = mutableListOf<String>()

    override suspend fun request(method: String, path: String, body: String): String {
        synchronized(calls) { calls += "$method $path" }
        val route = path.substringBefore('?')
        return when {
            route == "/api/v1/status" -> {
                statusCalls.incrementAndGet()
                if (failStatus) throw BackendException(0, "Cannot reach the daemon")
                """{"running":$running,"profile":"$activeProfile","listeners":[]}"""
            }
            route == "/api/v1/profiles" ->
                if (activeProfile.isBlank()) """{"profiles":[],"active":""}"""
                else """{"profiles":["$activeProfile","work"],"active":"$activeProfile"}"""
            route == "/api/v1/connect" -> {
                connectCalls.incrementAndGet()
                """{"accepted":true}"""
            }
            route == "/api/v1/config/export" -> "[profiles.default]\n"
            route == "/api/v1/developer/ca.pem" -> "-----BEGIN CERTIFICATE-----\n"
            else -> "{}"
        }
    }

    override fun configure(baseUrl: String, bearerToken: String) {
        this.baseUrl = baseUrl
    }

    override val displayName = "Test runtime"
    override val supportsConnectionControl = true
    override fun close() = Unit
}

/** Platform services with every capability except the ones a test opts out of. */
open class FakePlatformServices(
    override val capabilities: Set<Capability> = Capability.entries.toSet() - Capability.PER_APP_ROUTING,
) : PlatformServices {
    var pendingStart: CompletableDeferred<Unit>? = null
    val browserUris = mutableListOf<String>()

    override suspend fun requestVpnConsent() = true
    override suspend fun startVpn() {
        pendingStart?.await()
    }
    override suspend fun stopVpn() = Unit
    override suspend fun readTextFile(path: String, maximumBytes: Int) = ""
    override suspend fun writeTextFile(path: String, value: String) = Unit
    override suspend fun scanQrCode() = ""
    override suspend fun shareQrCode(value: String) = Unit
    override suspend fun secureRead(key: String) = ""
    override suspend fun secureWrite(key: String, value: String) = Unit
    override suspend fun secureDelete(key: String) = Unit
    override suspend fun clipboardRead() = ""
    override suspend fun clipboardWrite(value: String) = Unit
    override suspend fun openBrowser(uri: String) {
        browserUris += uri
    }
    override suspend fun notify(title: String, body: String) = Unit
    override suspend fun installedApplications() = emptyList<InstalledApplication>()
    override suspend fun appRoutingSettings() = AppRoutingSettings()
    override suspend fun updateAppRoutingSettings(mode: String, packageNames: Set<String>) =
        AppRoutingSettings(mode, packageNames)
    override suspend fun licensing(operation: String, requestJson: String) =
        Result(true, """{"status":{"decision":{"supporterTier":"none"}}}""")
    override suspend fun updates(operation: String, requestJson: String) = Result(true, "{}", "current")
    override val platformName = "Test"
    override fun close() = Unit
}
