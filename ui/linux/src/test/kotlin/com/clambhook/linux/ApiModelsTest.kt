package com.clambhook.linux.model

import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class ApiModelsTest {
    @Test
    fun statusDecodesSnakeCaseListeners() {
        val status = ApiJson.decodeFromString(StatusPayload.serializer(), """
            {"running":true,"profile":"work","listeners":[{"protocol":"socks5","addr":"127.0.0.1:1080","active_conns":3}]}
        """)
        assertTrue(status.running)
        assertEquals("work", status.profile)
        assertEquals(1, status.listeners.size)
        assertEquals("socks5", status.listeners[0].protocol)
        assertEquals(3, status.listeners[0].activeConns)
    }

    @Test
    fun serversDecodesGeo() {
        val servers = ApiJson.decodeFromString(ServersPayload.serializer(), """
            {"profile":"default","chains":[{"name":"primary","servers":[{"name":"london","address":"uk.example:443","protocol":"clambback","geo":{"country":"United Kingdom","country_code":"GB","city":"London","latitude":51.5,"longitude":-0.1}}]}]}
        """)
        assertEquals("default", servers.profile)
        assertEquals(1, servers.chains.size)
        assertEquals("london", servers.chains[0].servers[0].name)
        assertEquals("GB", servers.chains[0].servers[0].geo.countryCode)
    }

    @Test
    fun eventValuesReadNumericAndStringData() {
        val event = ApiJson.decodeFromString(DaemonEvent.serializer(), """
            {"shard_id":1,"lamport":2,"ts_ns":3,"type":"connection.bytes","data":{"rx_delta":2048,"tx_delta":"1024","line":"ready"}}
        """)
        assertEquals("connection.bytes", event.type)
        assertEquals(2048.0, event.doubleData("rx_delta"))
        assertEquals(1024.0, event.doubleData("tx_delta"))
        assertEquals("ready", event.stringData("line"))
    }

    @Test
    fun trafficDecodesRuleSuggestions() {
        val traffic = ApiJson.decodeFromString(TrafficSnapshotPayload.serializer(), """
            {"updated_ts_ns":99,"summary":{"active_connections":1},"cleanup_suggestions":[{"kind":"unused_in_history","profile":"Work","rule_name":"old","target_rule_name":"old","operation":"delete_rule","message":"No recent traffic-history entries matched this rule."}],"rule_suggestions":[{"id":"domain_suffix:block:example.com","kind":"domain_suffix","profile":"Work","action":"block","draft_rule":{"name":"block-example-com","action":"block","domain_suffixes":["example.com"],"ports":[443],"networks":["tcp"]},"count":3,"reason":"Observed 3 connections across 2 subdomains."}],"connections":[{"conn_id":"c1","profile":"Work","state":"closed","target_host":"api.example.com"}]}
        """)
        assertEquals(1, traffic.cleanupSuggestions.size)
        assertEquals("old", traffic.cleanupSuggestions[0].targetRuleName)
        assertEquals("delete_rule", traffic.cleanupSuggestions[0].operation)
        assertEquals(1, traffic.ruleSuggestions.size)
        val suggestion = traffic.ruleSuggestions[0]
        assertEquals("domain_suffix", suggestion.kind)
        assertEquals("block-example-com", suggestion.draftRule.name)
        assertEquals("example.com", suggestion.draftRule.domainSuffixes[0])
        assertEquals(443, suggestion.draftRule.ports[0])
        assertEquals("tcp", suggestion.draftRule.networks[0])
        assertEquals("Work", traffic.connections[0].profile)
    }
}