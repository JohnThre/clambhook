package com.clambhook.android

import android.content.Context
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File

interface ConfigValidator {
    suspend fun validate(configToml: String)
}

class AndroidConfigValidator(
    context: Context,
    runtime: EmbeddedClambhookRuntime = GomobileClambhookRuntime
) : ConfigValidator {
    private val delegate = RuntimeConfigValidator(
        runtime = runtime,
        tempDir = File(context.applicationContext.cacheDir, "clambhook-validation")
    )

    override suspend fun validate(configToml: String) {
        delegate.validate(configToml)
    }
}

class RuntimeConfigValidator(
    private val runtime: EmbeddedClambhookRuntime,
    private val tempDir: File
) : ConfigValidator {
    override suspend fun validate(configToml: String) = withContext(Dispatchers.IO) {
        tempDir.mkdirs()
        val tempFile = File.createTempFile("config-", ".toml", tempDir)
        try {
            tempFile.writeText(configToml)
            runtime.validateConfig(tempFile.absolutePath)
        } finally {
            tempFile.delete()
        }
    }
}
