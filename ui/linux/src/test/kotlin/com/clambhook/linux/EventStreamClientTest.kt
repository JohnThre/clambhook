package com.clambhook.linux.event

import com.clambhook.linux.model.ApiJson
import com.clambhook.linux.model.DaemonEvent
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class EventStreamClientTest {
    @Test
    fun malformedFrameReportsFailure() {
        val server = MockWebServer()
        val errors = mutableListOf<String>()
        val events = mutableListOf<DaemonEvent>()
        lateinit var serverSocket: WebSocket
        server.enqueue(MockResponse().withWebSocketUpgrade(object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: okhttp3.Response) {
                serverSocket = webSocket
                // Send a malformed frame as soon as the client connects.
                webSocket.send("not-json{{")
            }
        }))
        server.start()
        try {
            val client = EventStreamClient()
            client.onEvent = { events.add(it) }
            client.onFailed = { errors.add(it) }
            client.start("ws://${server.hostName}:${server.port}/events", "")
            Thread.sleep(400)
            client.stop()
            serverSocket.close(1000, null)
            assertTrue(errors.isNotEmpty(), "expected a malformed-frame failure")
            assertTrue(errors.any { it.contains("malformed", ignoreCase = true) })
        } finally {
            kotlin.runCatching { server.shutdown() }
        }
    }

    @Test
    fun validFrameIsDelivered() {
        val server = MockWebServer()
        val events = mutableListOf<DaemonEvent>()
        lateinit var serverSocket: WebSocket
        server.enqueue(MockResponse().withWebSocketUpgrade(object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: okhttp3.Response) {
                serverSocket = webSocket
                val frame = ApiJson.encodeToString(DaemonEvent.serializer(), DaemonEvent(type = "log.line", data = mapOf("line" to kotlinx.serialization.json.JsonPrimitive("hello"))))
                webSocket.send(frame)
            }
        }))
        server.start()
        try {
            val client = EventStreamClient()
            client.onEvent = { events.add(it) }
            client.start("ws://${server.hostName}:${server.port}/events", "")
            Thread.sleep(400)
            client.stop()
            serverSocket.close(1000, null)
            assertEquals(1, events.size)
            assertEquals("log.line", events[0].type)
        } finally {
            kotlin.runCatching { server.shutdown() }
        }
    }
}
