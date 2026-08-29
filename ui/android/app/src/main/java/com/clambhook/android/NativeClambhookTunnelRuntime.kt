// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

/** Thin lifecycle facade over the C17/JNI runtime owned by [ClambhookVpnService]. */
internal class NativeClambhookTunnelRuntime(
    packetWriter: NativeClambhookBridge.PacketWriter,
) : ClambhookTunnelRuntime, AutoCloseable {
    private val bridge = NativeClambhookBridge(packetWriter)

    override fun start(configPath: String) = bridge.start(configPath)
    override fun stop() = bridge.stop()
    override fun reload(configPath: String) = bridge.reload(configPath)
    override fun injectPacket(packet: ByteArray) = bridge.injectPacket(packet)
    override fun isRunning(): Boolean = bridge.isRunning()
    override fun query(operation: String, requestJson: String): String =
        bridge.query(operation, requestJson)
    override fun mutate(operation: String, requestJson: String): String =
        bridge.mutate(operation, requestJson)
    override fun developerRequest(repeat: Boolean, requestJson: String): String =
        bridge.developerRequest(repeat, requestJson)
    override fun close() = bridge.close()
}

internal object NativeClambhookTunnelRuntimeFactory {
    fun create(packetWriter: NativeClambhookBridge.PacketWriter): ClambhookTunnelRuntime =
        NativeClambhookTunnelRuntime(packetWriter)
}
