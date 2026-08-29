// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Test

class AppRoutingSettingsTest {
    @Test
    fun routingSettingsNormalizeModeAndPackageNames() {
        val settings = AppRoutingSettings(
            mode = "unsupported",
            packages = setOf(" com.example.beta ", "", "bad/package", "com.example.alpha"),
        )

        assertEquals(SplitTunnelMode.All, settings.normalizedMode)
        assertEquals(
            sortedSetOf("com.example.alpha", "com.example.beta"),
            settings.normalizedPackages,
        )
    }

    @Test
    fun storePersistsNormalizedRoutingContract() = runBlocking {
        val store = FakeAppRoutingSettingsStore()
        store.save(
            AppRoutingSettings(
                SplitTunnelMode.Include,
                setOf(" com.example.app "),
            ),
        )

        val settings = store.settings.first()
        assertEquals(SplitTunnelMode.Include, settings.mode)
        assertEquals(setOf("com.example.app"), settings.packages)
    }
}

private class FakeAppRoutingSettingsStore : AppRoutingSettingsStore {
    private val state = MutableStateFlow(AppRoutingSettings())
    override val settings: Flow<AppRoutingSettings> = state

    override suspend fun save(settings: AppRoutingSettings) {
        state.value = AppRoutingSettings(settings.normalizedMode, settings.normalizedPackages)
    }
}
