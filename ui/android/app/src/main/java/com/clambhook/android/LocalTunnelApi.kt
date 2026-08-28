package com.clambhook.android

import android.content.Context
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.encodeToJsonElement
import kotlinx.serialization.json.put

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
    private val ruleSetRefresher = AndroidRuleSetRefresher()

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
    override suspend fun repeatDeveloperEntry(id: String): DeveloperEntryPayload =
        io {
            val body = buildJsonObject { put("entry_id", id) }.toString()
            ApiJson.decodeFromString(
                DeveloperRepeatResponsePayload.serializer(),
                runtime().developerRepeatJson(body),
            ).entry
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
        NativeClambhookConfigBridge.mutate(
            configPath,
            "replace_rules",
            "rules_persistence",
            configCollectionRequest(profile, "rules", ApiJson.encodeToJsonElement(rules)),
        )
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
        val json = NativeClambhookConfigBridge.mutate(
            configPath,
            "create_rule",
            "rules_persistence",
            ApiJson.encodeToString(
                buildJsonObject {
                    put("position", "append")
                    put("rule", ApiJson.encodeToJsonElement(rule))
                },
            ),
        )
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
        NativeClambhookConfigBridge.mutate(
            configPath,
            "replace_rule_sets",
            "rule_sets_persistence",
            configCollectionRequest(profile, "rule_sets", ApiJson.encodeToJsonElement(ruleSets)),
        )
        rt.reload(configPath)
        ApiJson.decodeFromString(
            NativeClambhookConfigBridge.query(
                configPath,
                "rule_sets",
                configProfileRequest(profile),
            ),
        )
    }

    override suspend fun refreshRuleSets(profile: String, names: List<String>): RuleSetsPayload = io {
        val rt = runtime()
        val configPath = session.configPath
        val payload = ruleSetRefresher.refresh(configPath, profile, names)
        rt.reload(configPath)
        payload
    }

    override suspend fun conditioner(profile: String): ConditionerPayload = io {
        ApiJson.decodeFromString(
            NativeClambhookConfigBridge.query(
                session.configPath,
                "conditioner",
                configProfileRequest(profile),
            ),
        )
    }

    override suspend fun updateConditioner(request: ConditionerUpdateRequest): ConditionerPayload = io {
        val rt = runtime()
        val result = NativeClambhookConfigBridge.mutate(
            session.configPath,
            "update_conditioner",
            "conditioner",
            conditionerRequestJson(request),
        )
        rt.reload(session.configPath)
        ApiJson.decodeFromString(result)
    }

    override val supportsConditionerEditing: Boolean get() = true

    private fun configProfileRequest(profile: String): String = ApiJson.encodeToString(
        buildJsonObject {
            if (profile.isNotBlank()) put("profile", profile)
        },
    )

    private fun configCollectionRequest(profile: String, key: String, value: JsonElement): String =
        ApiJson.encodeToString(
            buildJsonObject {
                if (profile.isNotBlank()) put("profile", profile)
                put(key, value)
            },
        )

    private fun conditionerRequestJson(request: ConditionerUpdateRequest): String = ApiJson.encodeToString(
        buildJsonObject {
            request.profile?.takeIf { it.isNotBlank() }?.let { put("profile", it) }
            request.enabled?.let { put("enabled", it) }
            request.downloadKbps?.let { put("download_kbps", it) }
            request.uploadKbps?.let { put("upload_kbps", it) }
            request.latency?.let { put("latency", it) }
            request.jitter?.let { put("jitter", it) }
            request.lossPercent?.let { put("loss_percent", it) }
        },
    )
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
