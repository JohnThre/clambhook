// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.linux.event

import com.clambhook.linux.model.ApiJson
import com.clambhook.linux.model.DaemonEvent
import kotlinx.coroutines.*
import okhttp3.*
import java.util.concurrent.atomic.AtomicInteger

interface EventStream {
    var onEvent: ((DaemonEvent) -> Unit)?
    var onFailed: ((String) -> Unit)?
    var onClosed: (() -> Unit)?
    fun start(uri: String, authorization: String)
    fun stop()
}

class EventStreamClient : EventStream {
    private val generation = AtomicInteger(0)
    private var client: OkHttpClient? = null
    private var webSocket: WebSocket? = null

    override var onEvent: ((DaemonEvent) -> Unit)? = null
    override var onFailed: ((String) -> Unit)? = null
    override var onClosed: (() -> Unit)? = null

    override fun start(uri: String, authorization: String) {
        stop()
        generation.incrementAndGet()
        val gen = generation.get()
        val httpClient = OkHttpClient.Builder()
            .readTimeout(45, java.util.concurrent.TimeUnit.SECONDS)
            .pingInterval(30, java.util.concurrent.TimeUnit.SECONDS)
            .build()
        client = httpClient
        val request = Request.Builder().url(uri)
        if (authorization.isNotEmpty()) request.header("Authorization", authorization)
        webSocket = httpClient.newWebSocket(request.build(), object : WebSocketListener() {
            override fun onMessage(webSocket: WebSocket, text: String) {
                if (generation.get() != gen) return
                try {
                    val event = ApiJson.decodeFromString(DaemonEvent.serializer(), text)
                    if (event.type.isNotEmpty()) onEvent?.invoke(event)
                } catch (e: Exception) {
                    onFailed?.invoke("malformed event frame: ${e.message ?: e.javaClass.simpleName}")
                }
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                if (generation.get() != gen) return
                onFailed?.invoke(t.message ?: "event stream failed")
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                if (generation.get() != gen) return
                onClosed?.invoke()
            }
        })
    }

    override fun stop() {
        generation.incrementAndGet()
        webSocket?.cancel()
        webSocket = null
        client?.dispatcher?.executorService?.shutdownNow()
        client = null
    }
}