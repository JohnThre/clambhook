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

[developer]
enabled = true
capture_limit = 200
body_limit_bytes = 0
header_value_limit_bytes = 8192
redact_headers = ["authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key", "x-auth-token", "x-csrf-token", "x-xsrf-token", "csrf-token", "xsrf-token"]
redact_query_params = ["token", "access_token", "refresh_token", "id_token", "api_key", "apikey", "key", "secret", "password", "passwd", "code", "session", "auth"]

[traffic]
enabled = true
history_limit = 500
history_max_age = "168h"
history_path = "traffic-history.json"

[[profile]]
name = "default"

[profile.listen]
socks5 = "127.0.0.1:1080"
http = "127.0.0.1:8080"

# A valid direct chain keeps the first launch usable. Replace this server with
# a configured proxy when you are ready to route traffic remotely.
[[profile.chain]]
name = "default"

[[profile.chain.server]]
name = "direct"
protocol = "direct"
"""
