package com.clambhook.android

import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

/**
 * Kotlin façade over the C/JNI packet-tunnel runtime used by the Android VPN
 * service. Android owns the platform lifecycle and TUN descriptor; C owns
 * configuration, routing, protocol chains, and packet forwarding.
 */
internal class NativeClambhookTunnelRuntime(
    packetWriter: NativeClambhookBridge.PacketWriter,
) : ClambhookTunnelRuntime, AutoCloseable {
    private val bridge = NativeClambhookBridge(packetWriter)
    private var configPath: String = ""

    override fun start(configPath: String) {
        bridge.start(configPath)
        this.configPath = configPath
    }
    override fun stop() = bridge.stop()
    override fun reload(configPath: String) {
        bridge.reload(configPath)
        this.configPath = configPath
    }
    override fun injectPacket(packet: ByteArray) = bridge.injectPacket(packet)
    override fun isRunning(): Boolean = bridge.isRunning()

    override fun statusJson(): String = bridge.query("status")
    override fun profilesJson(): String = bridge.query("profiles")
    override fun serversJson(): String = bridge.query("servers")
    override fun rulesJson(): String = bridge.query("rules")
    override fun trafficJson(): String = bridge.query("traffic")

    override fun dashboardJson(): String {
        val dashboard =
            buildJsonObject {
                put("status", ApiJson.parseToJsonElement(statusJson()))
                put("profiles", ApiJson.parseToJsonElement(profilesJson()))
                put("servers", ApiJson.parseToJsonElement(serversJson()))
                put("rules", ApiJson.parseToJsonElement(rulesJson()))
                put("policy_groups", ApiJson.parseToJsonElement(bridge.query("policy_groups")))
                put("rule_sets", ApiJson.parseToJsonElement(bridge.query("rule_sets")))
                put("traffic", ApiJson.parseToJsonElement(trafficJson()))
            }
        return ApiJson.encodeToString(JsonObject.serializer(), dashboard)
    }

    override fun developerStatusJson(): String = bridge.query("developer_status")
    override fun developerEntriesJson(): String = bridge.query("developer_entries")
    override fun developerHarJson(): String = bridge.query("developer_har")
    override fun developerCaPem(): String = bridge.query("developer_ca")
    override fun developerEntriesFilterJson(filterJson: String): String =
        bridge.query("developer_entries_filter", filterJson)
    override fun developerEntryCurlJson(id: String): String =
        bridge.query("developer_entry_curl", buildJsonObject { put("id", id) }.toString())
    override fun developerCurlImportJson(curl: String): String =
        bridge.query("developer_curl_import", buildJsonObject { put("curl", curl) }.toString())
    override fun developerSendJson(requestJson: String): String =
        bridge.query("developer_send", requestJson)
    override fun clearDeveloperEntries() {
        bridge.mutate("clear_developer_entries")
    }

    override fun setActiveProfile(name: String) {
        val request = buildJsonObject { put("name", name) }
        bridge.mutate("set_active_profile", ApiJson.encodeToString(JsonObject.serializer(), request))
    }

    override fun selectPolicyGroup(profile: String, group: String, chain: String) {
        check(configPath.isNotBlank()) { "native runtime has no config path" }
        val request = buildJsonObject {
            if (profile.isNotBlank()) put("profile", profile)
            put("group", group)
            put("chain", chain)
        }
        NativeClambhookConfigBridge.mutate(
            configPath,
            "select_policy_group",
            "policy_group_selection",
            ApiJson.encodeToString(JsonObject.serializer(), request),
        )
        reload(configPath)
    }

    override fun createTemporaryRuleFromConnectionJson(
        connId: String,
        profile: String,
        name: String,
        action: String,
        scope: String,
        ttlSeconds: Long,
    ): String {
        val request =
            buildJsonObject {
                put("conn_id", connId)
                put("profile", profile)
                put("name", name)
                put("action", action)
                put("scope", scope)
                put("ttl_seconds", ttlSeconds)
            }
        return bridge.mutate(
            "create_temporary_rule_from_connection",
            ApiJson.encodeToString(JsonObject.serializer(), request),
        )
    }

    override fun createRuleFromConnectionJson(
        configPath: String,
        connId: String,
        profile: String,
        name: String,
        action: String,
        scope: String,
    ): String {
        check(configPath.isNotBlank()) { "native runtime has no config path" }
        val request =
            buildJsonObject {
                put("conn_id", connId)
                put("profile", profile)
                put("name", name)
                put("action", action)
                put("scope", scope)
            }
        val mutationRequest =
            bridge.query(
                "rule_from_connection",
                ApiJson.encodeToString(JsonObject.serializer(), request),
            )
        val response =
            NativeClambhookConfigBridge.mutate(
                configPath,
                "create_rule",
                "rules_persistence",
                mutationRequest,
            )
        reload(configPath)
        return response
    }

    override fun cleanupRuleJson(
        configPath: String,
        profile: String,
        kind: String,
        ruleName: String,
        targetRuleName: String,
        operation: String,
    ): String {
        check(configPath.isNotBlank()) { "native runtime has no config path" }
        val request =
            buildJsonObject {
                put("profile", profile)
                put("kind", kind)
                put("rule_name", ruleName)
                put("target_rule_name", targetRuleName)
                put("operation", operation)
            }
        val mutationRequest =
            bridge.query(
                "cleanup_rule_request",
                ApiJson.encodeToString(JsonObject.serializer(), request),
            )
        val response =
            NativeClambhookConfigBridge.mutate(
                configPath,
                "replace_rules",
                "rules_persistence",
                mutationRequest,
            )
        reload(configPath)
        return response
    }

    override fun testRuleJson(profile: String, network: String, target: String, source: String): String {
        val request = buildJsonObject {
            put("profile", profile)
            put("network", network)
            put("target", target)
            put("source", source)
        }
        return bridge.query(
            "test_rule",
            ApiJson.encodeToString(JsonObject.serializer(), request),
        )
    }

    override fun pendingPromptsJson(): String = bridge.query("pending_prompts")

    override fun resolvePromptJson(
        id: String,
        action: String,
        scope: String,
        matchHost: Boolean,
        matchPort: Boolean,
        matchProtocol: Boolean,
        ttlSeconds: Long,
    ): String {
        val request =
            buildJsonObject {
                put("id", id)
                put("action", action)
                put("scope", scope)
                put("match_host", matchHost)
                put("match_port", matchPort)
                put("match_protocol", matchProtocol)
                put("ttl_seconds", ttlSeconds)
            }
        return bridge.mutate(
            "resolve_prompt",
            ApiJson.encodeToString(JsonObject.serializer(), request),
        )
    }

    override fun silentDecisionsJson(): String = bridge.query("silent_decisions")

    override fun promoteSilentDecisionJson(
        id: String,
        scope: String,
        matchHost: Boolean,
        matchPort: Boolean,
        matchProtocol: Boolean,
    ): String {
        val request =
            buildJsonObject {
                put("id", id)
                put("scope", scope)
                put("match_host", matchHost)
                put("match_port", matchPort)
                put("match_protocol", matchProtocol)
            }
        return bridge.mutate(
            "promote_silent_decision",
            ApiJson.encodeToString(JsonObject.serializer(), request),
        )
    }

    override fun trafficFilterJson(filterJson: String): String =
        bridge.query("traffic_filter", filterJson)

    override fun close() = bridge.close()

}

internal object NativeClambhookTunnelRuntimeFactory {
    fun create(packetWriter: NativeClambhookBridge.PacketWriter): ClambhookTunnelRuntime =
        NativeClambhookTunnelRuntime(packetWriter)
}
