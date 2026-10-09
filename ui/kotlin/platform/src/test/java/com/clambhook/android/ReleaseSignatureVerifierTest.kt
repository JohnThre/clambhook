// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

class ReleaseSignatureVerifierTest {
    private fun fixture(name: String): String =
        requireNotNull(javaClass.classLoader?.getResource("release-signatures/$name")) {
            "missing fixture $name"
        }.readText()

    // Throwaway fixture key: certify-only primary with a separate signing
    // subkey, mirroring the layout of the developer@jpfchang.org release key.
    private val verifier = ReleaseSignatureVerifier(
        fixture("test-release-key.asc"),
        primaryFingerprint = "3477638CE8C423007A78062C3BE00810EBF241AE",
        signingFingerprint = "DBABB357921F1DBE2436AC4D0ECC1D47AEE95CF3",
    )
    private val manifest = fixture("manifest.json").toByteArray()

    @Test
    fun acceptsSignatureFromPinnedSigningSubkey() {
        assertTrue(verifier.verify(manifest, fixture("good.sig")))
    }

    @Test
    fun rejectsTamperedContent() {
        val tampered = manifest.copyOf().also { it[it.size - 2] = '8'.code.toByte() }
        assertFalse(verifier.verify(tampered, fixture("good.sig")))
    }

    @Test
    fun rejectsSignatureFromAnotherKey() {
        assertFalse(verifier.verify(manifest, fixture("other.sig")))
    }

    @Test
    fun rejectsMissingOrMalformedSignature() {
        assertFalse(verifier.verify(manifest, ""))
        assertFalse(verifier.verify(manifest, "-----BEGIN PGP SIGNATURE-----\n\nnot base64\n-----END PGP SIGNATURE-----\n"))
    }

    @Test
    fun rejectsKeyRingWithoutPinnedFingerprints() {
        assertThrows(IllegalArgumentException::class.java) {
            ReleaseSignatureVerifier(fixture("test-release-key.asc"))
        }
        assertThrows(IllegalArgumentException::class.java) {
            ReleaseSignatureVerifier(
                fixture("test-release-key.asc"),
                primaryFingerprint = "3477638CE8C423007A78062C3BE00810EBF241AE",
                signingFingerprint = "0000000000000000000000000000000000000000",
            )
        }
    }

    @Test
    fun loadsThePinnedReleaseKey() {
        val key = File("../../../keys/clambhook-release-key.asc").readText()
        // Pinned fingerprints load; our fixture signature is not from that key.
        assertFalse(ReleaseSignatureVerifier(key).verify(manifest, fixture("good.sig")))
    }

    @Test
    fun signatureUrlSitsNextToTheAsset() {
        assertEquals(
            "https://github.com/JohnThre/clambhook/releases/download/v1.2.3/ClambHook-arm64.apk.sig",
            signatureUrlFor("https://github.com/JohnThre/clambhook/releases/download/v1.2.3/ClambHook-arm64.apk"),
        )
    }
}
