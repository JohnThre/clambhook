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
    private val onConnect: () -> Unit,
    private val onDisconnect: () -> Unit,
    private val session: ClambhookTunnelSession = ClambhookTunnelSession,
) : ClambhookApi {
    constructor(context: Context) : this(
        onConnect = { ClambhookVpnService.start(context.applicationContext) },
        onDisconnect = { ClambhookVpnService.stop(context.applicationContext) },
    )

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
    override suspend fun traffic(filter: TrafficMonitorFilter): TrafficSnapshotPayload =
        io { ApiJson.decodeFromString(TrafficSnapshotPayload.serializer(), runtime().trafficFilterJson(ApiJson.encodeToString(TrafficMonitorFilter.serializer(), filter))) }
    override suspend fun pendingPrompts(): PromptsPayload =
        io { ApiJson.decodeFromString(PromptsPayload.serializer(), runtime().pendingPromptsJson()) }
    override suspend fun resolvePrompt(id: String, action: String, scope: String, matchHost: Boolean, matchPort: Boolean, matchProtocol: Boolean, ttlSeconds: Long) {
        io { runtime().resolvePromptJson(id, action, scope, matchHost, matchPort, matchProtocol, ttlSeconds) }
    }
    override suspend fun silentDecisions(): SilentDecisionsPayload =
        io { ApiJson.decodeFromString(SilentDecisionsPayload.serializer(), runtime().silentDecisionsJson()) }
    override suspend fun promoteSilentDecision(id: String, scope: String, matchHost: Boolean, matchPort: Boolean, matchProtocol: Boolean) {
        io { runtime().promoteSilentDecisionJson(id, scope, matchHost, matchPort, matchProtocol) }
    }

    override suspend fun developerStatus(): DeveloperStatusPayload =
        io { ApiJson.decodeFromString(runtime().developerStatusJson()) }

    override suspend fun developerEntries(): DeveloperEntriesPayload =
        io { ApiJson.decodeFromString(runtime().developerEntriesJson()) }
    override suspend fun developerEntries(filter: DeveloperEntriesFilter): List<DeveloperEntryPayload> =
        io { ApiJson.decodeFromString(DeveloperEntriesPayload.serializer(), runtime().developerEntriesFilterJson(filter.toJson())).entries }
    override suspend fun developerEntryCurl(id: String): String =
        io { ApiJson.decodeFromString(CurlExportResponse.serializer(), runtime().developerEntryCurlJson(id)).curl }
    override suspend fun importCurl(text: String): ParsedCurlResponse =
        io { ApiJson.decodeFromString(ParsedCurlResponse.serializer(), runtime().developerCurlImportJson(text)) }
    override suspend fun sendComposed(request: ComposedRequestPayload): DeveloperEntryPayload =
        io {
            val body = ApiJson.encodeToString(ComposedRequestPayload.serializer(), request)
            ApiJson.decodeFromString(DeveloperRepeatResponsePayload.serializer(), runtime().developerSendJson(body)).entry
        }
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
        io { onConnect() }
    }

    override suspend fun disconnect() {
        io { onDisconnect() }
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

    // The gomobile runtime exposes no [profile.conditioner] config-read or
    // config-edit primitive, and we cannot add native methods without
    // rebuilding the Go .aar. So we derive a read-only, disabled snapshot from
    // the active profile the runtime already reports, and reject edits — the
    // network conditioner can only be mutated through the daemon HTTP API.
    override suspend fun conditioner(profile: String): ConditionerPayload = io {
        val active = profile.ifBlank { ApiJson.decodeFromString<StatusPayload>(runtime().statusJson()).profile }
        ConditionerPayload(profile = active, enabled = false)
    }

    override suspend fun updateConditioner(request: ConditionerUpdateRequest): ConditionerPayload =
        throw UnsupportedOperationException("conditioner editing requires the daemon HTTP API")

    override val supportsConditionerEditing: Boolean get() = false
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
