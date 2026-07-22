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

class ApiHttpException(val statusCode: Int, val body: String) : IOException(body.ifBlank { statusCode.toString() })

interface ClambhookApi {
    suspend fun status(): StatusPayload
    suspend fun profiles(): ProfilesPayload
    suspend fun servers(): ServersPayload
    suspend fun rules(): RulesPayload
    suspend fun traffic(): TrafficSnapshotPayload
    suspend fun connect()
    suspend fun disconnect()
    suspend fun setActiveProfile(name: String)
    suspend fun createRule(rule: RulePayload): RulesPayload
    suspend fun createRuleFromConnection(connection: TrafficConnectionPayload, rule: RulePayload): RulesPayload
    suspend fun cleanupRule(suggestion: TrafficCleanupSuggestionPayload): RulesPayload
    suspend fun policyGroups(): PolicyGroupsPayload
    suspend fun selectPolicyGroup(group: String, chain: String): PolicyGroupsPayload
    suspend fun testPolicyGroups(group: String): PolicyGroupsPayload
    suspend fun pendingPrompts(): PromptsPayload
    suspend fun resolvePrompt(id: String, action: String, scope: String, matchHost: Boolean)
    suspend fun dns(): DnsPayload
    suspend fun developerStatus(): DeveloperStatusPayload
    suspend fun setDeveloperCapture(enabled: Boolean): DeveloperStatusPayload
    suspend fun developerEntries(): List<DeveloperEntryPayload>
    suspend fun developerEntry(id: String): DeveloperEntryPayload
    suspend fun repeatDeveloperEntry(id: String): DeveloperEntryPayload

    fun eventsUri(): String
    fun authorizationHeader(): String
    fun configureBaseUrl(baseUrl: String)
}

class ClambhookApiClient(
    private var baseUrl: String,
    private val tokenProvider: () -> String
) : ClambhookApi {
    private val client = OkHttpClient()

    init { baseUrl = normalizeBaseUrl(baseUrl) }

    override fun configureBaseUrl(baseUrl: String) { this.baseUrl = normalizeBaseUrl(baseUrl) }

    override fun eventsUri(): String {
        val scheme = if (baseUrl.startsWith("https://")) "wss://" else "ws://"
        val hostAndPath = baseUrl.replace("https://", "").replace("http://", "")
        return "$scheme$hostAndPath/api/v1/events?types=connection.*,rule.*,hop.*,log.*"
    }

    override fun authorizationHeader(): String {
        val token = tokenProvider().trim()
        return if (token.isEmpty()) "" else "Bearer $token"
    }

    override suspend fun status(): StatusPayload =
        StatusPayload.serializer() sendGet "/api/v1/status"
    override suspend fun profiles(): ProfilesPayload =
        ProfilesPayload.serializer() sendGet "/api/v1/profiles"
    override suspend fun servers(): ServersPayload =
        ServersPayload.serializer() sendGet "/api/v1/servers"
    override suspend fun rules(): RulesPayload =
        RulesPayload.serializer() sendGet "/api/v1/rules"
    override suspend fun traffic(): TrafficSnapshotPayload =
        TrafficSnapshotPayload.serializer() sendGet "/api/v1/traffic?limit=200"
    override suspend fun connect() { sendUnit("POST", "/api/v1/connect") }
    override suspend fun disconnect() { sendUnit("POST", "/api/v1/disconnect") }
    override suspend fun setActiveProfile(name: String) {
        sendUnit("PUT", "/api/v1/profiles/active", buildJsonObject { put("name", name) })
    }
    override suspend fun createRule(rule: RulePayload): RulesPayload =
        RulesPayload.serializer() sendPost ("/api/v1/rules" to buildJsonObject {
            putJsonObject("rule") { ruleToJson(rule, this) }
            put("position", "append")
        })
    override suspend fun createRuleFromConnection(connection: TrafficConnectionPayload, rule: RulePayload): RulesPayload =
        RulesPayload.serializer() sendPost ("/api/v1/rules/from-connection" to buildJsonObject {
            put("conn_id", connection.connId)
            put("profile", connection.profile)
            put("name", rule.name)
            put("action", rule.action)
            put("scope", "auto")
            put("position", "append")
        })
    override suspend fun cleanupRule(suggestion: TrafficCleanupSuggestionPayload): RulesPayload =
        RulesPayload.serializer() sendPost ("/api/v1/rules/cleanup" to buildJsonObject {
            put("profile", suggestion.profile)
            put("kind", suggestion.kind)
            put("rule_name", suggestion.ruleName)
            val target = if (suggestion.targetRuleName.isEmpty()) suggestion.ruleName else suggestion.targetRuleName
            put("target_rule_name", target)
            put("operation", suggestion.operation)
        })
    override suspend fun policyGroups(): PolicyGroupsPayload =
        PolicyGroupsPayload.serializer() sendGet "/api/v1/policy-groups"
    override suspend fun selectPolicyGroup(group: String, chain: String): PolicyGroupsPayload {
        sendUnit("PUT", "/api/v1/policy-groups/selection", buildJsonObject { put("group", group); put("chain", chain) })
        return policyGroups()
    }
    override suspend fun testPolicyGroups(group: String): PolicyGroupsPayload =
        PolicyGroupsPayload.serializer() sendPost ("/api/v1/policy-groups/test" to buildJsonObject { put("group", group) })
    override suspend fun pendingPrompts(): PromptsPayload =
        PromptsPayload.serializer() sendGet "/api/v1/prompts/pending"
    override suspend fun resolvePrompt(id: String, action: String, scope: String, matchHost: Boolean) {
        sendUnit("POST", "/api/v1/prompts/${java.net.URLEncoder.encode(id, "UTF-8")}/resolve", buildJsonObject {
            put("action", action); put("scope", scope); put("match_host", matchHost)
        })
    }
    override suspend fun dns(): DnsPayload =
        DnsPayload.serializer() sendGet "/api/v1/dns"
    override suspend fun developerStatus(): DeveloperStatusPayload =
        DeveloperStatusPayload.serializer() sendGet "/api/v1/developer/status"
    override suspend fun setDeveloperCapture(enabled: Boolean): DeveloperStatusPayload {
        sendUnit("PUT", "/api/v1/developer/settings", buildJsonObject { put("enabled", enabled) })
        return developerStatus()
    }
    override suspend fun developerEntries(): List<DeveloperEntryPayload> =
        withContext(Dispatchers.IO) {
            val body = send("GET", "/api/v1/developer/entries")
            ApiJson.decodeFromString(DeveloperEntriesPayload.serializer(), body).entries
        }
    override suspend fun developerEntry(id: String): DeveloperEntryPayload =
        DeveloperEntryPayload.serializer() sendGet "/api/v1/developer/entries/${java.net.URLEncoder.encode(id, "UTF-8")}"
    override suspend fun repeatDeveloperEntry(id: String): DeveloperEntryPayload =
        DeveloperEntryPayload.serializer() sendPost ("/api/v1/developer/repeat" to buildJsonObject { put("entry_id", id) })

    private fun normalizeBaseUrl(value: String): String {
        var normalized = value.trim()
        while (normalized.endsWith("/")) normalized = normalized.dropLast(1)
        return normalized
    }

    private fun buildUri(path: String): String {
        val normalizedPath = if (path.startsWith("/")) path else "/$path"
        return baseUrl + normalizedPath
    }

    private suspend fun send(method: String, path: String, body: JsonObject? = null): String =
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

    private suspend fun sendUnit(method: String, path: String, body: JsonObject? = null) {
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

private fun ruleToJson(rule: RulePayload, obj: kotlinx.serialization.json.JsonObjectBuilder) {
    obj.put("name", rule.name)
    obj.put("action", rule.action)
    if (rule.domains.isNotEmpty()) obj.putJsonArray("domains") { rule.domains.forEach { add(JsonPrimitive(it)) } }
    if (rule.domainSuffixes.isNotEmpty()) obj.putJsonArray("domain_suffixes") { rule.domainSuffixes.forEach { add(JsonPrimitive(it)) } }
    if (rule.domainKeywords.isNotEmpty()) obj.putJsonArray("domain_keywords") { rule.domainKeywords.forEach { add(JsonPrimitive(it)) } }
    if (rule.cidrs.isNotEmpty()) obj.putJsonArray("cidrs") { rule.cidrs.forEach { add(JsonPrimitive(it)) } }
    if (rule.ports.isNotEmpty()) obj.putJsonArray("ports") { rule.ports.forEach { add(JsonPrimitive(it)) } }
    if (rule.networks.isNotEmpty()) obj.putJsonArray("networks") { rule.networks.forEach { add(JsonPrimitive(it)) } }
}