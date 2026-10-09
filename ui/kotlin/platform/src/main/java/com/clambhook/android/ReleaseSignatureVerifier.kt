// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

import org.bouncycastle.bcpg.ArmoredInputStream
import org.bouncycastle.openpgp.PGPPublicKey
import org.bouncycastle.openpgp.PGPPublicKeyRingCollection
import org.bouncycastle.openpgp.PGPSignature
import org.bouncycastle.openpgp.PGPSignatureList
import org.bouncycastle.openpgp.PGPUtil
import org.bouncycastle.openpgp.jcajce.JcaPGPObjectFactory
import org.bouncycastle.openpgp.operator.bc.BcKeyFingerprintCalculator
import org.bouncycastle.openpgp.operator.bc.BcPGPContentVerifierBuilderProvider
import java.io.File
import java.io.InputStream

/**
 * Verifies detached, armored OpenPGP signatures on update manifests and APKs
 * against the pinned developer@jpfchang.org release key
 * (`keys/clambhook-release-key.asc`, bundled as an app asset). Only signatures
 * made by the pinned signing subkey of the pinned primary key are accepted.
 *
 * Key expiry is deliberately not enforced here: installed apps keep the key
 * as bundled at build time, so enforcing its expiry would strand them once the
 * owner extends the subkey. The release pipeline refuses to sign with a key
 * that is near expiry (`ch_gpg_check_expiry`), and the fingerprint pin is the
 * trust anchor on device.
 */
class ReleaseSignatureVerifier(
    armoredPublicKey: String,
    private val primaryFingerprint: String = RELEASE_PRIMARY_FINGERPRINT,
    private val signingFingerprint: String = RELEASE_SIGNING_FINGERPRINT,
) {
    private val signingKey: PGPPublicKey

    init {
        val rings = PGPPublicKeyRingCollection(
            PGPUtil.getDecoderStream(armoredPublicKey.byteInputStream()),
            BcKeyFingerprintCalculator(),
        )
        val ring = rings.keyRings.asSequence().firstOrNull {
            it.publicKey.fingerprint.toHex().equals(primaryFingerprint, ignoreCase = true)
        } ?: throw IllegalArgumentException("pinned release primary key is missing")
        signingKey = ring.publicKeys.asSequence().firstOrNull {
            it.fingerprint.toHex().equals(signingFingerprint, ignoreCase = true)
        } ?: throw IllegalArgumentException("pinned release signing subkey is missing")
        require(!ring.publicKey.hasRevocation() && !signingKey.hasRevocation()) {
            "pinned release key is revoked"
        }
    }

    /** Verifies [armoredSignature] over the bytes of [data]. */
    fun verify(data: ByteArray, armoredSignature: String): Boolean =
        verify(data.inputStream(), armoredSignature)

    /** Verifies [armoredSignature] over the contents of [file]. */
    fun verify(file: File, armoredSignature: String): Boolean =
        file.inputStream().use { verify(it, armoredSignature) }

    /** Verifies [armoredSignature] over everything read from [data]. */
    fun verify(data: InputStream, armoredSignature: String): Boolean {
        val signature = parseSignature(armoredSignature) ?: return false
        if (signature.keyID != signingKey.keyID) return false
        if (signature.signatureType != PGPSignature.BINARY_DOCUMENT) return false
        if (signature.creationTime.before(signingKey.creationTime)) return false
        return runCatching {
            signature.init(BcPGPContentVerifierBuilderProvider(), signingKey)
            val buffer = ByteArray(64 * 1024)
            while (true) {
                val read = data.read(buffer)
                if (read < 0) break
                signature.update(buffer, 0, read)
            }
            signature.verify()
        }.getOrDefault(false)
    }

    private fun parseSignature(armored: String): PGPSignature? = runCatching {
        ArmoredInputStream(armored.byteInputStream()).use { input ->
            val factory = JcaPGPObjectFactory(input)
            val signatures = generateSequence { factory.nextObject() }
                .filterIsInstance<PGPSignatureList>()
                .firstOrNull() ?: return@use null
            if (signatures.size() != 1) null else signatures[0]
        }
    }.getOrNull()

    companion object {
        const val RELEASE_PRIMARY_FINGERPRINT = "BAFC7769FDA1E0D4EBD23E2F6FF4807EAD977A9B"
        const val RELEASE_SIGNING_FINGERPRINT = "F09990BBE647C2D43F58D6F0EAA876B70B1832F5"
        const val RELEASE_KEY_ASSET = "clambhook-release-key.asc"
    }
}

private fun ByteArray.toHex(): String = joinToString("") { "%02X".format(it) }
