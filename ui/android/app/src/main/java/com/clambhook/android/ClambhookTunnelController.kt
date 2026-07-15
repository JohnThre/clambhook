package com.clambhook.android

import android.content.Context
import android.content.Intent
import android.net.VpnService

/**
 * Entry point for controlling the on-device packet tunnel from UI code.
 *
 * VPN consent must be granted before the service can establish the interface:
 * call [consentIntent] and, if it returns a non-null intent, launch it for a
 * result; start the service only after the user approves.
 */
object ClambhookTunnelController {
    /**
     * Returns the system consent intent that must be launched before starting
     * the tunnel, or `null` when consent was already granted and the service
     * can be started directly.
     */
    fun consentIntent(context: Context): Intent? = VpnService.prepare(context)

    fun start(context: Context) = ClambhookVpnService.start(context)

    fun stop(context: Context) = ClambhookVpnService.stop(context)
}
