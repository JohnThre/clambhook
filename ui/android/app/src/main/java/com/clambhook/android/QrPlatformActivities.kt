// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

@file:Suppress("DEPRECATION")

package com.clambhook.android

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.graphics.Bitmap
import android.os.Bundle
import androidx.core.content.FileProvider
import com.google.zxing.BarcodeFormat
import com.google.zxing.MultiFormatWriter
import com.google.zxing.integration.android.IntentIntegrator
import java.io.File
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/** Headless glue activity for the optional QR profile scanner. */
class QrScanActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        IntentIntegrator(this)
            .setDesiredBarcodeFormats(IntentIntegrator.QR_CODE)
            .setPrompt("Scan a ClambHook profile")
            .setBeepEnabled(false)
            .setOrientationLocked(false)
            .initiateScan()
    }

    @Deprecated("ZXing result bridge")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        val result = IntentIntegrator.parseActivityResult(requestCode, resultCode, data)
        if (result != null) {
            QrScanCoordinator.complete(result.contents.orEmpty())
            finish()
            return
        }
        super.onActivityResult(requestCode, resultCode, data)
    }

    override fun onDestroy() {
        if (isFinishing) QrScanCoordinator.complete("")
        super.onDestroy()
    }
}

internal object QrScanCoordinator {
    private val active = AtomicBoolean(false)
    @Volatile private var latch: CountDownLatch? = null
    @Volatile private var value = ""

    fun request(context: Context): String {
        check(active.compareAndSet(false, true)) { "QR scan is already pending" }
        val nextLatch = CountDownLatch(1)
        latch = nextLatch
        value = ""
        context.startActivity(
            Intent(context, QrScanActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
        )
        if (!nextLatch.await(90, TimeUnit.SECONDS)) complete("")
        return value
    }

    fun complete(result: String) {
        if (!active.compareAndSet(true, false)) return
        value = result
        latch?.countDown()
        latch = null
    }
}

internal object QrShare {
    fun share(context: Context, value: String) {
        val target = createQrShareFile(context, value)
        val uri = FileProvider.getUriForFile(context, "${context.packageName}.updates", target)
        val intent = Intent(Intent.ACTION_SEND).apply {
            type = "image/png"
            putExtra(Intent.EXTRA_STREAM, uri)
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
        context.startActivity(Intent.createChooser(intent, "Share ClambHook profile").addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }
}

/** Encodes a profile payload as the exact PNG shared by the platform facade. */
internal fun createQrShareFile(context: Context, value: String): File {
    check(value.isNotBlank()) { "QR payload is empty" }
    val matrix = MultiFormatWriter().encode(value, BarcodeFormat.QR_CODE, 1024, 1024)
    val pixels = IntArray(matrix.width * matrix.height)
    for (y in 0 until matrix.height) {
        for (x in 0 until matrix.width) {
            pixels[y * matrix.width + x] =
                if (matrix[x, y]) 0xff000000.toInt() else 0xffffffff.toInt()
        }
    }
    val bitmap = Bitmap.createBitmap(matrix.width, matrix.height, Bitmap.Config.ARGB_8888)
    bitmap.setPixels(pixels, 0, matrix.width, 0, 0, matrix.width, matrix.height)
    val directory = File(context.cacheDir, "share").apply { mkdirs() }
    val target = File(directory, "clambhook-profile.png")
    target.outputStream().use { output ->
        check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, output)) {
            "QR image encoding failed"
        }
    }
    bitmap.recycle()
    return target
}
