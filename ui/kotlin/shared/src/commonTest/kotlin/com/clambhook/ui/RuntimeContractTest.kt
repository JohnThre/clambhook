// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui

import com.clambhook.ui.format.Formatters
import com.clambhook.ui.json.Json
import com.clambhook.ui.model.DashboardMapper
import com.clambhook.ui.runtime.Backend
import com.clambhook.ui.runtime.DefaultRuntimeClient
import com.clambhook.ui.runtime.EventBatch
import com.clambhook.ui.runtime.EventCursor
import com.clambhook.ui.runtime.Profiles
import com.clambhook.ui.runtime.Status
import com.clambhook.ui.runtime.TrafficQuery
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.test.runTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class RecordingBackend(
    private val response: String = "{}",
    private val events: List<String> = emptyList(),
) : Backend {
    val calls = mutableListOf<String>()
    var livePath = ""

    override suspend fun request(method: String, path: String, body: String): String {
        calls += "$method $path $body"
        return response
    }

    override val supportsLiveEvents: Boolean get() = events.isNotEmpty()

    override fun liveEvents(path: String): Flow<String> {
        livePath = path
        return flowOf(*events.toTypedArray())
    }

    override val displayName = "test"
    override val supportsConnectionControl = true
    override fun close() = Unit
}

class RuntimeContractTest {
    @Test
    fun typedQueriesPreserveFrozenPathsAndEncoding() = runTest {
        val backend = RecordingBackend()
        val client = DefaultRuntimeClient(backend)
        client.servers("work vpn")
        client.traffic(TrafficQuery("A", "hello world", "active", "udp", "ads", "proxy", "org.example.app", 12, 25))
        client.events(EventCursor(41, 512, listOf("connection.*", "rule.matched"), listOf("conn 1")))
        client.removeTemporaryRule("temp/one")
        assertEquals("GET /api/v1/servers?profile=work+vpn ", backend.calls[0])
        assertEquals(
            "GET /api/v1/traffic?profile=A&query=hello+world&state=active&network=udp&rule=ads" +
                "&chain=proxy&application=org.example.app&after=12&limit=25 ",
            backend.calls[1],
        )
        assertEquals(
            "GET /api/v1/events/snapshot?after=41&limit=512&types=connection.*&types=rule.matched&conn_id=conn+1 ",
            backend.calls[2],
        )
        assertEquals("DELETE /api/v1/rules/temporary/temp%2Fone {}", backend.calls[3])
    }

    @Test
    fun typedModelsDecodeStatusProfilesAndEventReplay() {
        val status = Status.parse(
            """{"running":true,"profile":"work","tunnel_mode":"tun","listeners":[{"protocol":"socks5","addr":"127.0.0.1:1080","active_conns":3}]}""",
        )
        assertTrue(status.running)
        assertEquals("work", status.profile)
        assertEquals(3, status.listeners[0].activeConnections)
        assertEquals(listOf("work", "mobile"), Profiles.parse("""{"active":"work","profiles":["work","mobile"]}""").names)
        val batch = EventBatch.parse(
            """{"complete":false,"next_sequence":9,"events":[{"sequence":8,"shard_id":2,"lamport":4,"ts_ns":123,"type":"connection.opened","data":{"conn_id":"c1"}}]}""",
        )
        assertFalse(batch.complete)
        assertEquals(9, batch.nextSequence)
        assertEquals("connection.opened", batch.events[0].type)
    }

    @Test
    fun developerToolingUsesTheFrozenControlSurface() = runTest {
        val backend = RecordingBackend()
        val client = DefaultRuntimeClient(backend)
        client.developerPendingBreakpoints()
        client.developerCaPem()
        client.replaceDeveloperMapRules("""{"rules":[]}""")
        client.replaceDeveloperBreakpointRules("""{"rules":[]}""")
        client.replaceDeveloperRewriteRules("""{"rules":[]}""")
        client.regenerateDeveloperCa()
        client.resolveDeveloperBreakpoint("bp/one", """{"action":"drop"}""")
        assertEquals(
            listOf(
                "GET /api/v1/developer/breakpoints/pending ",
                "GET /api/v1/developer/ca.pem ",
                """PUT /api/v1/developer/map-rules {"rules":[]}""",
                """PUT /api/v1/developer/breakpoint-rules {"rules":[]}""",
                """PUT /api/v1/developer/rewrite-rules {"rules":[]}""",
                "POST /api/v1/developer/ca/regenerate {}",
                """POST /api/v1/developer/breakpoints/bp%2Fone/resolve {"action":"drop"}""",
            ),
            backend.calls,
        )
    }

    @Test
    fun profileConverterUsesReviewedHashContract() = runTest {
        val backend = RecordingBackend()
        val client = DefaultRuntimeClient(backend)
        client.reviewProfileConversion("proxies:\n", "mihomo", "Travel")
        client.importProfileConversion("[Proxy]\n", "surge", "Travel", "abc123", true)
        assertTrue(backend.calls[0].startsWith("POST /api/v1/config/converter/review "))
        assertTrue(backend.calls[0].contains("\"profile_name\":\"Travel\""))
        assertTrue(backend.calls[1].startsWith("POST /api/v1/config/converter/import "))
        assertTrue(backend.calls[1].contains("\"expected_sha256\":\"abc123\""))
        assertTrue(backend.calls[1].contains("\"activate\":true"))
    }

    @Test
    fun liveEventsUseTheWebSocketPathAndDecodeTypedFrames() = runTest {
        val backend = RecordingBackend(events = listOf("""{"type":"connection.opened","data":{"conn_id":"c1"}}"""))
        val client = DefaultRuntimeClient(backend)
        assertTrue(client.supportsLiveEvents)
        val received = client.liveEvents(EventCursor(5, 64, listOf("connection.*"), listOf("c 1"))).toList()
        assertEquals("/api/v1/events?after=5&limit=64&types=connection.*&conn_id=c+1", backend.livePath)
        assertEquals("connection.opened", received.single().type)
        assertEquals("c1", received.single().data["conn_id"].text())
    }
}

class JsonTest {
    @Test
    fun parsesNestedValues() {
        val root = Json.parse("""{"name":"ClambHook","running":true,"count":3,"items":["one",null,{"value":2.5}]}""")
        assertEquals("ClambHook", root["name"].text())
        assertTrue(root["running"].bool(false))
        assertEquals(3, root["count"].long(0))
        assertEquals("one", root["items"].elements()[0].text())
        assertTrue(root["items"].elements()[1].isNull)
        assertEquals(2.5, root["items"].elements()[2]["value"].double(0.0), 0.0001)
        assertFalse(root["missing"].exists)
        assertEquals("", root["count"].text(), "numbers are never coerced to text")
    }

    @Test
    fun serializesRequestsWithEscaping() {
        val parsed = Json.parse(Json.obj("name" to "line\n\"two\"", "enabled" to true, "values" to listOf(1, 2, 3)))
        assertEquals("line\n\"two\"", parsed["name"].text())
        assertTrue(parsed["enabled"].bool(false))
        assertEquals(3, parsed["values"].elements().size)
    }

    @Test
    fun rejectsAmbiguousOrMalformedInput() {
        listOf("""{"a":1,"a":2}""", "[01]", "\"unterminated", "true false", "", "{\"a\":\"\u0001\"}", "[1.]")
            .forEach { input -> assertFailsWith<IllegalArgumentException>(input) { Json.parse(input) } }
    }
}

class DashboardMapperTest {
    @Test
    fun mapsTheFrozenContracts() {
        val data = DashboardMapper.map(
            """{"running":true,"profile":"default","listeners":[{"protocol":"socks5","addr":"127.0.0.1:1080","active_conns":2}]}""",
            """{"profiles":["default","work"],"active":"default"}""",
            """{"profile":"default","chains":[{"name":"proxy","servers":[{"name":"edge","address":"example.test:443","protocol":"trojan","geo":{"city":"Singapore","country":"Singapore"}}]}]}""",
            """{"profile":"default","rules":[{"name":"private","action":"direct","cidrs":["10.0.0.0/8"],"networks":["tcp"]}]}""",
            """{"summary":{"active_connections":1,"rx_bps":2048,"tx_bps":1024,"rx_total":8192,"tx_total":4096},"connections":[{"conn_id":"c-1","target":"example.test:443","network":"tcp","state":"active","rule_action":"proxy","chain_name":"proxy","application":"browser","rx_bps":2048,"tx_bps":1024,"rx_total":8192,"tx_total":4096}]}""",
            """{"enabled":true,"strategy":"route","upstreams":[{"name":"secure","protocol":"doq","address":"dns.example:853"}]}""",
            """{"enabled":true,"capture_count":1}""",
            """{"entries":[{"id":"e-1","method":"GET","url":"https://example.test/","status_code":200,"error":""}]}""",
        )
        assertTrue(data.running)
        assertEquals("default", data.activeProfile)
        assertEquals(2, data.profiles.size)
        assertEquals(2, data.listeners[0].activeConnections)
        assertEquals("Singapore, Singapore", data.servers[0].location)
        assertEquals("CIDR: 10.0.0.0/8 · network: tcp", data.rules[0].matchSummary)
        assertEquals(1, data.traffic.activeConnections)
        assertEquals("example.test:443", data.connections[0].target)
        assertEquals("proxy", data.connections[0].route)
        assertEquals("secure · DOQ · dns.example:853", data.dns.upstreams[0])
        assertEquals(200, data.developer.captures[0].status)
    }

    @Test
    fun formatsBinaryUnitsLikeThePreviousController() {
        assertEquals("0 B", Formatters.bytes(-5))
        assertEquals("1023 B", Formatters.bytes(1023))
        assertEquals("1.0 KiB", Formatters.bytes(1024))
        assertEquals("1.5 MiB", Formatters.bytes(1536 * 1024))
        assertEquals("2.0 KiB/s", Formatters.rate(2048.0))
        assertEquals("1 entry", Formatters.count(1, "entry", "entries"))
        assertEquals("2 entries", Formatters.count(2, "entry", "entries"))
    }

    @Test
    fun packageListsIgnoreLabelsAndBlankLines() {
        assertEquals(
            linkedSetOf("org.example.one", "org.example.two"),
            parsePackageList("org.example.one # One\n\n  org.example.two  \norg.example.one"),
        )
    }
}
