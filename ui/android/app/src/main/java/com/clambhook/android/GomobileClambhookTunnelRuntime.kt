package com.clambhook.android

import com.clambhook.mobile.Mobile
import com.clambhook.mobile.PacketWriter
import com.clambhook.mobile.TunnelRuntime

/**
 * Kotlin-facing surface of the embedded packet-tunnel runtime. Read methods
 * return the JSON payloads emitted by `pkg/mobile.TunnelRuntime`; mutation
 * methods apply live against the running packet stack.
 */
interface ClambhookTunnelRuntime {
    fun start(configPath: String)
    fun stop()
    fun reload(configPath: String)
    fun injectPacket(packet: ByteArray)
    fun isRunning(): Boolean

    fun statusJson(): String
    fun profilesJson(): String
    fun serversJson(): String
    fun rulesJson(): String
    fun trafficJson(): String
    fun dashboardJson(): String
    fun developerStatusJson(): String
    fun developerEntriesJson(): String
    fun developerHarJson(): String
    fun developerCaPem(): String

    fun clearDeveloperEntries()
    fun setActiveProfile(name: String)
    fun selectPolicyGroup(profile: String, group: String, chain: String)
    fun createTemporaryRuleFromConnectionJson(
        connId: String,
        profile: String,
        name: String,
        action: String,
        scope: String,
        ttlSeconds: Long,
    ): String

    fun createRuleFromConnectionJson(
        configPath: String,
        connId: String,
        profile: String,
        name: String,
        action: String,
        scope: String,
    ): String
    fun cleanupRuleJson(
        configPath: String,
        profile: String,
        kind: String,
        ruleName: String,
        targetRuleName: String,
        operation: String,
    ): String

    fun testRuleJson(profile: String, network: String, target: String, source: String): String
}

class GomobileClambhookTunnelRuntime(
    private val delegate: TunnelRuntime,
) : ClambhookTunnelRuntime {
    override fun start(configPath: String) = delegate.start(configPath)
    override fun stop() = delegate.stop()
    override fun reload(configPath: String) = delegate.reload(configPath)
    override fun injectPacket(packet: ByteArray) = delegate.injectPacket(packet)
    override fun isRunning(): Boolean = delegate.isRunning

    override fun statusJson(): String = delegate.statusJSON()
    override fun profilesJson(): String = delegate.profilesJSON()
    override fun serversJson(): String = delegate.serversJSON()
    override fun rulesJson(): String = delegate.rulesJSON()
    override fun trafficJson(): String = delegate.trafficJSON()
    override fun dashboardJson(): String = delegate.dashboardJSON()
    override fun developerStatusJson(): String = delegate.developerStatusJSON()
    override fun developerEntriesJson(): String = delegate.developerEntriesJSON()
    override fun developerHarJson(): String = delegate.developerHARJSON()
    override fun developerCaPem(): String = delegate.developerCAPEM()

    override fun clearDeveloperEntries() = delegate.clearDeveloperEntries()
    override fun setActiveProfile(name: String) = delegate.setActiveProfile(name)
    override fun selectPolicyGroup(profile: String, group: String, chain: String) =
        delegate.selectPolicyGroup(profile, group, chain)

    override fun createTemporaryRuleFromConnectionJson(
        connId: String,
        profile: String,
        name: String,
        action: String,
        scope: String,
        ttlSeconds: Long,
    ): String = delegate.createTemporaryRuleFromConnectionJSON(connId, profile, name, action, scope, ttlSeconds)

    override fun createRuleFromConnectionJson(
        configPath: String,
        connId: String,
        profile: String,
        name: String,
        action: String,
        scope: String,
    ): String = delegate.createRuleFromConnectionJSON(configPath, connId, profile, name, action, scope)

    override fun cleanupRuleJson(
        configPath: String,
        profile: String,
        kind: String,
        ruleName: String,
        targetRuleName: String,
        operation: String,
    ): String = delegate.cleanupRuleJSON(configPath, profile, kind, ruleName, targetRuleName, operation)

    override fun testRuleJson(profile: String, network: String, target: String, source: String): String =
        delegate.testRuleJSON(profile, network, target, source)
}

object GomobileClambhookTunnelRuntimeFactory {
    fun networkSettingsJson(configPath: String): String = Mobile.tunnelNetworkSettingsJSON(configPath)

    fun replaceRulesJson(configPath: String, profile: String, rulesJson: String) =
        Mobile.replaceTunnelRulesJSON(configPath, profile, rulesJson)

    fun appendRuleJson(configPath: String, profile: String, ruleJson: String): String =
        Mobile.appendTunnelRuleJSON(configPath, profile, ruleJson)

    fun replaceRuleSetsJson(configPath: String, profile: String, ruleSetsJson: String) =
        Mobile.replaceTunnelRuleSetsJSON(configPath, profile, ruleSetsJson)

    fun refreshRuleSetsJson(configPath: String, profile: String, namesJson: String): String =
        Mobile.refreshRuleSetsJSON(configPath, profile, namesJson)

    fun ruleSetsJson(configPath: String, profile: String): String =
        Mobile.ruleSetsJSON(configPath, profile)

    fun create(packetWriter: PacketWriter): ClambhookTunnelRuntime =
        GomobileClambhookTunnelRuntime(Mobile.newTunnelRuntime(packetWriter))
}
