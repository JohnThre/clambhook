// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.platform

import com.clambhook.android.AndroidRuntimeFacade
import com.clambhook.ui.json.Json
import com.clambhook.ui.platform.PlatformServices.AppRoutingSettings
import com.clambhook.ui.platform.PlatformServices.Capability
import com.clambhook.ui.platform.PlatformServices.InstalledApplication
import com.clambhook.ui.platform.PlatformServices.Result
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** Android platform services backed by the Kotlin platform library. */
class AndroidPlatformServices : PlatformServices {
    @OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
    private val dispatcher = Dispatchers.IO.limitedParallelism(1)

    override val capabilities: Set<Capability> = Capability.entries.toSet() - Capability.DAEMON_SUPERVISION

    override suspend fun requestVpnConsent(): Boolean =
        Json.parse(call("vpn-consent", "{}"))["granted"].bool(false)

    override suspend fun startVpn() {
        call("vpn-start", "{}")
    }

    override suspend fun stopVpn() {
        call("vpn-stop", "{}")
    }

    override suspend fun readTextFile(path: String, maximumBytes: Int): String =
        call("file-read", Json.obj("path" to path, "maximum_bytes" to maximumBytes))

    override suspend fun writeTextFile(path: String, value: String) {
        call("file-write", Json.obj("path" to path, "value" to value))
    }

    override suspend fun scanQrCode(): String = call("qr-scan", "{}")

    override suspend fun shareQrCode(value: String) {
        call("qr-share", Json.obj("value" to value))
    }

    override suspend fun secureRead(key: String): String = call("secure-read", Json.obj("key" to key))

    override suspend fun secureWrite(key: String, value: String) {
        call("secure-write", Json.obj("key" to key, "value" to value))
    }

    override suspend fun secureDelete(key: String) {
        call("secure-delete", Json.obj("key" to key))
    }

    override suspend fun clipboardRead(): String = call("clipboard-read", "{}")

    override suspend fun clipboardWrite(value: String) {
        call("clipboard-write", Json.obj("value" to value))
    }

    override suspend fun takePendingOutlineAccessKey(): String = call("outline-link-consume", "{}")

    override suspend fun openBrowser(uri: String) {
        call("browser-open", Json.obj("uri" to uri))
    }

    override suspend fun notify(title: String, body: String) {
        call("notify", Json.obj("title" to title, "body" to body))
    }

    override suspend fun installedApplications(): List<InstalledApplication> =
        Json.parse(call("installed-apps", "{}"))["applications"].elements().map {
            val packageName = it["package_name"].text()
            InstalledApplication(packageName, it["label"].text(packageName), it["system"].bool(false))
        }

    override suspend fun appRoutingSettings(): AppRoutingSettings =
        decodeRoutingSettings(call("per-app-routing-status", "{}"))

    override suspend fun updateAppRoutingSettings(mode: String, packageNames: Set<String>): AppRoutingSettings =
        decodeRoutingSettings(
            call("per-app-routing-update", Json.obj("mode" to mode.ifBlank { "all" }, "packages" to packageNames)),
        )

    override suspend fun licensing(operation: String, requestJson: String): Result =
        Result(true, call("license-$operation", requestJson))

    override suspend fun updates(operation: String, requestJson: String): Result =
        Result(true, call("update-$operation", requestJson))

    override val platformName: String = "Android"

    override fun close() = Unit

    private suspend fun call(operation: String, request: String): String = withContext(dispatcher) {
        try {
            AndroidRuntimeFacade.dispatch(operation, request.ifBlank { "{}" })
        } catch (error: CancellationException) {
            throw error
        } catch (error: Throwable) {
            throw PlatformServiceException(error.message ?: "Android platform operation failed", error)
        }
    }

    private fun decodeRoutingSettings(payload: String): AppRoutingSettings {
        val root = Json.parse(payload)
        return AppRoutingSettings(
            mode = root["mode"].text("all"),
            packageNames = root["packages"].elements().map { it.text() }.filter { it.isNotBlank() }.toCollection(LinkedHashSet()),
        )
    }
}
