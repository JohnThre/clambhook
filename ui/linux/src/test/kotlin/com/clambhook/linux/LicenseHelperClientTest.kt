package com.clambhook.linux.license

import com.clambhook.linux.model.ApiJson
import kotlin.time.Duration.Companion.milliseconds
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class LicenseHelperClientTest {
    @Test
    fun missingOkFieldThrowsClearError() = runTest {
        val script = createScript("""echo '{"result":"x"}'""")
        val helper = LicenseHelperClient(script)
        val ex = runCatching { helper.call("ping", emptyJson()) }.exceptionOrNull()
        assertTrue(ex is IllegalStateException)
        assertTrue(ex?.message?.contains("ping failed", ignoreCase = true) == true)
    }

    @Test
    fun okFalseReportsErrorMessage() = runTest {
        val script = createScript("""echo '{"ok":false,"error":"bad key"}'""")
        val helper = LicenseHelperClient(script)
        val ex = runCatching { helper.call("activate", emptyJson()) }.exceptionOrNull()
        assertTrue(ex is IllegalStateException)
        assertEquals("bad key", ex?.message)
    }

    @Test
    fun okTrueReturnsResult() = runTest {
        val script = createScript("""echo '{"ok":true,"result":"license-data"}'""")
        val helper = LicenseHelperClient(script)
        val result = helper.call("get", emptyJson())
        assertEquals("license-data", result)
    }

    @Test
    fun helperProcessTimesOut() = runTest(timeout = 15_000.milliseconds) {
        val script = createScript("sleep 120")
        val helper = LicenseHelperClient(script)
        val ex = runCatching { helper.call("ping", emptyJson()) }.exceptionOrNull()
        assertTrue(ex is IllegalStateException)
        assertTrue(ex?.message?.contains("timed out", ignoreCase = true) == true)
    }

    private suspend fun createScript(command: String): String = withContext(Dispatchers.IO) {
        val tmp = java.io.File.createTempFile("license-helper-", ".sh")
        tmp.writeText("#!/bin/sh\n$command\n")
        tmp.setExecutable(true)
        tmp.absolutePath
    }

    private fun emptyJson(): JsonObject = buildJsonObject {}
}
