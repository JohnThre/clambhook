// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.VpnService
import android.os.Bundle
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicLong

/** Headless Android glue activity used only for the system VPN consent sheet. */
class VpnConsentActivity : Activity() {
    private var requestId = 0L
    private var consentResult: Boolean? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        requestId = intent.getLongExtra(VpnConsentCoordinator.REQUEST_ID_EXTRA, 0L)
        if (requestId <= 0L) {
            finish()
            return
        }
        val consent = VpnService.prepare(this)
        if (consent == null) {
            consentResult = true
            finish()
        } else {
            @Suppress("DEPRECATION")
            startActivityForResult(consent, REQUEST_VPN)
        }
    }

    @Deprecated("Android result bridge for the system VPN consent activity")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == REQUEST_VPN) {
            consentResult = resultCode == RESULT_OK
            finish()
        }
    }

    override fun onDestroy() {
        val finalResult = if (isFinishing && requestId > 0L) {
            consentResult ?: (VpnService.prepare(this) == null)
        } else null
        super.onDestroy()
        if (finalResult != null) {
            VpnConsentCoordinator.complete(requestId, finalResult)
        }
    }

    private companion object {
        const val REQUEST_VPN = 41
    }
}

internal object VpnConsentCoordinator {
    internal const val REQUEST_ID_EXTRA = "com.clambhook.android.VPN_CONSENT_REQUEST_ID"

    private class Pending(val id: Long) {
        val latch = CountDownLatch(1)
        @Volatile var granted = false
    }

    private val requestSequence = AtomicLong(0L)
    private val lock = Any()
    private var current: Pending? = null

    fun request(context: Context): Boolean {
        if (VpnService.prepare(context) == null) return true
        val pending = synchronized(lock) {
            check(current == null) { "VPN consent is already pending" }
            Pending(requestSequence.incrementAndGet()).also { current = it }
        }
        context.startActivity(
            Intent(context, VpnConsentActivity::class.java)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                .putExtra(REQUEST_ID_EXTRA, pending.id),
        )
        val completed = pending.latch.await(90, TimeUnit.SECONDS)
        if (!completed) complete(pending.id, false)
        return completed && pending.granted
    }

    fun complete(requestId: Long, value: Boolean) {
        val pending = synchronized(lock) {
            val candidate = current
            if (candidate == null || candidate.id != requestId) {
                null
            } else {
                candidate.granted = value
                current = null
                candidate
            }
        } ?: return
        pending.latch.countDown()
    }

    fun cancelPending() {
        val requestId = synchronized(lock) { current?.id } ?: return
        complete(requestId, false)
    }
}
