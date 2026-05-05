package com.clambhook.android

import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test

class SettingsStoreTest {
    @Test
    fun appSettingsNormalizeBaseUrlAndRefreshInterval() {
        val blank = AppSettings(apiBaseUrl = "   ", refreshIntervalSeconds = 1)
        val custom = AppSettings(apiBaseUrl = " http://proxy.example:9090/ ", refreshIntervalSeconds = 90)

        assertEquals(defaultAndroidApiBaseUrl, blank.normalizedBaseUrl)
        assertEquals(2, blank.normalizedRefreshIntervalSeconds)
        assertEquals("http://proxy.example:9090", custom.normalizedBaseUrl)
        assertEquals(60, custom.normalizedRefreshIntervalSeconds)
    }

    @Test
    fun fakeStoresPersistSettingsAndTokenContract() = runBlocking {
        val settingsStore = FakeSettingsStore()
        val tokenStore = FakeTokenStore()

        settingsStore.save(
            AppSettings(
                apiBaseUrl = " http://proxy.example:9090/ ",
                refreshIntervalSeconds = 1,
                eventStreamEnabled = false
            )
        )
        tokenStore.saveToken(" secret-token ")

        val settings = settingsStore.settings.first()
        assertEquals("http://proxy.example:9090", settings.apiBaseUrl)
        assertEquals(2, settings.refreshIntervalSeconds)
        assertFalse(settings.eventStreamEnabled)
        assertEquals("secret-token", tokenStore.currentToken())
        assertEquals("secret-token", tokenStore.token.first())
    }
}

private class FakeSettingsStore : SettingsStore {
    private val state = MutableStateFlow(AppSettings())
    override val settings: Flow<AppSettings> = state

    override suspend fun save(settings: AppSettings) {
        state.value = AppSettings(
            apiBaseUrl = settings.normalizedBaseUrl,
            refreshIntervalSeconds = settings.normalizedRefreshIntervalSeconds,
            eventStreamEnabled = settings.eventStreamEnabled
        )
    }
}

private class FakeTokenStore : TokenStore {
    private val state = MutableStateFlow("")
    override val token: Flow<String> = state

    override fun currentToken(): String = state.value

    override suspend fun saveToken(token: String) {
        state.value = token.trim()
    }
}
