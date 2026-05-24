package com.clambhook.android

import java.net.URI

data class SettingsValidationErrors(
    val apiBaseUrl: String? = null,
    val apiToken: String? = null,
    val refreshSeconds: String? = null,
    val configToml: String? = null
) {
    val isValid: Boolean
        get() = apiBaseUrl == null &&
            apiToken == null &&
            refreshSeconds == null &&
            configToml == null

    val firstMessage: String?
        get() = listOfNotNull(apiBaseUrl, apiToken, refreshSeconds, configToml).firstOrNull()
}

fun validateSettingsInput(
    apiBaseUrl: String,
    apiToken: String,
    refreshSeconds: String,
    embeddedDaemonEnabled: Boolean,
    configToml: String
): SettingsValidationErrors {
    return SettingsValidationErrors(
        apiBaseUrl = if (embeddedDaemonEnabled) null else validateApiBaseUrl(apiBaseUrl),
        apiToken = validateApiToken(apiToken),
        refreshSeconds = validateRefreshSeconds(refreshSeconds),
        configToml = validateConfigToml(configToml)
    )
}

private fun validateApiBaseUrl(value: String): String? {
    val trimmed = value.trim().trimEnd('/')
    if (trimmed.isBlank()) {
        return "Enter a base URL"
    }
    val uri = runCatching { URI(trimmed) }.getOrNull()
        ?: return "Enter a valid URL"
    if (uri.scheme !in setOf("http", "https")) {
        return "Use http:// or https://"
    }
    if (uri.host.isNullOrBlank()) {
        return "Include a host"
    }
    if (!uri.rawQuery.isNullOrBlank() || !uri.rawFragment.isNullOrBlank()) {
        return "Remove query and fragment text"
    }
    return null
}

private fun validateApiToken(value: String): String? {
    return if (value.trim().isBlank()) "Enter a bearer token" else null
}

private fun validateRefreshSeconds(value: String): String? {
    val seconds = value.toIntOrNull() ?: return "Refresh must be a number"
    return if (seconds in 2..60) null else "Refresh must be 2-60 seconds"
}

private fun validateConfigToml(value: String): String? {
    return if (value.trim().isBlank()) "Config TOML is required" else null
}
