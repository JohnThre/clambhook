// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.core.stringSetPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.map

private val Context.clambhookRoutingDataStore by
    preferencesDataStore(name = "clambhook_routing")

object SplitTunnelMode {
    const val All = "all"
    const val Include = "include"
    const val Exclude = "exclude"

    val supported = setOf(All, Include, Exclude)
}

data class AppRoutingSettings(
    val mode: String = SplitTunnelMode.All,
    val packages: Set<String> = emptySet(),
) {
    val normalizedMode: String
        get() = mode.takeIf { it in SplitTunnelMode.supported } ?: SplitTunnelMode.All

    val normalizedPackages: Set<String>
        get() = packages
            .map(String::trim)
            .filter { it.matches(Regex("[A-Za-z0-9_.]{1,255}")) }
            .toSortedSet()
}

interface AppRoutingSettingsStore {
    val settings: Flow<AppRoutingSettings>
    suspend fun save(settings: AppRoutingSettings)
}

class DataStoreAppRoutingSettingsStore(context: Context) : AppRoutingSettingsStore {
    private val dataStore = context.applicationContext.clambhookRoutingDataStore

    override val settings: Flow<AppRoutingSettings> = dataStore.data.map { preferences ->
        AppRoutingSettings(
            mode = preferences[Keys.mode] ?: SplitTunnelMode.All,
            packages = preferences[Keys.packages] ?: emptySet(),
        )
    }

    override suspend fun save(settings: AppRoutingSettings) {
        dataStore.edit { preferences ->
            preferences[Keys.mode] = settings.normalizedMode
            preferences[Keys.packages] = settings.normalizedPackages
        }
    }

    private object Keys {
        val mode = stringPreferencesKey("split_tunnel_mode")
        val packages = stringSetPreferencesKey("split_tunnel_packages")
    }
}
