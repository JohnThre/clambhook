// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.runtime

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.buffer
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.withContext
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import java.io.IOException
import java.util.concurrent.TimeUnit

/**
 * GNU/Linux controller transport for the local C daemon API: authenticated
 * loopback HTTP(S) plus the WebSocket event stream, using one bearer token for
 * both. Only loopback origins without credentials, paths, queries, or
 * fragments are accepted.
 */
class HttpBackend(baseUrl: String, token: String) : ConfigurableEndpointBackend {
    @Volatile
    private var origin: HttpUrl = normalizeBaseUrl(baseUrl)

    @Volatile
    private var bearerToken: String = token.trim()

    private val client = OkHttpClient.Builder()
        .connectTimeout(4, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .writeTimeout(15, TimeUnit.SECONDS)
        .followRedirects(false)
        .followSslRedirects(false)
        .retryOnConnectionFailure(false)
        .build()

    private val streamClient = client.newBuilder()
        .readTimeout(0, TimeUnit.MILLISECONDS)
        .pingInterval(20, TimeUnit.SECONDS)
        .build()

    override val baseUrl: String get() = origin.toString().removeSuffix("/")

    override fun configure(baseUrl: String, bearerToken: String) {
        val normalized = normalizeBaseUrl(baseUrl)
        origin = normalized
        this.bearerToken = bearerToken.trim()
    }

    override suspend fun request(method: String, path: String, body: String): String =
        withContext(Dispatchers.IO) {
            val verb = method.trim().uppercase()
            val target = resolve(path)
            val builder = Request.Builder().url(target).header("Accept", "application/json")
            authorize(builder)
            val requestBody = when {
                body.isNotEmpty() -> body.toRequestBody(JSON)
                verb == "GET" || verb == "HEAD" -> null
                else -> ByteArray(0).toRequestBody(JSON)
            }
            builder.method(verb, requestBody)
            try {
                client.newCall(builder.build()).execute().use { response -> readResponse(response) }
            } catch (error: BackendException) {
                throw error
            } catch (error: CancellationException) {
                throw error
            } catch (error: IOException) {
                throw BackendException(0, "Cannot reach $target: ${error.message}", error)
            }
        }

    override val supportsLiveEvents: Boolean = true

    override fun liveEvents(path: String): Flow<String> {
        val target = resolve(path)
        return callbackFlow {
            val builder = Request.Builder().url(target)
            authorize(builder)
            val socket = streamClient.newWebSocket(
                builder.build(),
                object : WebSocketListener() {
                    override fun onMessage(webSocket: WebSocket, text: String) {
                        trySend(text)
                    }

                    override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                        close(BackendException(0, "ClambHook event stream sent an unexpected binary frame"))
                        webSocket.cancel()
                    }

                    override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                        webSocket.close(code, null)
                        close()
                    }

                    override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                        close()
                    }

                    override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                        val status = response?.code ?: 0
                        response?.close()
                        close(
                            if (status != 0) {
                                BackendException(status, "ClambHook rejected the event stream upgrade (HTTP $status)", t)
                            } else {
                                BackendException(0, "ClambHook event stream failed: ${t.message}", t)
                            },
                        )
                    }
                },
            )
            awaitClose { socket.cancel() }
        }.buffer(256)
    }

    private fun authorize(builder: Request.Builder) {
        val current = bearerToken
        if (current.isNotBlank()) builder.header("Authorization", "Bearer $current")
    }

    private fun readResponse(response: Response): String {
        val source = response.body.source()
        // request() buffers up to the limit; true means the body is larger.
        if (source.request(MAX_RESPONSE_BYTES + 1L)) {
            throw BackendException(response.code, "response exceeds 16 MiB safety limit")
        }
        val text = source.buffer.readUtf8()
        if (!response.isSuccessful) {
            throw BackendException(
                response.code,
                text.ifBlank { "ClambHook returned HTTP ${response.code}" },
            )
        }
        return text
    }

    private fun resolve(path: String): HttpUrl {
        var value = path.trim()
        if (!value.startsWith("/")) value = "/$value"
        return (origin.toString().removeSuffix("/") + value).toHttpUrlOrNull()
            ?: throw BackendException(0, "Invalid request path: $path")
    }

    override val displayName: String = "GNU/Linux local daemon"

    override val supportsConnectionControl: Boolean = true

    override fun close() {
        client.dispatcher.cancelAll()
        streamClient.dispatcher.cancelAll()
        client.dispatcher.executorService.shutdown()
        client.connectionPool.evictAll()
    }

    companion object {
        const val DEFAULT_URL = "http://127.0.0.1:9090"
        private const val MAX_RESPONSE_BYTES = 16 * 1024 * 1024
        private val JSON = "application/json; charset=utf-8".toMediaType()
        private val IPV4_LOOPBACK = Regex("127(?:\\.[0-9]{1,3}){3}")

        /** Validates a loopback HTTP(S) origin; throws [BackendException] otherwise. */
        fun normalizeBaseUrl(value: String): HttpUrl {
            var normalized = value.trim().ifEmpty { DEFAULT_URL }
            while (normalized.endsWith("/")) normalized = normalized.dropLast(1)
            val invalid = BackendException(0, "API URL must be a loopback HTTP(S) origin without credentials or a path")
            val raw = try {
                java.net.URI("$normalized/")
            } catch (_: Exception) {
                throw BackendException(0, "Invalid API URL: $normalized")
            }
            val scheme = raw.scheme?.lowercase()
            if (scheme != "http" && scheme != "https") throw invalid
            if (raw.rawUserInfo != null || raw.rawQuery != null || raw.rawFragment != null) throw invalid
            if (raw.rawPath != null && raw.rawPath != "/") throw invalid
            val host = raw.host.orEmpty()
            val loopback = host.equals("localhost", ignoreCase = true) || host == "::1" || host == "[::1]" ||
                IPV4_LOOPBACK.matches(host)
            if (!loopback) throw invalid
            return "$normalized/".toHttpUrlOrNull() ?: throw BackendException(0, "Invalid API URL: $normalized")
        }
    }
}
