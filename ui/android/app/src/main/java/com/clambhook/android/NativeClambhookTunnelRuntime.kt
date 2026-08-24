package com.clambhook.android

import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

/**
 * Kotlin façade over the C/JNI runtime. Production selection remains gated,
 * but every implemented read uses the same [ClambhookTunnelRuntime] contract
 * as the gomobile oracle so parity tests can compare them directly.
 */
internal class NativeClambhookTunnelRuntime(
    packetWriter: NativeClambhookBridge.PacketWriter,
) : ClambhookTunnelRuntime, AutoCloseable {
    private val bridge = NativeClambhookBridge(packetWriter)

    override fun start(configPath: String) = bridge.start(configPath)
    override fun stop() = bridge.stop()
    override fun reload(configPath: String) = bridge.reload(configPath)
    override fun injectPacket(packet: ByteArray) = bridge.injectPacket(packet)
    override fun isRunning(): Boolean = bridge.isRunning()

    override fun statusJson(): String = bridge.query("status")
    override fun profilesJson(): String = bridge.query("profiles")
    override fun serversJson(): String = bridge.query("servers")
    override fun rulesJson(): String = bridge.query("rules")
    override fun trafficJson(): String = "{}"

    override fun dashboardJson(): String {
        val dashboard =
            buildJsonObject {
                put("status", ApiJson.parseToJsonElement(statusJson()))
                put("profiles", ApiJson.parseToJsonElement(profilesJson()))
                put("servers", ApiJson.parseToJsonElement(serversJson()))
                put("rules", ApiJson.parseToJsonElement(rulesJson()))
                put("policy_groups", ApiJson.parseToJsonElement(bridge.query("policy_groups")))
                put("rule_sets", ApiJson.parseToJsonElement(bridge.query("rule_sets")))
                put("traffic", JsonObject(emptyMap()))
            }
        return ApiJson.encodeToString(JsonObject.serializer(), dashboard)
    }

    override fun developerStatusJson(): String = "{}"
    override fun developerEntriesJson(): String = "{\"entries\":[]}"
    override fun developerHarJson(): String = "{\"log\":{\"version\":\"1.2\",\"entries\":[]}}"
    override fun developerCaPem(): String = unsupported("developer MITM CA")
    override fun developerEntriesFilterJson(filterJson: String): String = developerEntriesJson()
    override fun developerEntryCurlJson(id: String): String = unsupported("developer cURL export")
    override fun developerCurlImportJson(curl: String): String = unsupported("developer cURL import")
    override fun developerSendJson(requestJson: String): String = unsupported("developer request send")
    override fun clearDeveloperEntries() = Unit

    override fun setActiveProfile(name: String) {
        val request = buildJsonObject { put("name", name) }
        bridge.mutate("set_active_profile", ApiJson.encodeToString(JsonObject.serializer(), request))
    }

    override fun selectPolicyGroup(profile: String, group: String, chain: String) =
        unsupported<Unit>("policy group selection")

    override fun createTemporaryRuleFromConnectionJson(
        connId: String,
        profile: String,
        name: String,
        action: String,
        scope: String,
        ttlSeconds: Long,
    ): String = unsupported("temporary rules")

    override fun createRuleFromConnectionJson(
        configPath: String,
        connId: String,
        profile: String,
        name: String,
        action: String,
        scope: String,
    ): String = unsupported("persistent rules")

    override fun cleanupRuleJson(
        configPath: String,
        profile: String,
        kind: String,
        ruleName: String,
        targetRuleName: String,
        operation: String,
    ): String = unsupported("rule cleanup")

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

    override fun pendingPromptsJson(): String = "{\"prompts\":[]}"

    override fun resolvePromptJson(
        id: String,
        action: String,
        scope: String,
        matchHost: Boolean,
        matchPort: Boolean,
        matchProtocol: Boolean,
        ttlSeconds: Long,
    ): String = unsupported("prompt resolution")

    override fun silentDecisionsJson(): String = "{\"decisions\":[]}"

    override fun promoteSilentDecisionJson(
        id: String,
        scope: String,
        matchHost: Boolean,
        matchPort: Boolean,
        matchProtocol: Boolean,
    ): String = unsupported("silent decision promotion")

    override fun trafficFilterJson(filterJson: String): String = trafficJson()

    override fun close() = bridge.close()

    private fun <T> unsupported(feature: String): T =
        throw UnsupportedOperationException("$feature has not passed the native parity gate")
}
