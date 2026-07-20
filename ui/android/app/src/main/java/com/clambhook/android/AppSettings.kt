package com.clambhook.android

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.intPreferencesKey
import androidx.datastore.preferences.core.stringSetPreferencesKey
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

private val Context.clambhookDataStore by preferencesDataStore(name = "clambhook_settings")

object SplitTunnelMode {
    const val All = "all"
    const val Include = "include"
    const val Exclude = "exclude"

    val supported = setOf(All, Include, Exclude)
}

data class AppSettings(
    val refreshIntervalSeconds: Int = 5,
    val splitTunnelMode: String = SplitTunnelMode.All,
    val splitTunnelPackages: Set<String> = emptySet()
) {
    val normalizedRefreshIntervalSeconds: Int
        get() = refreshIntervalSeconds.coerceIn(2, 60)

    val normalizedSplitTunnelMode: String
        get() = splitTunnelMode.takeIf { it in SplitTunnelMode.supported } ?: SplitTunnelMode.All

    val normalizedSplitTunnelPackages: Set<String>
        get() = splitTunnelPackages.map { it.trim() }.filter { it.isNotBlank() }.toSortedSet()
}

interface SettingsStore {
    val settings: Flow<AppSettings>
    suspend fun save(settings: AppSettings)
}

class DataStoreSettingsStore(context: Context) : SettingsStore {
    private val dataStore = context.applicationContext.clambhookDataStore

    override val settings: Flow<AppSettings> = dataStore.data.map { prefs ->
        AppSettings(
            refreshIntervalSeconds = prefs[Keys.refreshIntervalSeconds] ?: 5,
            splitTunnelMode = prefs[Keys.splitTunnelMode] ?: SplitTunnelMode.All,
            splitTunnelPackages = prefs[Keys.splitTunnelPackages] ?: emptySet()
        )
    }

    override suspend fun save(settings: AppSettings) {
        dataStore.edit { prefs ->
            prefs[Keys.refreshIntervalSeconds] = settings.normalizedRefreshIntervalSeconds
            prefs[Keys.splitTunnelMode] = settings.normalizedSplitTunnelMode
            prefs[Keys.splitTunnelPackages] = settings.normalizedSplitTunnelPackages
        }
    }

    private object Keys {
        val refreshIntervalSeconds = intPreferencesKey("refresh_interval_seconds")
        val splitTunnelMode = stringPreferencesKey("split_tunnel_mode")
        val splitTunnelPackages = stringSetPreferencesKey("split_tunnel_packages")
    }
}
