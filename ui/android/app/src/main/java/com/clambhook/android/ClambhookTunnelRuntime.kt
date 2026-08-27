package com.clambhook.android

/** Kotlin-facing contract implemented by the native C/JNI tunnel runtime. */
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
    fun developerEntriesFilterJson(filterJson: String): String
    fun developerEntryCurlJson(id: String): String
    fun developerCurlImportJson(curl: String): String
    fun developerSendJson(requestJson: String): String

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

    fun pendingPromptsJson(): String
    fun resolvePromptJson(
        id: String,
        action: String,
        scope: String,
        matchHost: Boolean,
        matchPort: Boolean,
        matchProtocol: Boolean,
        ttlSeconds: Long,
    ): String

    fun silentDecisionsJson(): String
    fun promoteSilentDecisionJson(
        id: String,
        scope: String,
        matchHost: Boolean,
        matchPort: Boolean,
        matchProtocol: Boolean,
    ): String

    fun trafficFilterJson(filterJson: String): String
}
