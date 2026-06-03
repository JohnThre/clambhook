package com.clambhook.android

import kotlinx.serialization.decodeFromString
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Test

class ApiModelsTest {
    @Test
    fun decodesStatusPayload() {
        val status = ApiJson.decodeFromString<StatusPayload>(
            """
            {
              "running": true,
              "profile": "default",
              "listeners": [
                {"protocol": "socks5", "addr": "127.0.0.1:1080", "active_conns": 2}
              ]
            }
            """.trimIndent()
        )

        assertEquals(true, status.running)
        assertEquals("default", status.profile)
        assertEquals("socks5", status.listeners.single().protocol)
        assertEquals(2, status.listeners.single().activeConns)
    }

    @Test
    fun decodesServersPayload() {
        val servers = ApiJson.decodeFromString<ServersPayload>(
            """
            {
              "profile": "default",
              "chains": [
                {
                  "name": "primary",
                  "servers": [
                    {
                      "name": "london",
                      "address": "uk.example:443",
                      "protocol": "clambback",
                      "geo": {
                        "country": "United Kingdom",
                        "country_code": "GB",
                        "city": "London",
                        "latitude": 51.5072,
                        "longitude": -0.1276
                      }
                    }
                  ]
                }
              ]
            }
            """.trimIndent()
        )

        val server = servers.chains.single().servers.single()
        assertEquals("default", servers.profile)
        assertEquals("primary", servers.chains.single().name)
        assertEquals("london", server.name)
        assertEquals("GB", server.geo.countryCode)
    }

    @Test
    fun decodesDaemonEventPayload() {
        val event = ApiJson.decodeFromString<DaemonEvent>(
            """
            {
              "shard_id": 7,
              "lamport": 12,
              "ts_ns": 123456789,
              "type": "connection.bytes",
              "data": {
                "rx_delta": 2048,
                "tx_delta": 1024,
                "interval_ns": 1000000000
              }
            }
            """.trimIndent()
        )

        assertEquals(7uL, event.shardId)
        assertEquals(12uL, event.lamport)
        assertEquals("connection.bytes", event.type)
        assertEquals(JsonPrimitive(2048), event.data["rx_delta"])
    }

    @Test
    fun decodesTrafficMonitorAnalytics() {
        val traffic = ApiJson.decodeFromString<TrafficSnapshotPayload>(
            """
            {
              "updated_ts_ns": 99,
              "summary": {"active_connections": 1},
              "profile_context": {"active": "Work", "profiles": ["Work", "Home"]},
              "quick_filters": [{"key": "block", "label": "Block", "count": 2}],
              "rule_hits": [{"profile": "Work", "rule_name": "ads", "action": "block", "count": 2, "last_target": "ads.example.com:443"}],
              "block_decisions": [{"conn_id": "c1", "profile": "Work", "rule_name": "ads", "action": "block", "target_host": "ads.example.com", "ts_ns": 88}],
              "cleanup_suggestions": [{"kind": "unused_in_history", "profile": "Work", "rule_name": "old", "message": "No recent traffic-history entries matched this rule."}],
              "connections": [{"conn_id": "c1", "profile": "Work", "state": "closed", "rule_action": "block", "default": true, "target_host": "ads.example.com"}]
            }
            """.trimIndent()
        )

        assertEquals("Work", traffic.profileContext.active)
        assertEquals("block", traffic.quickFilters.single().key)
        assertEquals("ads", traffic.ruleHits.single().ruleName)
        assertEquals("ads.example.com", traffic.blockDecisions.single().targetHost)
        assertEquals("old", traffic.cleanupSuggestions.single().ruleName)
        assertEquals("Work", traffic.connections.single().profile)
        assertEquals(true, traffic.connections.single().isDefault)
    }
}
