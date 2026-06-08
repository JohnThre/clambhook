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
    fun decodesPolicyGroupsPayload() {
        val policyGroups = ApiJson.decodeFromString<PolicyGroupsPayload>(
            """
            {
              "profile": "default",
              "groups": [
                {
                  "name": "auto",
                  "type": "url-test",
                  "chains": ["proxy", "backup"],
                  "test_url": "https://probe.example/generate_204",
                  "interval": "30s",
                  "timeout": "5s",
                  "selected_chain": "backup",
                  "updated_ts_ns": 123,
                  "results": [
                    {"chain_name": "proxy", "healthy": false, "error": "timeout", "last_test_ts_ns": 100},
                    {"chain_name": "backup", "healthy": true, "latency_ns": 25000000, "status_code": 204, "last_test_ts_ns": 101}
                  ]
                }
              ]
            }
            """.trimIndent()
        )

        val group = policyGroups.groups.single()
        assertEquals("default", policyGroups.profile)
        assertEquals("auto", group.name)
        assertEquals("backup", group.selectedChain)
        assertEquals(2, group.results.size)
        assertEquals(true, group.results[1].healthy)
        assertEquals(25_000_000, group.results[1].latencyNs)
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
              "cleanup_suggestions": [{"kind": "unused_in_history", "profile": "Work", "rule_name": "old", "target_rule_name": "old", "operation": "delete_rule", "message": "No recent traffic-history entries matched this rule."}],
              "rule_suggestions": [{"id": "exact_host:block:api.example.com", "kind": "exact_host", "profile": "Work", "action": "block", "draft_rule": {"name": "block-api", "action": "block", "domains": ["api.example.com"], "ports": [443], "networks": ["tcp"]}, "count": 2, "reason": "Observed 2 matching connections."}],
              "connections": [{"conn_id": "c1", "profile": "Work", "state": "closed", "rule_action": "block", "default": true, "target_host": "ads.example.com"}]
            }
            """.trimIndent()
        )

        assertEquals("Work", traffic.profileContext.active)
        assertEquals("block", traffic.quickFilters.single().key)
        assertEquals("ads", traffic.ruleHits.single().ruleName)
        assertEquals("ads.example.com", traffic.blockDecisions.single().targetHost)
        assertEquals("old", traffic.cleanupSuggestions.single().ruleName)
        assertEquals("old", traffic.cleanupSuggestions.single().targetRuleName)
        assertEquals("delete_rule", traffic.cleanupSuggestions.single().operation)
        assertEquals("block-api", traffic.ruleSuggestions.single().draftRule.name)
        assertEquals(443, traffic.ruleSuggestions.single().draftRule.ports.single())
        assertEquals("Work", traffic.connections.single().profile)
        assertEquals(true, traffic.connections.single().isDefault)
    }
}
