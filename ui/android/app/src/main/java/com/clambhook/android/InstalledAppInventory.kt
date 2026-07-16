package com.clambhook.android

import android.content.Context
import android.content.pm.ApplicationInfo
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** A user-selectable installed application for per-app VPN routing. */
data class InstalledApp(
    val packageName: String,
    val label: String,
    val isSystem: Boolean,
)

/** Enumerates installed apps for the per-app split-tunnel picker. */
object InstalledAppInventory {
    suspend fun load(context: Context): List<InstalledApp> = withContext(Dispatchers.IO) {
        val pm = context.applicationContext.packageManager
        val self = context.applicationContext.packageName
        pm.getInstalledApplications(0)
            .asSequence()
            .filter { it.packageName != self }
            .map { info ->
                InstalledApp(
                    packageName = info.packageName,
                    label = runCatching { pm.getApplicationLabel(info).toString() }
                        .getOrDefault(info.packageName)
                        .ifBlank { info.packageName },
                    isSystem = (info.flags and ApplicationInfo.FLAG_SYSTEM) != 0,
                )
            }
            .sortedWith(compareBy({ it.isSystem }, { it.label.lowercase() }))
            .toList()
    }
}
