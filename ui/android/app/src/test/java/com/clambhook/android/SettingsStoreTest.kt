package com.clambhook.android

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class SettingsStoreTest {
    @Test
    fun appSettingsNormalizeRefreshAndSplitTunnelValues() {
        val settings = AppSettings(
            refreshIntervalSeconds = 90,
            splitTunnelMode = "unsupported",
            splitTunnelPackages = setOf(" com.example.beta ", "", "com.example.alpha")
        )

        assertEquals(60, settings.normalizedRefreshIntervalSeconds)
        assertEquals(SplitTunnelMode.All, settings.normalizedSplitTunnelMode)
        assertEquals(
            sortedSetOf("com.example.alpha", "com.example.beta"),
            settings.normalizedSplitTunnelPackages
        )
    }

    @Test
    fun fakeStorePersistsSettingsContract() = runBlocking {
        val settingsStore = FakeSettingsStore()

        settingsStore.save(
            AppSettings(
                refreshIntervalSeconds = 1,
                splitTunnelMode = SplitTunnelMode.Include,
                splitTunnelPackages = setOf(" com.example.app ")
            )
        )

        val settings = settingsStore.settings.first()
        assertEquals(2, settings.refreshIntervalSeconds)
        assertEquals(SplitTunnelMode.Include, settings.splitTunnelMode)
        assertEquals(setOf("com.example.app"), settings.splitTunnelPackages)
    }

    @Test
    fun settingsValidationRejectsInvalidEmbeddedInput() {
        val errors = validateSettingsInput(
            refreshSeconds = "1",
            configToml = " "
        )

        assertEquals("Refresh must be 2-60 seconds", errors.refreshSeconds)
        assertEquals("Config TOML is required", errors.configToml)
        assertFalse(errors.isValid)
    }

    @Test
    fun settingsValidationAcceptsEmbeddedInput() {
        val errors = validateSettingsInput(
            refreshSeconds = "5",
            configToml = defaultAndroidConfigToml
        )

        assertTrue(errors.isValid)
    }
}

private class FakeSettingsStore : SettingsStore {
    private val state = MutableStateFlow(AppSettings())
    override val settings: Flow<AppSettings> = state

    override suspend fun save(settings: AppSettings) {
        state.value = AppSettings(
            refreshIntervalSeconds = settings.normalizedRefreshIntervalSeconds,
            splitTunnelMode = settings.normalizedSplitTunnelMode,
            splitTunnelPackages = settings.normalizedSplitTunnelPackages
        )
    }
}
