// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.core.app.NotificationCompat
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import java.io.ByteArrayOutputStream
import java.io.File
import java.net.URI
import java.util.concurrent.atomic.AtomicBoolean
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.flow.first
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonArray
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.intOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import kotlinx.serialization.json.put

/**
 * Java-facing process boundary used by the Gluon native image. The VPN service
 * remains the sole owner of the C runtime; this facade only attaches to the
 * published session and therefore never destroys it when JavaFX closes.
 */
object GluonPlatformFacade {
    private val licenseStarted = AtomicBoolean(false)
    private val context: Context get() = AndroidPlatformEnvironment.context()
    private val configStore: AndroidConfigStore by lazy { AndroidConfigStore(context) }
    private val licenseManager: LicenseManager by lazy { LicenseManager(context) }
    private val updateManager: UpdateManager by lazy {
        UpdateManager(context) { published -> licenseManager.canInstallUpdate(published) }
    }
    private val routingSettings: AppRoutingSettingsStore by lazy {
        DataStoreAppRoutingSettingsStore(context)
    }
    private val securePreferences by lazy {
        EncryptedSharedPreferences.create(
            context,
            "clambhook_platform_services",
            MasterKey.Builder(context).setKeyScheme(MasterKey.KeyScheme.AES256_GCM).build(),
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
        )
    }

    @JvmStatic
    fun request(method: String, path: String, body: String): String {
        val verb = method.trim().uppercase()
        val uri = Uri.parse(path)
        val route = uri.path.orEmpty()
        val requestBody = body.ifBlank { "{}" }
        val runtime = ClambhookTunnelSession.runtime.value

        if (verb == "GET") {
            when (route) {
                "/api/v1/status" -> return runtime?.query("status") ?: offlineStatus()
                "/api/v1/profiles" -> return configuredQuery("profiles", "{}")
                "/api/v1/servers" -> return configuredQuery("servers", profileRequest(uri))
                "/api/v1/rules" -> return configuredQuery("rules", profileRequest(uri))
                "/api/v1/traffic" -> return runtime?.query("traffic_filter", trafficRequest(uri))
                    ?: emptyTraffic()
                "/api/v1/decisions" -> return runtime?.query("decisions", trafficRequest(uri))
                    ?: "{\"decisions\":[]}"
                "/api/v1/events/snapshot" -> return runtime?.query("events", eventRequest(uri))
                    ?: "{\"events\":[],\"complete\":true,\"next_sequence\":0}"
                "/api/v1/rules/temporary" -> return runtime?.query("temporary_rules", profileRequest(uri))
                    ?: "{\"rules\":[]}"
                "/api/v1/policy-groups" -> return runtime?.query(
                    "policy_groups",
                    profileRequest(uri),
                ) ?: configuredQuery("policy_groups", profileRequest(uri))
                "/api/v1/rule-sets" -> return configuredQuery("rule_sets", profileRequest(uri))
                "/api/v1/rule-subscriptions" -> return configuredQuery(
                    "rule_subscriptions",
                    profileRequest(uri),
                )
                "/api/v1/dns" -> return configuredQuery("dns", profileRequest(uri))
                "/api/v1/config/settings" -> return configuredQuery(
                    "config_settings",
                    profileRequest(uri),
                )
                "/api/v1/conditioner" -> return configuredQuery("conditioner", profileRequest(uri))
                "/api/v1/developer/status" -> return runtime?.query("developer_status")
                    ?: "{\"enabled\":false,\"capture_count\":0}"
                "/api/v1/developer/entries" -> return runtime?.query(
                    "developer_entries",
                    developerEntryRequest(uri),
                ) ?: "{\"entries\":[]}"
                "/api/v1/developer/har" -> return runtime?.query("developer_har")
                    ?: "{\"log\":{\"version\":\"1.2\",\"entries\":[]}}"
                "/api/v1/developer/settings" -> return configuredQuery("developer_settings", "{}")
                "/api/v1/developer/map-rules" -> return configuredQuery(
                    "developer_map_rules",
                    profileRequest(uri),
                )
                "/api/v1/developer/breakpoint-rules" -> return configuredQuery(
                    "developer_breakpoint_rules",
                    profileRequest(uri),
                )
                "/api/v1/developer/rewrite-rules" -> return configuredQuery(
                    "developer_rewrite_rules",
                    profileRequest(uri),
                )
                "/api/v1/prompts/pending" -> return runtime?.query("pending_prompts")
                    ?: "{\"prompts\":[]}"
                "/api/v1/prompts/decisions" -> return runtime?.query("silent_decisions")
                    ?: "{\"decisions\":[]}"
                "/api/v1/config/export" -> return runtime?.query("config_export")
                    ?: runBlocking { configStore.readConfig() }
            }
            developerEntryRoute(runtime, route)?.let { return it }
        }

        if (verb == "POST" && route == "/api/v1/connect") {
            dispatch("vpn-start", requestBody)
            return "{\"accepted\":true}"
        }
        if (verb == "POST" && route == "/api/v1/disconnect") {
            dispatch("vpn-stop", requestBody)
            return "{\"accepted\":true}"
        }

        if (verb == "POST" && route == "/api/v1/policy-groups/test") {
            return requireRuntime().mutate("test_policy_groups", requestBody)
        }

        val queryOperation = when {
            verb == "POST" && route == "/api/v1/developer/curl/import" -> "developer_curl_import"
            else -> null
        }
        if (queryOperation != null) return requireRuntime().query(queryOperation, requestBody)

        if (verb == "POST" &&
            (route == "/api/v1/rules/test" || route == "/api/v1/routes/explain")) {
            return configuredQuery("test_rule", requestBody)
        }

        if (verb == "POST" && route in setOf("/api/v1/developer/send", "/api/v1/developer/repeat")) {
            return requireRuntime().developerRequest(route.endsWith("repeat"), requestBody)
        }

        if (verb == "POST" && route == "/api/v1/config/import") {
            val path = runBlocking { configStore.ensureConfig() }
            val response = NativeClambhookConfigBridge.importConfig(path, body)
            ClambhookTunnelSession.runtime.value?.reload(path)
            return response
        }

        if (verb == "PUT" && route == "/api/v1/profiles/active") {
            val path = runBlocking { configStore.ensureConfig() }
            val response = NativeClambhookConfigBridge.setActiveProfile(path, requestBody)
            ClambhookTunnelSession.runtime.value?.reload(path)
            return response
        }

        val mutation = mutationFor(verb, route)
        if (mutation != null) {
            val payload = when {
                route.startsWith("/api/v1/prompts/decisions/") ->
                    withPathId(requestBody, route, "/api/v1/prompts/decisions/", "/promote")
                route.startsWith("/api/v1/prompts/") ->
                    withPathId(requestBody, route, "/api/v1/prompts/", "/resolve")
                route.startsWith("/api/v1/rules/temporary/") ->
                    withPathId(requestBody, route, "/api/v1/rules/temporary/", "")
                route.startsWith("/api/v1/developer/map-rules/") ->
                    withPathId(requestBody, route, "/api/v1/developer/map-rules/", "")
                route.startsWith("/api/v1/developer/breakpoint-rules/") ->
                    withPathId(requestBody, route, "/api/v1/developer/breakpoint-rules/", "")
                route.startsWith("/api/v1/developer/rewrite-rules/") ->
                    withPathId(requestBody, route, "/api/v1/developer/rewrite-rules/", "")
                else -> requestBody
            }
            responseOperationFor(mutation)?.let { responseOperation ->
                return configuredMutation(mutation, responseOperation, payload)
            }
            return requireRuntime().mutate(mutation, payload)
        }
        error("unsupported runtime route $verb $route")
    }

    @JvmStatic
    fun dispatch(operation: String, requestJson: String): String {
        val request = parseObject(requestJson)
        return when (operation) {
            "vpn-consent" -> buildJsonObject {
                put("granted", VpnConsentCoordinator.request(context))
            }.toString()
            "vpn-start" -> {
                ensureLicenseStarted()
                check(licenseManager.state.value.decision.canUseApp) {
                    "ClambHook requires an active trial or license"
                }
                runBlocking { configStore.ensureConfig() }
                ClambhookTunnelController.start(context)
                "{\"accepted\":true}"
            }
            "vpn-stop" -> {
                ClambhookTunnelController.stop(context)
                "{\"accepted\":true}"
            }
            "file-read" -> readPlatformFile(request)
            "file-write" -> writePlatformFile(request)
            "qr-scan" -> QrScanCoordinator.request(context)
            "qr-share" -> {
                QrShare.share(context, request.string("value"))
                "{}"
            }
            "secure-read" -> securePreferences.getString(request.string("key").safeStorageKey(), "").orEmpty()
            "secure-write" -> {
                securePreferences.edit()
                    .putString(request.string("key").safeStorageKey(), request.string("value"))
                    .apply()
                "{}"
            }
            "secure-delete" -> {
                securePreferences.edit().remove(request.string("key").safeStorageKey()).apply()
                "{}"
            }
            "clipboard-read" -> context.getSystemService(ClipboardManager::class.java)
                .primaryClip?.getItemAt(0)?.coerceToText(context)?.toString().orEmpty()
            "clipboard-write" -> {
                context.getSystemService(ClipboardManager::class.java).setPrimaryClip(
                    ClipData.newPlainText("ClambHook", request.string("value")),
                )
                "{}"
            }
            "browser-open" -> {
                openBrowser(request.string("uri"))
                "{}"
            }
            "notify" -> {
                showNotification(request.string("title"), request.string("body"))
                "{}"
            }
            "installed-apps" -> buildJsonObject {
                put("applications", buildJsonArray {
                    runBlocking { InstalledAppInventory.load(context) }.forEach { application ->
                        add(buildJsonObject {
                            put("package_name", application.packageName)
                            put("label", application.label)
                            put("system", application.isSystem)
                        })
                    }
                })
            }.toString()
            "per-app-routing-status" -> routingStatus()
            "per-app-routing-update" -> {
                val packages = request["packages"]?.jsonArray
                    ?.mapNotNull { it.jsonPrimitive.contentOrNull }
                    ?.toSet()
                    ?: emptySet()
                runBlocking {
                    routingSettings.save(
                        AppRoutingSettings(request.string("mode"), packages),
                    )
                }
                routingStatus()
            }
            "license-status" -> licenseStatus()
            "license-activate" -> {
                ensureLicenseStarted()
                runBlocking { licenseManager.activate(request.string("license_key"), request.string("email")) }
                licenseStatus()
            }
            "license-deactivate" -> {
                ensureLicenseStarted()
                runBlocking { licenseManager.deactivateCurrentDevice() }
                licenseStatus()
            }
            "license-reactivate" -> {
                ensureLicenseStarted()
                runBlocking { licenseManager.reactivateCurrentDevice() }
                licenseStatus()
            }
            "license-transfer" -> {
                ensureLicenseStarted()
                runBlocking { licenseManager.transferCurrentDevice() }
                licenseStatus()
            }
            "update-check" -> {
                ensureLicenseStarted()
                runBlocking { updateManager.check() }
                updateStatus()
            }
            "update-install" -> {
                ensureLicenseStarted()
                runBlocking { updateManager.downloadAndInstall() }
                updateStatus()
            }
            else -> error("unsupported Android platform operation $operation")
        }
    }

    private fun configuredQuery(operation: String, request: String): String {
        val path = runBlocking { configStore.ensureConfig() }
        return NativeClambhookConfigBridge.query(path, operation, request)
    }

    private fun configuredMutation(
        mutation: String,
        responseOperation: String,
        request: String,
    ): String {
        val path = runBlocking { configStore.ensureConfig() }
        val response = NativeClambhookConfigBridge.mutate(
            path,
            mutation,
            responseOperation,
            request,
        )
        ClambhookTunnelSession.runtime.value?.reload(path)
        return response
    }

    private fun requireRuntime(): ClambhookTunnelRuntime =
        ClambhookTunnelSession.runtime.value ?: error("VPN runtime is not connected")

    private fun offlineStatus(): String {
        val profiles = parseObject(configuredQuery("profiles", "{}"))
        return buildJsonObject {
            put("running", false)
            put("profile", profiles.string("active").ifBlank { "default" })
            put("listeners", JsonArray(emptyList()))
            put("dns", buildJsonObject { put("enabled", false) })
        }.toString()
    }

    private fun emptyTraffic(): String =
        "{\"summary\":{\"active_connections\":0,\"rx_bps\":0,\"tx_bps\":0,\"rx_total\":0,\"tx_total\":0},\"connections\":[]}"

    private fun profileRequest(uri: Uri): String = buildJsonObject {
        uri.getQueryParameter("profile")?.takeIf { it.isNotBlank() }?.let { put("profile", it) }
    }.toString()

    private fun trafficRequest(uri: Uri): String = buildJsonObject {
        listOf("profile", "query", "state", "network", "rule", "chain", "application")
            .forEach { key -> uri.getQueryParameter(key)?.takeIf { it.isNotBlank() }?.let { put(key, it) } }
        uri.getQueryParameter("after")?.toLongOrNull()?.let { put("after", it) }
        uri.getQueryParameter("limit")?.toLongOrNull()?.let { put("limit", it) }
    }.toString()

    private fun eventRequest(uri: Uri): String = buildJsonObject {
        uri.getQueryParameter("after")?.toLongOrNull()?.let { put("after_sequence", it) }
        uri.getQueryParameter("limit")?.toLongOrNull()?.let { put("limit", it) }
        val types = uri.getQueryParameters("types").flatMap { it.split(',') }.filter { it.isNotBlank() }
        val ids = uri.getQueryParameters("conn_id").flatMap { it.split(',') }.filter { it.isNotBlank() }
        if (types.isNotEmpty()) put("types", buildJsonArray { types.forEach { add(JsonPrimitive(it)) } })
        if (ids.isNotEmpty()) put("conn_ids", buildJsonArray { ids.forEach { add(JsonPrimitive(it)) } })
    }.toString()

    private fun developerEntryRequest(uri: Uri): String = buildJsonObject {
        uri.getQueryParameter("method")?.let { method ->
            put("methods", buildJsonArray { add(JsonPrimitive(method)) })
        }
        listOf("host", "scheme", "content_type").forEach { key ->
            uri.getQueryParameter(key)?.let { put(key, it) }
        }
        uri.getQueryParameter("q")?.let { put("query", it) }
        listOf("status_min", "status_max", "limit").forEach { key ->
            uri.getQueryParameter(key)?.toLongOrNull()?.let { put(key, it) }
        }
        uri.getQueryParameter("error_only")?.toBooleanStrictOrNull()?.let { put("error_only", it) }
    }.toString()

    private fun developerEntryRoute(runtime: ClambhookTunnelRuntime?, route: String): String? {
        val prefix = "/api/v1/developer/entries/"
        if (!route.startsWith(prefix) || runtime == null) return null
        val curl = route.endsWith("/curl")
        val id = route.removePrefix(prefix).removeSuffix(if (curl) "/curl" else "")
        if (id.isBlank() || id.contains('/')) return null
        return runtime.query(
            if (curl) "developer_entry_curl" else "developer_entry",
            buildJsonObject { put("id", Uri.decode(id)) }.toString(),
        )
    }

    private fun mutationFor(method: String, route: String): String? = when {
        method == "PUT" && route == "/api/v1/dns" -> "update_dns"
        method == "PUT" && route == "/api/v1/config/settings" -> "update_config_settings"
        method == "PUT" && route == "/api/v1/conditioner" -> "update_conditioner"
        method == "DELETE" && route == "/api/v1/developer/entries" -> "clear_developer_entries"
        method == "PUT" && route == "/api/v1/rules" -> "replace_rules"
        method == "POST" && route == "/api/v1/rules" -> "create_rule"
        method == "PUT" && route == "/api/v1/policy-groups" -> "replace_policy_groups"
        method == "PUT" && route == "/api/v1/rule-sets" -> "replace_rule_sets"
        method == "PUT" && route == "/api/v1/rule-subscriptions" -> "replace_rule_subscriptions"
        method == "PUT" && route == "/api/v1/policy-groups/selection" -> "select_policy_group"
        method == "PUT" && route == "/api/v1/developer/settings" -> "update_developer_settings"
        method == "PUT" && route == "/api/v1/developer/map-rules" -> "replace_developer_map_rules"
        method == "PUT" && route == "/api/v1/developer/breakpoint-rules" -> "replace_developer_breakpoint_rules"
        method == "PUT" && route == "/api/v1/developer/rewrite-rules" -> "replace_developer_rewrite_rules"
        method == "DELETE" && route.startsWith("/api/v1/developer/map-rules/") -> "delete_developer_map_rule"
        method == "DELETE" && route.startsWith("/api/v1/developer/breakpoint-rules/") -> "delete_developer_breakpoint_rule"
        method == "DELETE" && route.startsWith("/api/v1/developer/rewrite-rules/") -> "delete_developer_rewrite_rule"
        method == "POST" && route == "/api/v1/rule-sets/refresh" -> "refresh_rule_sets"
        method == "POST" && route == "/api/v1/rule-subscriptions/refresh" -> "refresh_rule_subscriptions"
        method == "POST" && route == "/api/v1/rules/from-connection" -> "create_rule_from_connection"
        method == "POST" && route == "/api/v1/rules/temporary/from-connection" -> "create_temporary_rule_from_connection"
        method == "POST" && route.startsWith("/api/v1/prompts/decisions/") -> "promote_silent_decision"
        method == "POST" && route.startsWith("/api/v1/prompts/") -> "resolve_prompt"
        method == "POST" && route == "/api/v1/rules/cleanup" -> "cleanup_rule_from_traffic"
        method == "DELETE" && route.startsWith("/api/v1/rules/temporary/") -> "remove_temporary_rule"
        method == "PUT" && route == "/api/v1/profiles/active" -> "persist_active_profile"
        else -> null
    }

    private fun responseOperationFor(mutation: String): String? = when (mutation) {
        "update_dns" -> "dns"
        "update_config_settings" -> "config_settings"
        "update_conditioner" -> "conditioner"
        "replace_rules", "create_rule" -> "rules_persistence"
        "replace_policy_groups" -> "policy_groups_persistence"
        "replace_rule_sets" -> "rule_sets_persistence"
        "replace_rule_subscriptions" -> "rule_subscriptions_persistence"
        "select_policy_group" -> "policy_group_selection"
        "update_developer_settings" -> "developer_settings"
        "replace_developer_map_rules",
        "replace_developer_breakpoint_rules",
        "replace_developer_rewrite_rules",
        "delete_developer_map_rule",
        "delete_developer_breakpoint_rule",
        "delete_developer_rewrite_rule" -> "developer_persistence"
        else -> null
    }

    private fun withPathId(body: String, route: String, prefix: String, suffix: String): String {
        val raw = route.removePrefix(prefix).removeSuffix(suffix)
        check(raw.isNotBlank() && !raw.contains('/')) { "invalid route identifier" }
        val source = parseObject(body)
        return buildJsonObject {
            source.forEach { (key, value) -> put(key, value) }
            put("id", Uri.decode(raw))
        }.toString()
    }

    private fun readPlatformFile(request: JsonObject): String {
        val file = safePlatformFile(request.string("path"))
        val limit = request["maximum_bytes"]?.jsonPrimitive?.intOrNull?.coerceIn(1, 8 * 1024 * 1024)
            ?: 8 * 1024 * 1024
        file.inputStream().use { input ->
            val output = ByteArrayOutputStream(minOf(limit, 16 * 1024))
            val buffer = ByteArray(16 * 1024)
            var total = 0
            while (true) {
                val count = input.read(buffer)
                if (count < 0) break
                total += count
                check(total <= limit) { "file exceeds safety limit" }
                output.write(buffer, 0, count)
            }
            return output.toByteArray().decodeToString()
        }
    }

    private fun writePlatformFile(request: JsonObject): String {
        val file = safePlatformFile(request.string("path"))
        file.parentFile?.mkdirs()
        file.writeText(request.string("value"))
        return "{}"
    }

    private fun safePlatformFile(path: String): File {
        val file = File(path).canonicalFile
        val roots = listOf(context.filesDir.canonicalFile, context.cacheDir.canonicalFile)
        check(roots.any { file.path == it.path || file.path.startsWith(it.path + File.separator) }) {
            "file path must remain inside ClambHook storage"
        }
        return file
    }

    private fun String.safeStorageKey(): String {
        val value = trim()
        check(value.matches(Regex("[A-Za-z0-9._-]{1,128}"))) { "secure-storage key is invalid" }
        return value
    }

    private fun openBrowser(raw: String) {
        val target = URI(raw.trim())
        check(target.scheme.equals("https", true) || target.scheme.equals("http", true)) {
            "browser URI must use HTTP or HTTPS"
        }
        context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(target.toString())).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }

    private fun showNotification(title: String, body: String) {
        val manager = context.getSystemService(NotificationManager::class.java)
        val channel = "clambhook_ui"
        if (manager.getNotificationChannel(channel) == null) {
            manager.createNotificationChannel(
                NotificationChannel(channel, "ClambHook", NotificationManager.IMPORTANCE_DEFAULT),
            )
        }
        val launch = context.packageManager.getLaunchIntentForPackage(context.packageName)
        val contentIntent = launch?.let {
            PendingIntent.getActivity(
                context,
                0,
                it,
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
            )
        }
        manager.notify(
            1003,
            NotificationCompat.Builder(context, channel)
                .setSmallIcon(R.drawable.ic_stat_clambhook)
                .setContentTitle(title.ifBlank { "ClambHook" })
                .setContentText(body)
                .setContentIntent(contentIntent)
                .setAutoCancel(true)
                .build(),
        )
    }

    private fun ensureLicenseStarted() {
        if (licenseStarted.compareAndSet(false, true)) runBlocking { licenseManager.start() }
    }

    private fun licenseStatus(): String {
        ensureLicenseStarted()
        val state = licenseManager.state.value
        return buildJsonObject {
            put("status", ApiJson.encodeToJsonElement(LicenseStatus.serializer(), state.status))
            put("device_state", ApiJson.encodeToJsonElement(LicenseDeviceState.serializer(), state.deviceState))
            put("has_license_key", state.hasLicenseKey)
            put("email", state.email)
            put("loading", state.loading)
            put("message", state.message)
            put("initialized", state.initialized)
            put("buy_url", licenseManager.buyUrl)
            put("portal_url", licenseManager.portalUrl)
        }.toString()
    }

    private fun updateStatus(): String {
        val state = updateManager.state.value
        return buildJsonObject {
            put("checking", state.checking)
            put("downloading", state.downloading)
            put("up_to_date", state.upToDate)
            put("message", state.message)
            state.available?.let { available ->
                put("available", buildJsonObject {
                    put("manifest", ApiJson.encodeToJsonElement(AndroidUpdateManifest.serializer(), available.manifest))
                    put("published_at_millis", available.publishedAtMillis)
                    put("installable", available.installable)
                })
            }
        }.toString()
    }

    private fun routingStatus(): String {
        val settings = runBlocking { routingSettings.settings.first() }
        return buildJsonObject {
            put("mode", settings.normalizedMode)
            put("packages", buildJsonArray {
                settings.normalizedPackages.forEach { add(JsonPrimitive(it)) }
            })
        }.toString()
    }

    private fun parseObject(value: String): JsonObject =
        ApiJson.parseToJsonElement(value.ifBlank { "{}" }).jsonObject

    private fun JsonObject.string(key: String): String =
        this[key]?.jsonPrimitive?.contentOrNull.orEmpty()
}
