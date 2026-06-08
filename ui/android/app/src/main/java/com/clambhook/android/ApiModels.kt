package com.clambhook.android

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.doubleOrNull

val ApiJson = Json {
    ignoreUnknownKeys = true
    encodeDefaults = true
}

@Serializable
data class StatusPayload(
    val running: Boolean = false,
    val profile: String = "",
    val listeners: List<ListenerStatusPayload> = emptyList()
)

@Serializable
data class ListenerStatusPayload(
    val protocol: String,
    val addr: String,
    @SerialName("active_conns")
    val activeConns: Int
)

@Serializable
data class ProfilesPayload(
    val profiles: List<String> = emptyList(),
    val active: String = ""
)

@Serializable
data class ServersPayload(
    val profile: String = "",
    val chains: List<ChainPayload> = emptyList()
)

@Serializable
data class RulesPayload(
    val profile: String = "",
    val rules: List<RulePayload> = emptyList(),
    @SerialName("generated_rules")
    val generatedRules: List<RulePayload> = emptyList(),
    @SerialName("effective_rules")
    val effectiveRules: List<RulePayload> = emptyList(),
    @SerialName("rule_sets")
    val ruleSets: List<RuleSetStatusPayload> = emptyList()
)

@Serializable
data class RuleSetsPayload(
    val profile: String = "",
    @SerialName("rule_sets")
    val ruleSets: List<RuleSetPayload> = emptyList(),
    val statuses: List<RuleSetStatusPayload> = emptyList()
)

@Serializable
data class RuleSetPayload(
    val name: String = "",
    val domains: List<String> = emptyList(),
    @SerialName("domain_suffixes")
    val domainSuffixes: List<String> = emptyList(),
    @SerialName("domain_keywords")
    val domainKeywords: List<String> = emptyList(),
    val cidrs: List<String> = emptyList(),
    val url: String = "",
    val format: String = "",
    val disabled: Boolean = false
)

@Serializable
data class RuleSetStatusPayload(
    val name: String = "",
    val url: String = "",
    val format: String = "",
    val disabled: Boolean = false,
    val cached: Boolean = false,
    @SerialName("fetched_ts_ns")
    val fetchedTsNs: Long = 0,
    @SerialName("inline_domain_count")
    val inlineDomainCount: Int = 0,
    @SerialName("inline_cidr_count")
    val inlineCidrCount: Int = 0,
    @SerialName("domain_count")
    val domainCount: Int = 0,
    @SerialName("cidr_count")
    val cidrCount: Int = 0,
    val skipped: Int = 0,
    @SerialName("cache_error")
    val cacheError: String = "",
    @SerialName("last_error")
    val lastError: String = ""
)

@Serializable
data class PolicyGroupsPayload(
    val profile: String = "",
    val groups: List<PolicyGroupPayload> = emptyList()
)

@Serializable
data class PolicyGroupPayload(
    val name: String = "",
    val type: String = "",
    val chains: List<String> = emptyList(),
    @SerialName("test_url")
    val testUrl: String = "",
    val interval: String = "",
    val timeout: String = "",
    @SerialName("selected_chain")
    val selectedChain: String = "",
    val selected: String = "",
    @SerialName("selection_mode")
    val selectionMode: String = "",
    @SerialName("updated_ts_ns")
    val updatedTsNs: Long = 0,
    val results: List<PolicyProbeResultPayload> = emptyList()
)

@Serializable
data class PolicyProbeResultPayload(
    @SerialName("chain_name")
    val chainName: String = "",
    val healthy: Boolean = false,
    @SerialName("latency_ns")
    val latencyNs: Long = 0,
    @SerialName("status_code")
    val statusCode: Int = 0,
    val error: String = "",
    @SerialName("last_test_ts_ns")
    val lastTestTsNs: Long = 0
)

@Serializable
data class RulePayload(
    val name: String = "",
    val action: String = "",
    @SerialName("rule_sets")
    val ruleSets: List<String> = emptyList(),
    val domains: List<String> = emptyList(),
    @SerialName("domain_suffixes")
    val domainSuffixes: List<String> = emptyList(),
    @SerialName("domain_keywords")
    val domainKeywords: List<String> = emptyList(),
    val cidrs: List<String> = emptyList(),
    @SerialName("source_cidrs")
    val sourceCidrs: List<String> = emptyList(),
    val ports: List<Int> = emptyList(),
    val networks: List<String> = emptyList()
)

@Serializable
data class CreateRuleRequest(
    val rule: RulePayload,
    val position: String = "append"
)

@Serializable
data class CreateRuleFromConnectionRequest(
    @SerialName("conn_id")
    val connId: String,
    val profile: String = "",
    val name: String = "",
    val action: String = "",
    val scope: String = "auto",
    val position: String = "append"
)

@Serializable
data class CleanupRuleRequest(
    val profile: String = "",
    val kind: String,
    @SerialName("rule_name")
    val ruleName: String,
    @SerialName("target_rule_name")
    val targetRuleName: String,
    val operation: String
)

@Serializable
data class ReplaceRulesRequest(
    val profile: String = "",
    val rules: List<RulePayload> = emptyList()
)

@Serializable
data class ReplaceRuleSetsRequest(
    val profile: String = "",
    @SerialName("rule_sets")
    val ruleSets: List<RuleSetPayload> = emptyList()
)

@Serializable
data class RefreshRuleSetsRequest(
    val profile: String = "",
    val names: List<String> = emptyList()
)

@Serializable
data class SelectPolicyGroupRequest(
    val profile: String = "",
    val group: String = "",
    val chain: String = ""
)

@Serializable
data class RouteExplainRequest(
    val profile: String = "",
    val network: String = "",
    val target: String = "",
    val source: String = ""
)

@Serializable
data class RuleTestResponse(
    val profile: String = "",
    val decision: RuleTestDecisionPayload = RuleTestDecisionPayload(),
    val chain: RuleTestChainPayload? = null,
    val hops: List<ServerPayload> = emptyList()
)

@Serializable
data class RuleTestDecisionPayload(
    @SerialName("rule_name")
    val ruleName: String = "",
    @SerialName("rule_number")
    val ruleNumber: Int = 0,
    val action: String = "",
    @SerialName("chain_name")
    val chainName: String = "",
    @SerialName("group_name")
    val groupName: String = "",
    val target: String = "",
    @SerialName("target_host")
    val targetHost: String = "",
    @SerialName("target_port")
    val targetPort: String = "",
    val network: String = "",
    val source: String = "",
    @SerialName("default")
    val isDefault: Boolean = false,
    @SerialName("elapsed_ns")
    val elapsedNs: Long = 0
)

@Serializable
data class RuleTestChainPayload(
    val name: String = "",
    @SerialName("hop_count")
    val hopCount: Int = 0
)

@Serializable
data class ChainPayload(
    val name: String,
    val servers: List<ServerPayload>
)

@Serializable
data class ServerPayload(
    val name: String,
    val address: String,
    val protocol: String,
    val geo: LocationPayload = LocationPayload(),
    @SerialName("geo_error")
    val geoError: String? = null
)

@Serializable
data class LocationPayload(
    val country: String = "",
    @SerialName("country_code")
    val countryCode: String = "",
    val city: String = "",
    val latitude: Double = 0.0,
    val longitude: Double = 0.0
)

@Serializable
data class DaemonEvent(
    @SerialName("shard_id")
    val shardId: ULong,
    val lamport: ULong,
    @SerialName("ts_ns")
    val tsNs: Long,
    val type: String,
    val data: Map<String, JsonElement> = emptyMap()
)

@Serializable
data class TrafficSnapshotPayload(
    @SerialName("updated_ts_ns")
    val updatedTsNs: Long = 0,
    val summary: TrafficSummaryPayload = TrafficSummaryPayload(),
    val connections: List<TrafficConnectionPayload> = emptyList(),
    @SerialName("profile_context")
    val profileContext: TrafficProfileContextPayload = TrafficProfileContextPayload(),
    @SerialName("quick_filters")
    val quickFilters: List<TrafficQuickFilterPayload> = emptyList(),
    @SerialName("rule_hits")
    val ruleHits: List<TrafficRuleHitPayload> = emptyList(),
    @SerialName("block_decisions")
    val blockDecisions: List<TrafficBlockDecisionPayload> = emptyList(),
    @SerialName("cleanup_suggestions")
    val cleanupSuggestions: List<TrafficCleanupSuggestionPayload> = emptyList(),
    @SerialName("rule_suggestions")
    val ruleSuggestions: List<TrafficRuleSuggestionPayload> = emptyList(),
    val breakdowns: TrafficBreakdownsPayload = TrafficBreakdownsPayload()
)

@Serializable
data class TrafficBreakdownsPayload(
    val profiles: List<TrafficBreakdownRowPayload> = emptyList(),
    val chains: List<TrafficBreakdownRowPayload> = emptyList(),
    val rules: List<TrafficBreakdownRowPayload> = emptyList(),
    val actions: List<TrafficBreakdownRowPayload> = emptyList(),
    val networks: List<TrafficBreakdownRowPayload> = emptyList()
)

@Serializable
data class TrafficBreakdownRowPayload(
    val key: String = "",
    val label: String = "",
    val count: Int = 0,
    @SerialName("rx_total")
    val rxTotal: Long = 0,
    @SerialName("tx_total")
    val txTotal: Long = 0
)

@Serializable
data class TrafficProfileContextPayload(
    val active: String = "",
    val profiles: List<String> = emptyList()
)

@Serializable
data class TrafficQuickFilterPayload(
    val key: String = "",
    val label: String = "",
    val count: Int = 0
)

@Serializable
data class TrafficRuleHitPayload(
    val profile: String = "",
    @SerialName("rule_name")
    val ruleName: String = "",
    val action: String = "",
    val count: Int = 0,
    @SerialName("last_hit_ts_ns")
    val lastHitTsNs: Long = 0,
    @SerialName("rx_total")
    val rxTotal: Long = 0,
    @SerialName("tx_total")
    val txTotal: Long = 0,
    @SerialName("last_target")
    val lastTarget: String = "",
    @SerialName("default")
    val isDefault: Boolean = false
)

@Serializable
data class TrafficBlockDecisionPayload(
    @SerialName("conn_id")
    val connId: String = "",
    val profile: String = "",
    @SerialName("rule_name")
    val ruleName: String = "",
    val action: String = "",
    val target: String = "",
    @SerialName("target_host")
    val targetHost: String = "",
    @SerialName("target_port")
    val targetPort: String = "",
    val network: String = "",
    @SerialName("ts_ns")
    val tsNs: Long = 0,
    @SerialName("close_reason")
    val closeReason: String = ""
)

@Serializable
data class TrafficCleanupSuggestionPayload(
    val kind: String = "",
    val profile: String = "",
    @SerialName("rule_name")
    val ruleName: String = "",
    @SerialName("target_rule_name")
    val targetRuleName: String = "",
    val operation: String = "",
    val action: String = "",
    val message: String = "",
    val count: Int = 0,
    @SerialName("last_hit_ts_ns")
    val lastHitTsNs: Long = 0
)

@Serializable
data class TrafficRuleSuggestionPayload(
    val id: String = "",
    val kind: String = "",
    val profile: String = "",
    val action: String = "",
    @SerialName("draft_rule")
    val draftRule: RulePayload = RulePayload(),
    val count: Int = 0,
    @SerialName("last_seen_ts_ns")
    val lastSeenTsNs: Long = 0,
    @SerialName("sample_targets")
    val sampleTargets: List<String> = emptyList(),
    val confidence: String = "",
    val reason: String = ""
)

@Serializable
data class TrafficSummaryPayload(
    @SerialName("active_connections")
    val activeConnections: Int = 0,
    @SerialName("rx_bps")
    val rxBps: Double = 0.0,
    @SerialName("tx_bps")
    val txBps: Double = 0.0,
    @SerialName("rx_total")
    val rxTotal: Long = 0,
    @SerialName("tx_total")
    val txTotal: Long = 0,
    @SerialName("history_limit")
    val historyLimit: Int = 0,
    @SerialName("history_path")
    val historyPath: String = "",
    @SerialName("history_persisted")
    val historyPersisted: Boolean = false,
    @SerialName("persist_error")
    val persistError: String = ""
)

@Serializable
data class TrafficConnectionPayload(
    @SerialName("conn_id")
    val connId: String = "",
    val profile: String = "",
    val state: String = "",
    @SerialName("start_ts_ns")
    val startTsNs: Long = 0,
    @SerialName("updated_ts_ns")
    val updatedTsNs: Long = 0,
    @SerialName("end_ts_ns")
    val endTsNs: Long = 0,
    val listener: ListenerInfoPayload = ListenerInfoPayload(),
    @SerialName("client_addr")
    val clientAddr: String = "",
    @SerialName("chain_name")
    val chainName: String = "",
    @SerialName("group_name")
    val groupName: String = "",
    @SerialName("rule_name")
    val ruleName: String = "",
    @SerialName("rule_action")
    val ruleAction: String = "",
    @SerialName("default")
    val isDefault: Boolean = false,
    @SerialName("decision_ns")
    val decisionNs: Long = 0,
    val target: String = "",
    @SerialName("target_host")
    val targetHost: String = "",
    @SerialName("target_port")
    val targetPort: String = "",
    val network: String = "",
    val source: String = "",
    val application: String = "",
    val hops: List<TrafficHopPayload> = emptyList(),
    val timeline: List<TrafficTimelinePayload> = emptyList(),
    val visibility: TrafficVisibilityPayload? = null,
    val geo: LocationPayload = LocationPayload(),
    @SerialName("geo_error")
    val geoError: String = "",
    @SerialName("total_dial_ns")
    val totalDialNs: Long = 0,
    @SerialName("rx_bps")
    val rxBps: Double = 0.0,
    @SerialName("tx_bps")
    val txBps: Double = 0.0,
    @SerialName("rx_total")
    val rxTotal: Long = 0,
    @SerialName("tx_total")
    val txTotal: Long = 0,
    @SerialName("duration_ns")
    val durationNs: Long = 0,
    @SerialName("close_reason")
    val closeReason: String = ""
)

@Serializable
data class TrafficTimelinePayload(
    @SerialName("ts_ns")
    val tsNs: Long = 0,
    val type: String = "",
    val title: String = "",
    val detail: String = ""
)

@Serializable
data class TrafficVisibilityPayload(
    val kind: String = "",
    val method: String = "",
    val scheme: String = "",
    val host: String = "",
    val port: String = "",
    val path: String = "",
    @SerialName("query_type")
    val queryType: String = ""
)

@Serializable
data class ListenerInfoPayload(
    val protocol: String = "",
    val addr: String = ""
)

@Serializable
data class TrafficHopPayload(
    val index: Int = 0,
    val name: String = "",
    val protocol: String = "",
    val address: String = "",
    val state: String = "",
    @SerialName("elapsed_ns")
    val elapsedNs: Long = 0,
    val error: String = ""
)

@Serializable
data class DeveloperStatusPayload(
    val enabled: Boolean = false,
    @SerialName("mitm_enabled")
    val mitmEnabled: Boolean = false,
    @SerialName("capture_limit")
    val captureLimit: Int = 0,
    @SerialName("body_limit_bytes")
    val bodyLimitBytes: Long = 0,
    @SerialName("header_value_limit_bytes")
    val headerValueLimitBytes: Int = 0,
    @SerialName("ca_cert_path")
    val caCertPath: String = "",
    @SerialName("ca_fingerprint_sha256")
    val caFingerprintSha256: String = "",
    @SerialName("capture_count")
    val captureCount: Int = 0
)

@Serializable
data class DeveloperEntriesPayload(
    val entries: List<DeveloperEntryPayload> = emptyList()
)

@Serializable
data class DeveloperEntryPayload(
    val id: String = "",
    @SerialName("conn_id")
    val connId: String = "",
    val profile: String = "",
    @SerialName("client_addr")
    val clientAddr: String = "",
    @SerialName("chain_name")
    val chainName: String = "",
    @SerialName("started_at")
    val startedAt: String = "",
    @SerialName("finished_at")
    val finishedAt: String = "",
    val method: String = "",
    val url: String = "",
    val scheme: String = "",
    val host: String = "",
    val status: Int = 0,
    val request: DeveloperMessagePayload = DeveloperMessagePayload(),
    val response: DeveloperMessagePayload = DeveloperMessagePayload(),
    val error: String = ""
)

@Serializable
data class DeveloperMessagePayload(
    val headers: List<DeveloperHeaderPayload> = emptyList(),
    val body: DeveloperBodyPayload = DeveloperBodyPayload()
)

@Serializable
data class DeveloperHeaderPayload(
    val name: String = "",
    val value: String = "",
    val redacted: Boolean = false,
    val truncated: Boolean = false
)

@Serializable
data class DeveloperBodyPayload(
    val size: Long = 0,
    val preview: String = "",
    @SerialName("preview_bytes")
    val previewBytes: Long = 0,
    val truncated: Boolean = false,
    @SerialName("truncated_after")
    val truncatedAfter: Long = 0
)

data class BandwidthSample(
    val rxBps: Double = 0.0,
    val txBps: Double = 0.0
)

fun JsonElement.stringValueOrNull(): String? {
    val primitive = this as? JsonPrimitive ?: return null
    if (primitive is JsonNull) {
        return null
    }
    return primitive.content
}

fun JsonElement.doubleValueOrNull(): Double? {
    val primitive = this as? JsonPrimitive ?: return null
    return primitive.doubleOrNull
        ?: primitive.content.toDoubleOrNull()
        ?: primitive.booleanOrNull?.let { if (it) 1.0 else 0.0 }
}
