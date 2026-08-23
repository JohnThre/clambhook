package com.clambhook.linux.api

import com.clambhook.linux.model.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import kotlinx.serialization.json.putJsonArray
import kotlinx.serialization.json.putJsonObject
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException
import java.net.URI

class ApiHttpException(
    val statusCode: Int,
    val body: String,
) : IOException(body.ifBlank { statusCode.toString() })

interface ClambhookApi {
    suspend fun status(): StatusPayload

    suspend fun profiles(): ProfilesPayload

    suspend fun servers(): ServersPayload

    suspend fun rules(): RulesPayload

    suspend fun traffic(): TrafficSnapshotPayload

    suspend fun traffic(filter: TrafficMonitorFilter): TrafficSnapshotPayload

    suspend fun connect()

    suspend fun disconnect()

    suspend fun setActiveProfile(name: String)

    suspend fun createRule(rule: RulePayload): RulesPayload

    suspend fun createRuleFromConnection(
        connection: TrafficConnectionPayload,
        rule: RulePayload,
    ): RulesPayload

    suspend fun cleanupRule(suggestion: TrafficCleanupSuggestionPayload): RulesPayload

    suspend fun policyGroups(): PolicyGroupsPayload

    suspend fun selectPolicyGroup(
        group: String,
        chain: String,
    ): PolicyGroupsPayload

    suspend fun testPolicyGroups(group: String): PolicyGroupsPayload

    suspend fun pendingPrompts(): PromptsPayload

    suspend fun resolvePrompt(
        id: String,
        action: String,
        scope: String,
        matchHost: Boolean,
        matchPort: Boolean = false,
        matchProtocol: Boolean = false,
    )

    suspend fun silentDecisions(): SilentDecisionsPayload

    suspend fun promoteSilentDecision(
        id: String,
        scope: String,
        matchHost: Boolean = false,
        matchPort: Boolean = false,
        matchProtocol: Boolean = false,
    )

    suspend fun dns(): DnsPayload

    suspend fun developerStatus(): DeveloperStatusPayload

    suspend fun setDeveloperCapture(enabled: Boolean): DeveloperStatusPayload

    suspend fun developerEntries(): List<DeveloperEntryPayload>

    suspend fun developerEntries(filter: DeveloperEntriesFilter): List<DeveloperEntryPayload>

    suspend fun developerEntry(id: String): DeveloperEntryPayload

    suspend fun repeatDeveloperEntry(id: String): DeveloperEntryPayload

    suspend fun developerEntryCurl(id: String): String

    suspend fun importCurl(text: String): ParsedCurlResponse

    suspend fun sendComposed(request: ComposedRequestPayload): DeveloperEntryPayload

    suspend fun conditioner(profile: String = ""): ConditionerPayload

    suspend fun updateConditioner(request: ConditionerUpdateRequest): ConditionerPayload

    fun eventsUri(): String

    fun authorizationHeader(): String

    fun configureBaseUrl(baseUrl: String)
}

class ClambhookApiClient(
    baseUrl: String,
    private val tokenProvider: () -> String,
) : ClambhookApi {
    private val client = OkHttpClient()
    private val baseUrlRef =
        java.util.concurrent.atomic
            .AtomicReference(normalizeBaseUrl(baseUrl))
    private val baseUrl: String get() = baseUrlRef.get()

    init {
        baseUrlRef.set(normalizeBaseUrl(baseUrl))
    }

    override fun configureBaseUrl(baseUrl: String) {
        baseUrlRef.set(normalizeBaseUrl(baseUrl))
    }

    override fun eventsUri(): String {
        val current = baseUrlRef.get()
        val scheme = if (current.startsWith("https://")) "wss://" else "ws://"
        val authorityAndPath = current.replace("https://", "").replace("http://", "")
        val uri = URI.create("http://$authorityAndPath")
        val host = if (uri.host == null) authorityAndPath else uri.host
        val port = uri.port
        val portPart = if (port == -1) "" else ":$port"
        val path = uri.path?.trim('/')?.let { if (it.isEmpty()) "" else "/$it" } ?: ""
        return "$scheme$host$portPart$path/api/v1/events?types=connection.*,rule.*,hop.*,log.*"
    }

    override fun authorizationHeader(): String {
        val token = tokenProvider().trim()
        return if (token.isEmpty()) "" else "Bearer $token"
    }

    override suspend fun status(): StatusPayload = StatusPayload.serializer() sendGet "/api/v1/status"

    override suspend fun profiles(): ProfilesPayload = ProfilesPayload.serializer() sendGet "/api/v1/profiles"

    override suspend fun servers(): ServersPayload = ServersPayload.serializer() sendGet "/api/v1/servers"

    override suspend fun rules(): RulesPayload = RulesPayload.serializer() sendGet "/api/v1/rules"

    override suspend fun traffic(): TrafficSnapshotPayload = TrafficSnapshotPayload.serializer() sendGet "/api/v1/traffic?limit=200"

    override suspend fun traffic(filter: TrafficMonitorFilter): TrafficSnapshotPayload {
        val qs = filter.queryPairs().joinToString("&") { (k, v) -> "$k=${java.net.URLEncoder.encode(v, "UTF-8")}" }
        return TrafficSnapshotPayload.serializer() sendGet "/api/v1/traffic?$qs"
    }

    override suspend fun connect() {
        sendUnit("POST", "/api/v1/connect")
    }

    override suspend fun disconnect() {
        sendUnit("POST", "/api/v1/disconnect")
    }

    override suspend fun setActiveProfile(name: String) {
        sendUnit("PUT", "/api/v1/profiles/active", buildJsonObject { put("name", name) })
    }

    override suspend fun createRule(rule: RulePayload): RulesPayload =
        RulesPayload.serializer() sendPost (
            "/api/v1/rules" to
                buildJsonObject {
                    putJsonObject("rule") { ruleToJson(rule, this) }
                    put("position", "append")
                }
        )

    override suspend fun createRuleFromConnection(
        connection: TrafficConnectionPayload,
        rule: RulePayload,
    ): RulesPayload =
        RulesPayload.serializer() sendPost (
            "/api/v1/rules/from-connection" to
                buildJsonObject {
                    put("conn_id", connection.connId)
                    put("profile", connection.profile)
                    put("name", rule.name)
                    put("action", rule.action)
                    put("scope", "auto")
                    put("position", "append")
                }
        )

    override suspend fun cleanupRule(suggestion: TrafficCleanupSuggestionPayload): RulesPayload =
        RulesPayload.serializer() sendPost (
            "/api/v1/rules/cleanup" to
                buildJsonObject {
                    put("profile", suggestion.profile)
                    put("kind", suggestion.kind)
                    put("rule_name", suggestion.ruleName)
                    val target = if (suggestion.targetRuleName.isEmpty()) suggestion.ruleName else suggestion.targetRuleName
                    put("target_rule_name", target)
                    put("operation", suggestion.operation)
                }
        )

    override suspend fun policyGroups(): PolicyGroupsPayload = PolicyGroupsPayload.serializer() sendGet "/api/v1/policy-groups"

    override suspend fun selectPolicyGroup(
        group: String,
        chain: String,
    ): PolicyGroupsPayload {
        sendUnit(
            "PUT",
            "/api/v1/policy-groups/selection",
            buildJsonObject {
                put("group", group)
                put("chain", chain)
            },
        )
        return policyGroups()
    }

    override suspend fun testPolicyGroups(group: String): PolicyGroupsPayload =
        PolicyGroupsPayload.serializer() sendPost ("/api/v1/policy-groups/test" to buildJsonObject { put("group", group) })

    override suspend fun pendingPrompts(): PromptsPayload = PromptsPayload.serializer() sendGet "/api/v1/prompts/pending"

    override suspend fun resolvePrompt(
        id: String,
        action: String,
        scope: String,
        matchHost: Boolean,
        matchPort: Boolean,
        matchProtocol: Boolean,
    ) {
        sendUnit(
            "POST",
            "/api/v1/prompts/${java.net.URLEncoder.encode(id, "UTF-8")}/resolve",
            buildJsonObject {
                put("action", action)
                put("scope", scope)
                put("match_host", matchHost)
                put("match_port", matchPort)
                put("match_protocol", matchProtocol)
            },
        )
    }

    override suspend fun silentDecisions(): SilentDecisionsPayload = SilentDecisionsPayload.serializer() sendGet "/api/v1/prompts/decisions"

    override suspend fun promoteSilentDecision(
        id: String,
        scope: String,
        matchHost: Boolean,
        matchPort: Boolean,
        matchProtocol: Boolean,
    ) {
        sendUnit(
            "POST",
            "/api/v1/prompts/${java.net.URLEncoder.encode(id, "UTF-8")}/promote",
            buildJsonObject {
                put("scope", scope)
                put("match_host", matchHost)
                put("match_port", matchPort)
                put("match_protocol", matchProtocol)
            },
        )
    }

    override suspend fun dns(): DnsPayload = DnsPayload.serializer() sendGet "/api/v1/dns"

    override suspend fun developerStatus(): DeveloperStatusPayload = DeveloperStatusPayload.serializer() sendGet "/api/v1/developer/status"

    override suspend fun setDeveloperCapture(enabled: Boolean): DeveloperStatusPayload {
        sendUnit("PUT", "/api/v1/developer/settings", buildJsonObject { put("enabled", enabled) })
        return developerStatus()
    }

    override suspend fun developerEntries(): List<DeveloperEntryPayload> = developerEntries(DeveloperEntriesFilter())

    override suspend fun developerEntries(filter: DeveloperEntriesFilter): List<DeveloperEntryPayload> =
        withContext(Dispatchers.IO) {
            val body = send("GET", filter.entriesPath())
            ApiJson.decodeFromString(DeveloperEntriesPayload.serializer(), body).entries
        }

    override suspend fun developerEntryCurl(id: String): String =
        (CurlExportResponse.serializer() sendGet "/api/v1/developer/entries/${java.net.URLEncoder.encode(id, "UTF-8")}/curl").curl

    override suspend fun importCurl(text: String): ParsedCurlResponse =
        ParsedCurlResponse.serializer() sendPost ("/api/v1/developer/curl/import" to buildJsonObject { put("curl", text) })

    override suspend fun sendComposed(request: ComposedRequestPayload): DeveloperEntryPayload =
        withContext(Dispatchers.IO) {
            val body =
                buildJsonObject {
                    put("method", request.method)
                    put("url", request.url)
                    if (request.body != null) put("body", request.body)
                    if (request.headers.isNotEmpty()) {
                        putJsonArray("headers") {
                            request.headers.forEach { h ->
                                add(
                                    buildJsonObject {
                                        put("name", h.name)
                                        put("value", h.value)
                                    },
                                )
                            }
                        }
                    }
                }
            ApiJson.decodeFromString(DeveloperRepeatResponsePayload.serializer(), send("POST", "/api/v1/developer/send", body)).entry
        }

    override suspend fun developerEntry(id: String): DeveloperEntryPayload =
        DeveloperEntryPayload.serializer() sendGet "/api/v1/developer/entries/${java.net.URLEncoder.encode(id, "UTF-8")}"

    override suspend fun repeatDeveloperEntry(id: String): DeveloperEntryPayload =
        DeveloperEntryPayload.serializer() sendPost ("/api/v1/developer/repeat" to buildJsonObject { put("entry_id", id) })

    override suspend fun conditioner(profile: String): ConditionerPayload {
        val path =
            if (profile.isBlank()) {
                "/api/v1/conditioner"
            } else {
                "/api/v1/conditioner?profile=${java.net.URLEncoder.encode(profile, "UTF-8")}"
            }
        return ConditionerPayload.serializer() sendGet path
    }

    override suspend fun updateConditioner(request: ConditionerUpdateRequest): ConditionerPayload =
        withContext(Dispatchers.IO) {
            val body =
                buildJsonObject {
                    request.profile?.let { put("profile", it) }
                    request.enabled?.let { put("enabled", it) }
                    request.downloadKbps?.let { put("download_kbps", it) }
                    request.uploadKbps?.let { put("upload_kbps", it) }
                    request.latency?.let { put("latency", it) }
                    request.jitter?.let { put("jitter", it) }
                    request.lossPercent?.let { put("loss_percent", it) }
                }
            ApiJson.decodeFromString(ConditionerPayload.serializer(), send("PUT", "/api/v1/conditioner", body))
        }

    private fun normalizeBaseUrl(value: String): String {
        var normalized = value.trim()
        while (normalized.endsWith("/")) normalized = normalized.dropLast(1)
        return normalized
    }

    private fun buildUri(path: String): String {
        val normalizedPath = if (path.startsWith("/")) path else "/$path"
        return baseUrl + normalizedPath
    }

    private suspend fun send(
        method: String,
        path: String,
        body: JsonObject? = null,
    ): String =
        withContext(Dispatchers.IO) {
            val builder = Request.Builder().url(buildUri(path)).method(method, body?.let { it.toString().toRequestBody(JSON) })
            val auth = authorizationHeader()
            if (auth.isNotEmpty()) builder.header("Authorization", auth)
            client.newCall(builder.build()).execute().use { resp ->
                val respBody = resp.body?.string().orEmpty()
                if (!resp.isSuccessful) throw ApiHttpException(resp.code, respBody)
                respBody
            }
        }

    private suspend fun sendUnit(
        method: String,
        path: String,
        body: JsonObject? = null,
    ) {
        send(method, path, body)
    }

    private suspend infix fun <T> kotlinx.serialization.KSerializer<T>.sendGet(path: String): T =
        ApiJson.decodeFromString(this, send("GET", path))

    private suspend infix fun <T> kotlinx.serialization.KSerializer<T>.sendPost(pathBody: Pair<String, JsonObject>): T =
        ApiJson.decodeFromString(this, send("POST", pathBody.first, pathBody.second))

    companion object {
        private val JSON = "application/json".toMediaType()
    }
}

private fun ruleToJson(
    rule: RulePayload,
    obj: kotlinx.serialization.json.JsonObjectBuilder,
) {
    obj.put("name", rule.name)
    obj.put("action", rule.action)
    if (rule.domains.isNotEmpty()) obj.putJsonArray("domains") { rule.domains.forEach { add(JsonPrimitive(it)) } }
    if (rule.domainSuffixes.isNotEmpty()) obj.putJsonArray("domain_suffixes") { rule.domainSuffixes.forEach { add(JsonPrimitive(it)) } }
    if (rule.domainKeywords.isNotEmpty()) obj.putJsonArray("domain_keywords") { rule.domainKeywords.forEach { add(JsonPrimitive(it)) } }
    if (rule.cidrs.isNotEmpty()) obj.putJsonArray("cidrs") { rule.cidrs.forEach { add(JsonPrimitive(it)) } }
    if (rule.ports.isNotEmpty()) obj.putJsonArray("ports") { rule.ports.forEach { add(JsonPrimitive(it)) } }
    if (rule.networks.isNotEmpty()) obj.putJsonArray("networks") { rule.networks.forEach { add(JsonPrimitive(it)) } }
}
