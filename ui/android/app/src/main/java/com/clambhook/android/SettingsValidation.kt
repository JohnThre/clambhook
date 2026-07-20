package com.clambhook.android

data class SettingsValidationErrors(
    val refreshSeconds: String? = null,
    val configToml: String? = null
) {
    val isValid: Boolean
        get() = refreshSeconds == null && configToml == null

    val firstMessage: String?
        get() = listOfNotNull(refreshSeconds, configToml).firstOrNull()
}

fun validateSettingsInput(
    refreshSeconds: String,
    configToml: String
): SettingsValidationErrors {
    return SettingsValidationErrors(
        refreshSeconds = validateRefreshSeconds(refreshSeconds),
        configToml = validateConfigToml(configToml)
    )
}

private fun validateRefreshSeconds(value: String): String? {
    val seconds = value.toIntOrNull() ?: return "Refresh must be a number"
    return if (seconds in 2..60) null else "Refresh must be 2-60 seconds"
}

private fun validateConfigToml(value: String): String? {
    return if (value.trim().isBlank()) "Config TOML is required" else null
}
