// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.runtime

import kotlinx.coroutines.flow.Flow

/**
 * Platform transport to the C runtime: authenticated loopback HTTP/WebSocket
 * on GNU/Linux, the in-process JNI runtime on Android.
 */
interface Backend : AutoCloseable {
    suspend fun request(method: String, path: String, body: String): String

    suspend fun get(path: String): String = request("GET", path, "")

    val supportsLiveEvents: Boolean get() = false

    /** Raw event frames from [path]; completes or fails when the stream ends. */
    fun liveEvents(path: String): Flow<String> =
        throw UnsupportedOperationException("this backend has no live event stream")

    val displayName: String

    val supportsConnectionControl: Boolean

    override fun close()
}

/** Backend whose loopback control endpoint can be changed at runtime. */
interface ConfigurableEndpointBackend : Backend {
    val baseUrl: String

    fun configure(baseUrl: String, bearerToken: String)
}

/** A stable error carrying the platform response code when one exists. */
class BackendException(
    val statusCode: Int = 0,
    message: String?,
    cause: Throwable? = null,
) : RuntimeException(if (message.isNullOrBlank()) "ClambHook request failed" else message, cause)
