// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.linux.settings

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.Paths

const val MIN_LOG_RETENTION = 50
const val MAX_LOG_RETENTION = 500

@Serializable
data class AppSettings(
    @SerialName("apiEndpoint") val apiEndpoint: String = "http://127.0.0.1:9090",
    @SerialName("daemonPath") val daemonPath: String = "",
    @SerialName("configPath") val configPath: String = "",
    @SerialName("launchDaemonOnStart") val launchDaemonOnStart: Boolean = false,
    @SerialName("stopDaemonOnExit") val stopDaemonOnExit: Boolean = true,
    @SerialName("eventStreamEnabled") val eventStreamEnabled: Boolean = true,
    @SerialName("refreshIntervalSeconds") val refreshIntervalSeconds: Int = 5,
    @SerialName("logRetention") val logRetention: Int = 200
)

private val settingsJson = Json {
    ignoreUnknownKeys = true
    encodeDefaults = true
    prettyPrint = true
}

fun AppSettings.normalized(): AppSettings = copy(
    apiEndpoint = if (isSupportedApiEndpoint(apiEndpoint)) normalizeEndpoint(apiEndpoint) else "http://127.0.0.1:9090",
    daemonPath = daemonPath.trim(),
    configPath = configPath.trim(),
    refreshIntervalSeconds = refreshIntervalSeconds.coerceIn(2, 60),
    logRetention = logRetention.coerceIn(MIN_LOG_RETENTION, MAX_LOG_RETENTION)
)

fun isSupportedApiEndpoint(value: String): Boolean {
    val normalized = normalizeEndpoint(value)
    val isHttp = normalized.startsWith("http://") || normalized.startsWith("https://")
    if (!isHttp) return false
    return try {
        val noScheme = normalized.substringAfter("://")
        noScheme.isNotBlank() && noScheme.none { it == '/' || it == '?' || it == '#' }
    } catch (e: Exception) {
        false
    }
}

fun hasApiEndpointPath(value: String): Boolean {
    val normalized = normalizeEndpoint(value)
    return normalized.substringAfter("://", "").let { it.isNotBlank() && it.any { ch -> ch == '/' || ch == '?' || ch == '#' } }
}

private fun normalizeEndpoint(value: String): String {
    val trimmed = value.trim()
    if (trimmed.isEmpty()) return "http://127.0.0.1:9090"
    var result = trimmed
    while (result.endsWith("/")) result = result.dropLast(1)
    return result
}

interface SettingsStore {
    fun load(): AppSettings
    fun save(settings: AppSettings)
    fun current(): AppSettings
}

open class FileSettingsStore(private val path: Path = defaultSettingsPath()) : SettingsStore {
    private var cached: AppSettings = AppSettings()
    override fun load(): AppSettings = try {
        val loaded = if (Files.exists(path)) {
            settingsJson.decodeFromString(AppSettings.serializer(), Files.readString(path)).normalized()
        } else {
            AppSettings()
        }
        cached = loaded
        loaded
    } catch (e: Exception) {
        AppSettings()
    }

    override fun save(settings: AppSettings) {
        val normalized = settings.normalized()
        cached = normalized
        Files.createDirectories(path.parent)
        Files.writeString(path, settingsJson.encodeToString(AppSettings.serializer(), normalized))
    }

    override fun current(): AppSettings = cached

    companion object {
        fun defaultSettingsPath(): Path =
            Paths.get(System.getenv("XDG_CONFIG_HOME") ?: System.getProperty("user.home") + "/.config")
                .resolve("clambhook")
                .resolve("linux-settings.json")
    }
}