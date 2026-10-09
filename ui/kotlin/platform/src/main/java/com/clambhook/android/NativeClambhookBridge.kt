// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

/**
 * Thin, typed owner of the production C runtime's JNI handle.
 */
internal class NativeClambhookBridge(
    packetWriter: PacketWriter,
) : AutoCloseable {
    fun interface PacketWriter {
        fun writePacket(packet: ByteArray)
    }

    private var handle: Long = nativeCreate(packetWriter)

    fun start(configPath: String) = nativeStart(requireHandle(), configPath)
    fun stop() = nativeStop(requireHandle())
    fun reload(configPath: String) = nativeReload(requireHandle(), configPath)
    fun injectPacket(packet: ByteArray, length: Int = packet.size) {
        require(length in 1..packet.size) { "packet length must be within the buffer" }
        nativeInjectPacket(requireHandle(), packet, length)
    }
    fun isRunning(): Boolean = nativeIsRunning(requireHandle())
    fun query(operation: String, requestJson: String = "{}"): String =
        nativeQuery(requireHandle(), operation, requestJson)
    fun mutate(operation: String, requestJson: String = "{}"): String =
        nativeMutate(requireHandle(), operation, requestJson)
    fun developerRequest(repeat: Boolean, requestJson: String): String =
        nativeDeveloperRequest(requireHandle(), repeat, requestJson)

    override fun close() {
        val current = handle
        if (current != 0L) {
            handle = 0L
            nativeDestroy(current)
        }
    }

    private fun requireHandle(): Long = handle.takeIf { it != 0L }
        ?: error("native runtime is closed")

    private external fun nativeCreate(packetWriter: PacketWriter): Long
    private external fun nativeDestroy(handle: Long)
    private external fun nativeStart(handle: Long, configPath: String)
    private external fun nativeStop(handle: Long)
    private external fun nativeReload(handle: Long, configPath: String)
    private external fun nativeInjectPacket(handle: Long, packet: ByteArray, length: Int)
    private external fun nativeIsRunning(handle: Long): Boolean
    private external fun nativeQuery(handle: Long, operation: String, requestJson: String): String
    private external fun nativeMutate(handle: Long, operation: String, requestJson: String): String
    private external fun nativeDeveloperRequest(
        handle: Long,
        repeat: Boolean,
        requestJson: String,
    ): String

    private companion object {
        init {
            System.loadLibrary("clambhook_jni")
        }
    }
}
