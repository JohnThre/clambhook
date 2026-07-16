package com.clambhook.android

import android.content.Context
import androidx.datastore.preferences.core.booleanPreferencesKey
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.intPreferencesKey
import androidx.datastore.preferences.core.stringSetPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.withContext
import java.security.SecureRandom
import java.util.Base64

private val Context.clambhookDataStore by preferencesDataStore(name = "clambhook_settings")

object SplitTunnelMode {
    const val All = "all"
    const val Include = "include"
    const val Exclude = "exclude"

    val supported = setOf(All, Include, Exclude)
}


data class AppSettings(
    val apiBaseUrl: String = defaultAndroidApiBaseUrl,
    val refreshIntervalSeconds: Int = 5,
    val eventStreamEnabled: Boolean = true,
    val embeddedDaemonEnabled: Boolean = true,
    val splitTunnelMode: String = SplitTunnelMode.All,
    val splitTunnelPackages: Set<String> = emptySet()
) {
    val normalizedBaseUrl: String
        get() = apiBaseUrl.trim().trimEnd('/').ifBlank { defaultAndroidApiBaseUrl }

    val normalizedRefreshIntervalSeconds: Int
        get() = refreshIntervalSeconds.coerceIn(2, 60)

    val normalizedSplitTunnelMode: String
        get() = splitTunnelMode.takeIf { it in SplitTunnelMode.supported } ?: SplitTunnelMode.All

    val normalizedSplitTunnelPackages: Set<String>
        get() = splitTunnelPackages.map { it.trim() }.filter { it.isNotBlank() }.toSortedSet()
}

const val defaultAndroidApiListenAddress = "127.0.0.1:9090"
const val defaultAndroidApiBaseUrl = "http://$defaultAndroidApiListenAddress"

interface SettingsStore {
    val settings: Flow<AppSettings>
    suspend fun save(settings: AppSettings)
}

class DataStoreSettingsStore(context: Context) : SettingsStore {
    private val dataStore = context.applicationContext.clambhookDataStore

    override val settings: Flow<AppSettings> = dataStore.data.map { prefs ->
        AppSettings(
            apiBaseUrl = prefs[Keys.apiBaseUrl] ?: defaultAndroidApiBaseUrl,
            refreshIntervalSeconds = prefs[Keys.refreshIntervalSeconds] ?: 5,
            eventStreamEnabled = prefs[Keys.eventStreamEnabled] ?: true,
            embeddedDaemonEnabled = prefs[Keys.embeddedDaemonEnabled] ?: true,
            splitTunnelMode = prefs[Keys.splitTunnelMode] ?: SplitTunnelMode.All,
            splitTunnelPackages = prefs[Keys.splitTunnelPackages] ?: emptySet()
        )
    }

    override suspend fun save(settings: AppSettings) {
        dataStore.edit { prefs ->
            prefs[Keys.apiBaseUrl] = settings.normalizedBaseUrl
            prefs[Keys.refreshIntervalSeconds] = settings.normalizedRefreshIntervalSeconds
            prefs[Keys.eventStreamEnabled] = settings.eventStreamEnabled
            prefs[Keys.embeddedDaemonEnabled] = settings.embeddedDaemonEnabled
            prefs[Keys.splitTunnelMode] = settings.normalizedSplitTunnelMode
            prefs[Keys.splitTunnelPackages] = settings.normalizedSplitTunnelPackages
        }
    }

    private object Keys {
        val apiBaseUrl = stringPreferencesKey("api_base_url")
        val refreshIntervalSeconds = intPreferencesKey("refresh_interval_seconds")
        val eventStreamEnabled = booleanPreferencesKey("event_stream_enabled")
        val embeddedDaemonEnabled = booleanPreferencesKey("embedded_daemon_enabled")
        val splitTunnelMode = stringPreferencesKey("split_tunnel_mode")
        val splitTunnelPackages = stringSetPreferencesKey("split_tunnel_packages")
    }
}

interface TokenStore {
    val token: Flow<String>
    fun currentToken(): String
    suspend fun saveToken(token: String)
}

class EncryptedTokenStore(context: Context) : TokenStore {
    private val prefs = EncryptedSharedPreferences.create(
        context.applicationContext,
        "clambhook_secrets",
        MasterKey.Builder(context.applicationContext)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build(),
        EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
        EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM
    )
    private val initialToken = prefs.getString(apiTokenKey, "").orEmpty().ifBlank {
        generateApiToken().also { generated ->
            prefs.edit().putString(apiTokenKey, generated).apply()
        }
    }
    private val tokenState = MutableStateFlow(initialToken)

    override val token: Flow<String> = tokenState

    override fun currentToken(): String = tokenState.value

    override suspend fun saveToken(token: String) {
        val trimmed = token.trim()
        withContext(Dispatchers.IO) {
            prefs.edit().putString(apiTokenKey, trimmed).apply()
        }
        tokenState.value = trimmed
    }

    private companion object {
        const val apiTokenKey = "api_token"
    }
}

fun generateApiToken(): String {
    val bytes = ByteArray(32)
    SecureRandom().nextBytes(bytes)
    return Base64.getUrlEncoder().withoutPadding().encodeToString(bytes)
}
