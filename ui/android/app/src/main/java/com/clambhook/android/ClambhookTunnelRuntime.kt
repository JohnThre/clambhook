// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

/** Kotlin-facing contract implemented by the service-owned C17/JNI runtime. */
interface ClambhookTunnelRuntime {
    fun start(configPath: String)
    fun stop()
    fun reload(configPath: String)
    fun injectPacket(packet: ByteArray)
    fun isRunning(): Boolean

    /** Frozen query contract consumed by the shared JavaFX application. */
    fun query(operation: String, requestJson: String = "{}"): String

    /** Frozen mutation contract consumed by the shared JavaFX application. */
    fun mutate(operation: String, requestJson: String = "{}"): String

    fun developerRequest(repeat: Boolean, requestJson: String): String
}
