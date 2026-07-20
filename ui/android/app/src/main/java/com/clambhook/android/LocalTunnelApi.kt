package com.clambhook.android

import android.content.Context
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString

/**
 * [ClambhookApi] backed by the on-device packet-tunnel runtime instead of the
 * daemon HTTP API. Reads decode the runtime's JSON payloads; mutations apply
 * to the on-device config via the embedded config-edit primitives and take
 * effect after a runtime reload.
 */
class LocalTunnelApi(
    private val appContext: Context,
    private val session: ClambhookTunnelSession = ClambhookTunnelSession,
) : ClambhookApi {
    private fun runtime(): ClambhookTunnelRuntime =
        session.runtime.value ?: throw IllegalStateException("tunnel is not running")

    private suspend fun <T> io(block: () -> T): T = withContext(Dispatchers.IO) { block() }

    private fun dashboard(): TunnelDashboardBundle =
        ApiJson.decodeFromString(runtime().dashboardJson())

    override suspend fun status(): StatusPayload =
        io { ApiJson.decodeFromString(runtime().statusJson()) }

    override suspend fun profiles(): ProfilesPayload =
        io { ApiJson.decodeFromString(runtime().profilesJson()) }

    override suspend fun servers(): ServersPayload =
        io { ApiJson.decodeFromString(runtime().serversJson()) }

    override suspend fun rules(): RulesPayload =
        io { ApiJson.decodeFromString(runtime().rulesJson()) }

    override suspend fun traffic(): TrafficSnapshotPayload =
        io { ApiJson.decodeFromString(runtime().trafficJson()) }

    override suspend fun developerStatus(): DeveloperStatusPayload =
        io { ApiJson.decodeFromString(runtime().developerStatusJson()) }

    override suspend fun developerEntries(): DeveloperEntriesPayload =
        io { ApiJson.decodeFromString(runtime().developerEntriesJson()) }

    override suspend fun developerHar(): String = io { runtime().developerHarJson() }

    override suspend fun policyGroups(): PolicyGroupsPayload = io { dashboard().policyGroups }

    override suspend fun ruleSets(): RuleSetsPayload = io { dashboard().ruleSets }

    override suspend fun selectPolicyGroup(profile: String, group: String, chain: String): PolicyGroupsPayload =
        io {
            runtime().selectPolicyGroup(profile, group, chain)
            dashboard().policyGroups
        }

    override suspend fun setActiveProfile(name: String) = io { runtime().setActiveProfile(name) }

    override suspend fun createTemporaryRuleFromConnection(
        connection: TrafficConnectionPayload,
        action: String,
        ttlSeconds: Int,
    ): TemporaryRuleCreateResponsePayload = io {
        val json = runtime().createTemporaryRuleFromConnectionJson(
            connection.connId,
            connection.profile,
            "",
            action,
            "auto",
            ttlSeconds.toLong(),
        )
        ApiJson.decodeFromString(json)
    }

    override suspend fun clearDeveloperEntries() = io { runtime().clearDeveloperEntries() }

    override suspend fun explainRoute(profile: String, network: String, target: String, source: String): RuleTestResponse =
        io { ApiJson.decodeFromString(runtime().testRuleJson(profile, network, target, source)) }

    override suspend fun replaceRules(profile: String, rules: List<RulePayload>): RulesPayload = io {
        val configPath = session.configPath
        GomobileClambhookTunnelRuntimeFactory.replaceRulesJson(configPath, profile, ApiJson.encodeToString(rules))
        runtime().reload(configPath)
        ApiJson.decodeFromString(runtime().rulesJson())
    }

    override suspend fun connect() {
        io { ClambhookVpnService.start(appContext) }
    }

    override suspend fun disconnect() {
        io { ClambhookVpnService.stop(appContext) }
    }

    override suspend fun createRule(rule: RulePayload): RulesPayload = io {
        val rt = runtime()
        val configPath = session.configPath
        val json = GomobileClambhookTunnelRuntimeFactory.appendRuleJson(configPath, "", ApiJson.encodeToString(rule))
        rt.reload(configPath)
        ApiJson.decodeFromString(json)
    }

    override suspend fun createRuleFromConnection(connection: TrafficConnectionPayload, rule: RulePayload): RulesPayload = io {
        val rt = runtime()
        val configPath = session.configPath
        val json = rt.createRuleFromConnectionJson(
            configPath,
            connection.connId,
            connection.profile,
            rule.name,
            rule.action,
            "auto",
        )
        rt.reload(configPath)
        ApiJson.decodeFromString(json)
    }

    override suspend fun cleanupRule(suggestion: TrafficCleanupSuggestionPayload): RulesPayload = io {
        val rt = runtime()
        val configPath = session.configPath
        val json = rt.cleanupRuleJson(
            configPath,
            suggestion.profile,
            suggestion.kind,
            suggestion.ruleName,
            suggestion.targetRuleName.ifBlank { suggestion.ruleName },
            suggestion.operation,
        )
        rt.reload(configPath)
        ApiJson.decodeFromString(json)
    }

    override suspend fun replaceRuleSets(profile: String, ruleSets: List<RuleSetPayload>): RuleSetsPayload = io {
        val rt = runtime()
        val configPath = session.configPath
        GomobileClambhookTunnelRuntimeFactory.replaceRuleSetsJson(configPath, profile, ApiJson.encodeToString(ruleSets))
        rt.reload(configPath)
        ApiJson.decodeFromString(GomobileClambhookTunnelRuntimeFactory.ruleSetsJson(configPath, profile))
    }

    override suspend fun refreshRuleSets(profile: String, names: List<String>): RuleSetsPayload = io {
        val rt = runtime()
        val configPath = session.configPath
        val json = GomobileClambhookTunnelRuntimeFactory.refreshRuleSetsJson(configPath, profile, ApiJson.encodeToString(names))
        rt.reload(configPath)
        ApiJson.decodeFromString(json)
    }
}

/** Subset of the runtime dashboard payload used to source aggregate views. */
@Serializable
data class TunnelDashboardBundle(
    val status: StatusPayload = StatusPayload(),
    val profiles: ProfilesPayload = ProfilesPayload(),
    val servers: ServersPayload = ServersPayload(),
    val rules: RulesPayload = RulesPayload(),
    @SerialName("policy_groups")
    val policyGroups: PolicyGroupsPayload = PolicyGroupsPayload(),
    @SerialName("rule_sets")
    val ruleSets: RuleSetsPayload = RuleSetsPayload(),
    val traffic: TrafficSnapshotPayload = TrafficSnapshotPayload(),
)
