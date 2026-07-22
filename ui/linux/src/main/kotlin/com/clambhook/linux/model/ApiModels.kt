package com.clambhook.linux.model

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.doubleOrNull
import kotlinx.serialization.json.jsonPrimitive

val ApiJson = Json {
    ignoreUnknownKeys = true
    coerceInputValues = true
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
    val protocol: String = "",
    val addr: String = "",
    @SerialName("active_conns") val activeConns: Int = 0
)

@Serializable
data class ProfilesPayload(
    val profiles: List<String> = emptyList(),
    val active: String = ""
)

@Serializable
data class LocationPayload(
    val country: String = "",
    @SerialName("country_code") val countryCode: String = "",
    val city: String = "",
    val latitude: Double = 0.0,
    val longitude: Double = 0.0
)

@Serializable
data class ServerPayload(
    val name: String = "",
    val address: String = "",
    val protocol: String = "",
    val geo: LocationPayload = LocationPayload(),
    @SerialName("geo_error") val geoError: String = ""
)

@Serializable
data class ChainPayload(
    val name: String = "",
    val servers: List<ServerPayload> = emptyList()
)

@Serializable
data class ServersPayload(
    val profile: String = "",
    val chains: List<ChainPayload> = emptyList()
)

@Serializable
data class RulePayload(
    val name: String = "",
    val action: String = "",
    val domains: List<String> = emptyList(),
    @SerialName("domain_suffixes") val domainSuffixes: List<String> = emptyList(),
    @SerialName("domain_keywords") val domainKeywords: List<String> = emptyList(),
    val cidrs: List<String> = emptyList(),
    val ports: List<Int> = emptyList(),
    val networks: List<String> = emptyList()
)

@Serializable
data class RulesPayload(
    val profile: String = "",
    val rules: List<RulePayload> = emptyList()
)

@Serializable
data class BandwidthSample(
    @SerialName("rx_bps") val rxBps: Double = 0.0,
    @SerialName("tx_bps") val txBps: Double = 0.0
)

@Serializable
data class DaemonEvent(
    @SerialName("shard_id") val shardId: ULong = 0u,
    val lamport: ULong = 0u,
    @SerialName("ts_ns") val tsNs: Long = 0,
    val type: String = "",
    val data: Map<String, JsonElement> = emptyMap()
) {
    fun doubleData(key: String): Double = data[key]?.jsonPrimitive?.doubleOrNull ?: 0.0
    fun stringData(key: String): String = try { data[key]?.jsonPrimitive?.content ?: "" } catch (e: Exception) { "" }
}

@Serializable
data class TrafficSummaryPayload(
    @SerialName("active_connections") val activeConnections: Int = 0,
    @SerialName("rx_bps") val rxBps: Double = 0.0,
    @SerialName("tx_bps") val txBps: Double = 0.0,
    @SerialName("rx_total") val rxTotal: ULong = 0u,
    @SerialName("tx_total") val txTotal: ULong = 0u,
    @SerialName("history_limit") val historyLimit: Int = 0,
    @SerialName("history_path") val historyPath: String = "",
    @SerialName("history_persisted") val historyPersisted: Boolean = false,
    @SerialName("persist_error") val persistError: String = ""
)

@Serializable
data class TrafficListenerPayload(
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
    @SerialName("elapsed_ns") val elapsedNs: Long = 0,
    val error: String = ""
)

@Serializable
data class TrafficConnectionPayload(
    @SerialName("conn_id") val connId: String = "",
    val profile: String = "",
    val state: String = "",
    @SerialName("start_ts_ns") val startTsNs: Long = 0,
    @SerialName("updated_ts_ns") val updatedTsNs: Long = 0,
    @SerialName("end_ts_ns") val endTsNs: Long = 0,
    val listener: TrafficListenerPayload = TrafficListenerPayload(),
    @SerialName("client_addr") val clientAddr: String = "",
    @SerialName("chain_name") val chainName: String = "",
    @SerialName("rule_name") val ruleName: String = "",
    @SerialName("rule_action") val ruleAction: String = "",
    @SerialName("decision_ns") val decisionNs: Long = 0,
    val target: String = "",
    @SerialName("target_host") val targetHost: String = "",
    @SerialName("target_port") val targetPort: String = "",
    val network: String = "",
    val application: String = "",
    val hops: List<TrafficHopPayload> = emptyList(),
    val geo: LocationPayload = LocationPayload(),
    @SerialName("geo_error") val geoError: String = "",
    @SerialName("total_dial_ns") val totalDialNs: Long = 0,
    @SerialName("rx_bps") val rxBps: Double = 0.0,
    @SerialName("tx_bps") val txBps: Double = 0.0,
    @SerialName("rx_total") var rxTotal: ULong = 0u,
    @SerialName("tx_total") var txTotal: ULong = 0u,
    @SerialName("duration_ns") val durationNs: Long = 0,
    @SerialName("close_reason") val closeReason: String = ""
)

@Serializable
data class TrafficCleanupSuggestionPayload(
    val kind: String = "",
    val profile: String = "",
    @SerialName("rule_name") val ruleName: String = "",
    @SerialName("target_rule_name") val targetRuleName: String = "",
    val operation: String = "",
    val action: String = "",
    val message: String = ""
)

@Serializable
data class TrafficRuleSuggestionPayload(
    val id: String = "",
    val kind: String = "",
    val profile: String = "",
    val action: String = "",
    @SerialName("draft_rule") val draftRule: RulePayload = RulePayload(),
    val count: Int = 0,
    @SerialName("last_seen_ts_ns") val lastSeenTsNs: Long = 0,
    @SerialName("sample_targets") val sampleTargets: List<String> = emptyList(),
    val confidence: String = "",
    val reason: String = ""
)

@Serializable
data class TrafficSnapshotPayload(
    @SerialName("updated_ts_ns") val updatedTsNs: Long = 0,
    val summary: TrafficSummaryPayload = TrafficSummaryPayload(),
    val connections: List<TrafficConnectionPayload> = emptyList(),
    @SerialName("cleanup_suggestions") val cleanupSuggestions: List<TrafficCleanupSuggestionPayload> = emptyList(),
    @SerialName("rule_suggestions") val ruleSuggestions: List<TrafficRuleSuggestionPayload> = emptyList()
)

@Serializable
data class PolicyProbeResultPayload(
    @SerialName("chain_name") val chainName: String = "",
    val healthy: Boolean = false,
    @SerialName("latency_ns") val latencyNs: Long = 0,
    @SerialName("status_code") val statusCode: Int = 0,
    val error: String = "",
    @SerialName("udp_capable") val udpCapable: Boolean = false
)

@Serializable
data class PolicyGroupPayload(
    val name: String = "",
    val type: String = "",
    val selected: String = "",
    @SerialName("selected_chain") val selectedChain: String = "",
    @SerialName("selection_mode") val selectionMode: String = "",
    val hidden: Boolean = false,
    val chains: List<String> = emptyList(),
    val results: List<PolicyProbeResultPayload> = emptyList()
) {
    fun isSelect(): Boolean = type.equals("select", ignoreCase = true)
    fun activeChain(): String = if (selectedChain.isNotEmpty()) selectedChain else selected
    fun resultFor(chain: String): PolicyProbeResultPayload? = results.firstOrNull { it.chainName == chain }
}

@Serializable
data class PolicyGroupsPayload(
    val profile: String = "",
    val groups: List<PolicyGroupPayload> = emptyList()
)

@Serializable
data class PromptPayload(
    val id: String = "",
    @SerialName("conn_id") val connId: String = "",
    val profile: String = "",
    val network: String = "",
    val target: String = "",
    @SerialName("target_host") val targetHost: String = "",
    @SerialName("process_name") val processName: String = "",
    @SerialName("process_path") val processPath: String = "",
    val pid: Int = 0,
    val waiters: Int = 0
)

@Serializable
data class PromptsPayload(
    val prompts: List<PromptPayload> = emptyList()
)

@Serializable
data class DnsUpstreamPayload(
    val name: String = "",
    val protocol: String = "",
    val url: String = "",
    val address: String = "",
    @SerialName("server_name") val serverName: String = ""
) {
    fun endpoint(): String = when {
        url.isNotEmpty() -> url
        address.isNotEmpty() -> address
        else -> serverName
    }
}

@Serializable
data class DnsRoutePayload(
    val name: String = "",
    val protocol: String = "",
    val target: String = "",
    val action: String = "",
    @SerialName("chain_name") val chainName: String = "",
    val error: String = ""
)

@Serializable
data class DnsPayload(
    val profile: String = "",
    val strategy: String = "route",
    val enabled: Boolean = false,
    val timeout: String = "",
    @SerialName("intercepts_port_53") val interceptsPort53: Boolean = false,
    val upstreams: List<DnsUpstreamPayload> = emptyList(),
    @SerialName("upstream_routes") val upstreamRoutes: List<DnsRoutePayload> = emptyList()
)

@Serializable
data class DeveloperStatusPayload(
    val enabled: Boolean = false,
    @SerialName("mitm_enabled") val mitmEnabled: Boolean = false,
    @SerialName("no_cache_enabled") val noCacheEnabled: Boolean = false,
    @SerialName("capture_limit") val captureLimit: Int = 0,
    @SerialName("body_limit_bytes") val bodyLimitBytes: Long = 0,
    @SerialName("capture_count") val captureCount: Int = 0,
    @SerialName("ca_cert_path") val caCertPath: String = "",
    @SerialName("ca_fingerprint_sha256") val caFingerprintSha256: String = ""
)

@Serializable
data class CapturedHeaderPayload(
    val name: String = "",
    val value: String = "",
    val redacted: Boolean = false,
    val truncated: Boolean = false
)

@Serializable
data class CapturedBodyPayload(
    val size: Long = 0,
    val preview: String = "",
    @SerialName("preview_base64") val previewBase64: String = "",
    @SerialName("preview_bytes") val previewBytes: Long = 0,
    val truncated: Boolean = false,
    @SerialName("truncated_after") val truncatedAfter: Long = 0,
    @SerialName("mime_type") val mimeType: String = "",
    val encoding: String = ""
)

@Serializable
data class CapturedMessagePayload(
    val headers: List<CapturedHeaderPayload> = emptyList(),
    val body: CapturedBodyPayload = CapturedBodyPayload()
) {
    fun headersText(): String = headers.joinToString("\n") { "${it.name}: ${it.value}" }
    fun bodyText(): String = if (body.preview.isNotEmpty()) body.preview else body.previewBase64
}

@Serializable
data class DeveloperEntryPayload(
    val id: String = "",
    val method: String = "",
    val url: String = "",
    val host: String = "",
    val status: Int = 0,
    @SerialName("status_code") val statusCodeAlt: Int = 0,
    @SerialName("response_bytes") val responseBytes: Long = 0,
    val error: String = "",
    val request: CapturedMessagePayload = CapturedMessagePayload(),
    val response: CapturedMessagePayload = CapturedMessagePayload()
) {
    val statusCode: Int get() = if (status != 0) status else statusCodeAlt
}

@Serializable
data class DeveloperEntriesPayload(
    val entries: List<DeveloperEntryPayload> = emptyList()
)