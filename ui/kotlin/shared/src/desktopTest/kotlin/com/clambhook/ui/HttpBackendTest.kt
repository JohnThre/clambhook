// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui

import com.clambhook.ui.runtime.BackendException
import com.clambhook.ui.runtime.HttpBackend
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

class HttpBackendTest {
    private val server = MockWebServer()

    @BeforeTest
    fun start() = server.start(java.net.InetAddress.getByName("127.0.0.1"), 0)

    @AfterTest
    fun stop() = server.close()

    private fun backend(token: String = "secret") = HttpBackend("http://127.0.0.1:${server.port}", token)

    @Test
    fun requestsPreserveTheAuthenticatedControlContract() = runBlocking {
        server.enqueue(MockResponse.Builder().body("""{"ok":true}""").build())
        val response = backend().request("POST", "/api/v1/connect", """{"value":1}""")
        val recorded = server.takeRequest()
        assertEquals("""{"ok":true}""", response)
        assertEquals("POST", recorded.method)
        assertEquals("/api/v1/connect", recorded.url.encodedPath)
        assertEquals("Bearer secret", recorded.headers["Authorization"])
        assertEquals("""{"value":1}""", recorded.body?.utf8())
    }

    @Test
    fun httpErrorsCarryTheStatusAndDaemonMessage() = runBlocking {
        server.enqueue(MockResponse.Builder().code(409).body("profile is active").build())
        val failure = assertFailsWith<BackendException> { backend().get("/api/v1/status") }
        assertEquals(409, failure.statusCode)
        assertEquals("profile is active", failure.message)
    }

    @Test
    fun endpointConfigurationRejectsNonLoopbackOrNonOriginUrls() {
        listOf(
            "https://controller.example:9443",
            "http://127.0.0.1:9090/api",
            "http://user:pass@127.0.0.1:9090",
            "ftp://127.0.0.1",
            "http://127.0.0.1:9090?x=1",
        ).forEach { url -> assertFailsWith<BackendException>(url) { HttpBackend(url, "") } }
        assertEquals("http://[::1]:9090", HttpBackend("http://[::1]:9090/", "").baseUrl)
        assertEquals("http://localhost:9090", HttpBackend("http://localhost:9090", "").baseUrl)
    }

    @Test
    fun liveEventsUseAnAuthenticatedWebSocket() = runBlocking {
        server.enqueue(
            MockResponse.Builder().webSocketUpgrade(object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: okhttp3.Response) {
                    webSocket.send("""{"type":"connection.opened","data":{}}""")
                }
            }).build(),
        )
        val frame = withTimeout(5_000) { backend().liveEvents("/api/v1/events?after=0&limit=256").first() }
        val handshake = server.takeRequest()
        assertEquals("""{"type":"connection.opened","data":{}}""", frame)
        assertEquals("/api/v1/events?after=0&limit=256", handshake.url.encodedPath + "?" + handshake.url.encodedQuery)
        assertEquals("Bearer secret", handshake.headers["Authorization"])
        assertEquals("websocket", handshake.headers["Upgrade"])
    }
}
