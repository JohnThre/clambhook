package com.clambhook.android

import android.content.Context
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File

class AndroidConfigStore(context: Context) {
    private val configFile = File(context.applicationContext.filesDir, "clambhook/config.toml")

    val path: String
        get() = configFile.absolutePath

    suspend fun ensureConfig(): String = withContext(Dispatchers.IO) {
        if (!configFile.exists()) {
            configFile.parentFile?.mkdirs()
            configFile.writeText(defaultAndroidConfigToml)
        }
        configFile.absolutePath
    }

    suspend fun readConfig(): String = withContext(Dispatchers.IO) {
        ensureConfig()
        configFile.readText()
    }

    suspend fun saveConfig(toml: String): String = withContext(Dispatchers.IO) {
        configFile.parentFile?.mkdirs()
        configFile.writeText(toml)
        configFile.absolutePath
    }
}

const val defaultAndroidConfigToml = """active = "default"

[[profile]]
name = "default"

[profile.listen]
socks5 = "127.0.0.1:1080"
http = "127.0.0.1:8080"

# Add a chain and server before connecting. Example:
#
# [[profile.chain]]
# name = "default"
#
# [[profile.chain.server]]
# name = "example"
# address = "proxy.example:8388"
# protocol = "shadowsocks"
#
# [profile.chain.server.settings]
# method = "chacha20-ietf-poly1305"
# password = "replace-me"
"""
