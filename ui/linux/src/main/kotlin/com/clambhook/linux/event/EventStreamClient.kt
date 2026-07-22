package com.clambhook.linux.event

import com.clambhook.linux.model.ApiJson
import com.clambhook.linux.model.DaemonEvent
import kotlinx.coroutines.*
import okhttp3.*
import java.util.concurrent.atomic.AtomicInteger

class EventStreamClient {
    private val generation = AtomicInteger(0)
    private var client: OkHttpClient? = null
    private var webSocket: WebSocket? = null

    var onEvent: ((DaemonEvent) -> Unit)? = null
    var onFailed: ((String) -> Unit)? = null
    var onClosed: (() -> Unit)? = null

    fun start(uri: String, authorization: String) {
        stop()
        generation.incrementAndGet()
        val gen = generation.get()
        val httpClient = OkHttpClient()
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
                    // ignore malformed frames
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

    fun stop() {
        generation.incrementAndGet()
        webSocket?.close(1000, null)
        webSocket = null
        client?.dispatcher?.executorService?.shutdown()
        client = null
    }
}