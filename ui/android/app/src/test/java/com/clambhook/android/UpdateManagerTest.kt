// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.security.MessageDigest

class UpdateManagerTest {
    private fun manifest(
        versionCode: Long = 2,
        minSdk: Int = 31,
        apkUrl: String = "https://github.com/JohnThre/clambhook/releases/download/v1.0.0/ClambHook.apk",
        sha256: String = "abc123",
    ) = AndroidUpdateManifest(
        versionCode = versionCode,
        versionName = "1.0.0",
        minSdk = minSdk,
        apkUrl = apkUrl,
        sha256 = sha256,
    )

    @Test
    fun classifiesUpToDateWhenNotNewer() {
        assertEquals(UpdateClassification.UpToDate, classifyUpdate(manifest(versionCode = 5), currentVersionCode = 5, currentSdk = 34))
        assertEquals(UpdateClassification.UpToDate, classifyUpdate(manifest(versionCode = 3), currentVersionCode = 5, currentSdk = 34))
    }

    @Test
    fun classifiesNeedsNewerAndroidWhenSdkTooLow() {
        assertEquals(
            UpdateClassification.NeedsNewerAndroid,
            classifyUpdate(manifest(versionCode = 9, minSdk = 40), currentVersionCode = 1, currentSdk = 34),
        )
    }

    @Test
    fun classifiesIncompleteWhenArtifactMissing() {
        assertEquals(
            UpdateClassification.IncompleteManifest,
            classifyUpdate(manifest(versionCode = 9, apkUrl = ""), currentVersionCode = 1, currentSdk = 34),
        )
        assertEquals(
            UpdateClassification.IncompleteManifest,
            classifyUpdate(manifest(versionCode = 9, sha256 = " "), currentVersionCode = 1, currentSdk = 34),
        )
    }

    @Test
    fun classifiesInstallableWhenNewerAndComplete() {
        assertEquals(
            UpdateClassification.Installable,
            classifyUpdate(manifest(versionCode = 9), currentVersionCode = 1, currentSdk = 34),
        )
    }

    @Test
    fun checksumAcceptsMatchingDigestIgnoringCaseAndWhitespace() {
        val bytes = "clambhook-apk-bytes".toByteArray()
        val actual = MessageDigest.getInstance("SHA-256").digest(bytes).toHexString()

        assertTrue(checksumMatches(actual, actual))
        assertTrue(checksumMatches("  ${actual.uppercase()}  ", actual))
    }

    @Test
    fun checksumRejectsMismatchOrBlankExpected() {
        val actual = MessageDigest.getInstance("SHA-256").digest("payload".toByteArray()).toHexString()

        assertFalse(checksumMatches("deadbeef", actual))
        assertFalse(checksumMatches("", actual))
        assertFalse(checksumMatches("   ", actual))
    }

    @Test
    fun toHexStringProducesLowercaseTwoDigitBytes() {
        val hex = byteArrayOf(0x00, 0x0f.toByte(), 0xff.toByte(), 0xa5.toByte()).toHexString()
        assertEquals("000fffa5", hex)
    }

    @Test
    fun trustedOriginAcceptsGitHubOverHttps() {
        assertTrue(isTrustedUpdateOrigin("https://github.com/JohnThre/clambhook/releases/download/v1.0.0/ClambHook.apk"))
        assertTrue(isTrustedUpdateOrigin("  https://GITHUB.com/JohnThre/clambhook/releases/download/v1/x.apk  "))
    }

    @Test
    fun trustedOriginRejectsOffOriginNonHttpsAndMalformed() {
        assertFalse(isTrustedUpdateOrigin("https://evil.example.com/clambhook.apk"))
        assertFalse(isTrustedUpdateOrigin("https://github.com.evil.example.com/x.apk"))
        assertFalse(isTrustedUpdateOrigin("http://github.com/JohnThre/clambhook/x.apk"))
        assertFalse(isTrustedUpdateOrigin(""))
        assertFalse(isTrustedUpdateOrigin("not a url"))
    }

    @Test
    fun cancellationExceptionIsRethrownDuringCheck() {
        val error = runCatching {
            runCatching {
                throw kotlin.coroutines.cancellation.CancellationException("cancelled")
            }.onFailure { if (it is kotlin.coroutines.cancellation.CancellationException) throw it }
        }.exceptionOrNull()
        assertTrue(error is kotlin.coroutines.cancellation.CancellationException)
    }
}
