// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.runtime

/** Creates the GNU/Linux client for the local daemon from the environment. */
object DesktopRuntime {
    fun create(): RuntimeClient = DefaultRuntimeClient(
        HttpBackend(
            setting("CLAMBHOOK_API_URL", "clambhook.apiUrl", HttpBackend.DEFAULT_URL),
            setting("CLAMBHOOK_API_TOKEN", "clambhook.apiToken", ""),
        ),
    )

    internal fun setting(environmentName: String, propertyName: String, fallback: String): String {
        System.getProperty(propertyName, "").trim().let { if (it.isNotEmpty()) return it }
        return System.getenv(environmentName)?.trim()?.takeIf { it.isNotEmpty() } ?: fallback
    }
}
