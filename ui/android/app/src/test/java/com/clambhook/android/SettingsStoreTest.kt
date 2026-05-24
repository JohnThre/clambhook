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
    fun appSettingsNormalizeBaseUrlAndRefreshInterval() {
        val blank = AppSettings(apiBaseUrl = "   ", refreshIntervalSeconds = 1)
        val custom = AppSettings(apiBaseUrl = " http://proxy.example:9090/ ", refreshIntervalSeconds = 90)

        assertEquals(defaultAndroidApiBaseUrl, blank.normalizedBaseUrl)
        assertEquals(2, blank.normalizedRefreshIntervalSeconds)
        assertEquals("http://proxy.example:9090", custom.normalizedBaseUrl)
        assertEquals(60, custom.normalizedRefreshIntervalSeconds)
        assertEquals("127.0.0.1:9090", defaultAndroidApiListenAddress)
    }

    @Test
    fun fakeStoresPersistSettingsAndTokenContract() = runBlocking {
        val settingsStore = FakeSettingsStore()
        val tokenStore = FakeTokenStore()

        settingsStore.save(
            AppSettings(
                apiBaseUrl = " http://proxy.example:9090/ ",
                refreshIntervalSeconds = 1,
                eventStreamEnabled = false,
                embeddedDaemonEnabled = false
            )
        )
        tokenStore.saveToken(" secret-token ")

        val settings = settingsStore.settings.first()
        assertEquals("http://proxy.example:9090", settings.apiBaseUrl)
        assertEquals(2, settings.refreshIntervalSeconds)
        assertFalse(settings.eventStreamEnabled)
        assertFalse(settings.embeddedDaemonEnabled)
        assertEquals("secret-token", tokenStore.currentToken())
        assertEquals("secret-token", tokenStore.token.first())
    }

    @Test
    fun generatedApiTokenIsUrlSafe() {
        val token = generateApiToken()

        assertEquals(43, token.length)
        assertFalse(token.contains("+"))
        assertFalse(token.contains("/"))
        assertFalse(token.contains("="))
    }

    @Test
    fun settingsValidationRejectsInvalidRemoteInput() {
        val errors = validateSettingsInput(
            apiBaseUrl = "ftp://example.com",
            apiToken = " ",
            refreshSeconds = "1",
            embeddedDaemonEnabled = false,
            configToml = " "
        )

        assertEquals("Use http:// or https://", errors.apiBaseUrl)
        assertEquals("Enter a bearer token", errors.apiToken)
        assertEquals("Refresh must be 2-60 seconds", errors.refreshSeconds)
        assertEquals("Config TOML is required", errors.configToml)
        assertFalse(errors.isValid)
    }

    @Test
    fun settingsValidationSkipsBaseUrlForEmbeddedDaemon() {
        val errors = validateSettingsInput(
            apiBaseUrl = "",
            apiToken = "secret",
            refreshSeconds = "5",
            embeddedDaemonEnabled = true,
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
            apiBaseUrl = settings.normalizedBaseUrl,
            refreshIntervalSeconds = settings.normalizedRefreshIntervalSeconds,
            eventStreamEnabled = settings.eventStreamEnabled,
            embeddedDaemonEnabled = settings.embeddedDaemonEnabled
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
