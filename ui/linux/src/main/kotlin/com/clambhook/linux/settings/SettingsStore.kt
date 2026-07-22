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
    return isHttp && apiListenAddress(normalized) != normalized
}

private fun normalizeEndpoint(value: String): String {
    val trimmed = value.trim()
    if (trimmed.isEmpty()) return "http://127.0.0.1:9090"
    var result = trimmed
    while (result.endsWith("/")) result = result.dropLast(1)
    return result
}

private fun apiListenAddress(endpoint: String): String {
    // Parse scheme://host:port -> host:port for the daemon listen address.
    return try {
        val noScheme = endpoint.substringAfter("://")
        noScheme
    } catch (e: Exception) {
        endpoint
    }
}

interface SettingsStore {
    fun load(): AppSettings
    fun save(settings: AppSettings)
}

class FileSettingsStore(private val path: Path = defaultSettingsPath()) : SettingsStore {
    override fun load(): AppSettings = try {
        if (Files.exists(path)) {
            settingsJson.decodeFromString(AppSettings.serializer(), Files.readString(path)).normalized()
        } else {
            AppSettings()
        }
    } catch (e: Exception) {
        AppSettings()
    }

    override fun save(settings: AppSettings) {
        val normalized = settings.normalized()
        Files.createDirectories(path.parent)
        Files.writeString(path, settingsJson.encodeToString(AppSettings.serializer(), normalized))
    }

    companion object {
        fun defaultSettingsPath(): Path =
            Paths.get(System.getenv("XDG_CONFIG_HOME") ?: System.getProperty("user.home") + "/.config")
                .resolve("clambhook")
                .resolve("linux-settings.json")
    }
}