package com.clambhook.android

import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.File

class ConfigValidationTest {
    @get:Rule
    val temporaryFolder = TemporaryFolder()

    @Test
    fun runtimeConfigValidatorWritesConfigToTempFile() = runBlocking {
        val tempDir = temporaryFolder.newFolder()
        val runtime = RecordingRuntime()

        RuntimeConfigValidator(runtime, tempDir).validate("active = \"default\"")

        assertEquals("active = \"default\"", runtime.validatedConfig)
        assertTrue(tempDir.listFiles().orEmpty().isEmpty())
    }

    @Test
    fun runtimeConfigValidatorDeletesTempFileWhenValidationFails() = runBlocking {
        val tempDir = temporaryFolder.newFolder()
        val runtime = RecordingRuntime(error = IllegalArgumentException("bad config"))

        runCatching {
            RuntimeConfigValidator(runtime, tempDir).validate("bad")
        }

        assertEquals("bad", runtime.validatedConfig)
        assertTrue(tempDir.listFiles().orEmpty().isEmpty())
    }
}

private class RecordingRuntime(
    private val error: Throwable? = null
) : ConfigPathValidator {
    var validatedConfig: String = ""

    override fun validateConfig(configPath: String) {
        validatedConfig = File(configPath).readText()
        error?.let { throw it }
    }
}
