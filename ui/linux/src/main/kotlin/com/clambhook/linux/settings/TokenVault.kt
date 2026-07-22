package com.clambhook.linux.settings

import java.io.File

/**
 * Token vault abstraction. The real implementation stores the API bearer token
 * via the platform Secret Service (libsecret/GNOME Keyring/KWallet) through the
 * `secret-tool` CLI; a memory implementation is used in tests.
 */
interface TokenVault {
    suspend fun readToken(): String
    suspend fun saveToken(token: String)
}

class SecretTokenVault : TokenVault {
    override suspend fun readToken(): String = readSecret()

    override suspend fun saveToken(token: String) {
        val trimmed = token.trim()
        if (trimmed.isEmpty()) clearSecret() else storeSecret(trimmed)
    }

    private fun storeSecret(value: String) {
        try {
            val process = ProcessBuilder(
                "secret-tool", "store",
                "--label", "clambhook API token",
                "account", ACCOUNT,
                SCHEMA_NAME
            ).start()
            process.outputStream.use { it.write(value.toByteArray()); it.flush() }
            process.waitFor()
        } catch (e: Exception) {
            // libsecret/secret-tool unavailable — silently degrade.
        }
    }

    private fun readSecret(): String = try {
        val process = ProcessBuilder("secret-tool", "lookup", "account", ACCOUNT, SCHEMA_NAME)
            .redirectError(ProcessBuilder.Redirect.DISCARD)
            .start()
        val result = process.inputStream.bufferedReader().readText().trim()
        process.waitFor()
        result
    } catch (e: Exception) {
        ""
    }

    private fun clearSecret() {
        try {
            ProcessBuilder("secret-tool", "clear", "account", ACCOUNT, SCHEMA_NAME)
                .redirectError(ProcessBuilder.Redirect.DISCARD)
                .start().waitFor()
        } catch (e: Exception) {
        }
    }

    companion object {
        private const val SCHEMA_NAME = "com.clambhook.Clambhook.ApiToken"
        private const val ACCOUNT = "default"
    }
}

class MemoryTokenVault(private var token: String = "") : TokenVault {
    override suspend fun readToken(): String = token
    override suspend fun saveToken(token: String) { this.token = token.trim() }
}