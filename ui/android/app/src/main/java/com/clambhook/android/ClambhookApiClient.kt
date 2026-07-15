package com.clambhook.android

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.io.IOException
import java.io.Closeable

interface ClambhookApi {
    suspend fun status(): StatusPayload
    suspend fun profiles(): ProfilesPayload
    suspend fun servers(): ServersPayload
    suspend fun policyGroups(): PolicyGroupsPayload
    suspend fun selectPolicyGroup(profile: String = "", group: String, chain: String): PolicyGroupsPayload
    suspend fun rules(): RulesPayload
    suspend fun ruleSets(): RuleSetsPayload
    suspend fun replaceRuleSets(profile: String, ruleSets: List<RuleSetPayload>): RuleSetsPayload
    suspend fun refreshRuleSets(profile: String = "", names: List<String> = emptyList()): RuleSetsPayload
    suspend fun explainRoute(profile: String = "", network: String, target: String, source: String = ""): RuleTestResponse
    suspend fun traffic(): TrafficSnapshotPayload
    suspend fun connect()
    suspend fun disconnect()
    suspend fun setActiveProfile(name: String)
    suspend fun createRule(rule: RulePayload): RulesPayload
    suspend fun createRuleFromConnection(connection: TrafficConnectionPayload, rule: RulePayload): RulesPayload
    suspend fun createTemporaryRuleFromConnection(connection: TrafficConnectionPayload, action: String, ttlSeconds: Int = 900): TemporaryRuleCreateResponsePayload
    suspend fun cleanupRule(suggestion: TrafficCleanupSuggestionPayload): RulesPayload
    suspend fun replaceRules(profile: String, rules: List<RulePayload>): RulesPayload
    suspend fun developerStatus(): DeveloperStatusPayload
    suspend fun developerEntries(): DeveloperEntriesPayload
    suspend fun developerHar(): String
    suspend fun clearDeveloperEntries()
}

interface ClambhookEventStream {
    fun openEventStream(
        onEvent: (DaemonEvent) -> Unit,
        onFailure: (Throwable) -> Unit
    ): Closeable
}

class ApiHttpException(
    val statusCode: Int,
    val body: String
) : IOException(if (body.isBlank()) statusCode.toString() else "$statusCode: $body")

class ClambhookApiClient(
    baseUrl: String,
    private val tokenProvider: () -> String? = { null },
    private val okHttpClient: OkHttpClient = OkHttpClient()
) : ClambhookApi, ClambhookEventStream {
    private val baseUrl = baseUrl.trim().trimEnd('/')
    private val jsonMediaType = "application/json".toMediaType()

    override suspend fun status(): StatusPayload =
        ApiJson.decodeFromString(send("GET", "/api/v1/status"))

    override suspend fun profiles(): ProfilesPayload =
        ApiJson.decodeFromString(send("GET", "/api/v1/profiles"))

    override suspend fun servers(): ServersPayload =
        ApiJson.decodeFromString(send("GET", "/api/v1/servers"))

    override suspend fun policyGroups(): PolicyGroupsPayload =
        ApiJson.decodeFromString(send("GET", "/api/v1/policy-groups"))

    override suspend fun selectPolicyGroup(profile: String, group: String, chain: String): PolicyGroupsPayload =
        ApiJson.decodeFromString(
            send("PUT", "/api/v1/policy-groups/selection", ApiJson.encodeToString(SelectPolicyGroupRequest(profile, group, chain)))
        )

    override suspend fun rules(): RulesPayload =
        ApiJson.decodeFromString(send("GET", "/api/v1/rules"))

    override suspend fun ruleSets(): RuleSetsPayload =
        ApiJson.decodeFromString(send("GET", "/api/v1/rule-sets"))

    override suspend fun replaceRuleSets(profile: String, ruleSets: List<RuleSetPayload>): RuleSetsPayload =
        ApiJson.decodeFromString(
            send("PUT", "/api/v1/rule-sets", ApiJson.encodeToString(ReplaceRuleSetsRequest(profile, ruleSets)))
        )

    override suspend fun refreshRuleSets(profile: String, names: List<String>): RuleSetsPayload =
        ApiJson.decodeFromString(
            send("POST", "/api/v1/rule-sets/refresh", ApiJson.encodeToString(RefreshRuleSetsRequest(profile, names)))
        )

    override suspend fun explainRoute(profile: String, network: String, target: String, source: String): RuleTestResponse =
        ApiJson.decodeFromString(
            send("POST", "/api/v1/routes/explain", ApiJson.encodeToString(RouteExplainRequest(profile, network, target, source)))
        )

    override suspend fun traffic(): TrafficSnapshotPayload =
        ApiJson.decodeFromString(send("GET", "/api/v1/traffic?limit=200"))

    override suspend fun connect() {
        send("POST", "/api/v1/connect")
    }

    override suspend fun disconnect() {
        send("POST", "/api/v1/disconnect")
    }

    override suspend fun setActiveProfile(name: String) {
        send("PUT", "/api/v1/profiles/active", ApiJson.encodeToString(mapOf("name" to name)))
    }

    override suspend fun createRule(rule: RulePayload): RulesPayload =
        ApiJson.decodeFromString(
            send("POST", "/api/v1/rules", ApiJson.encodeToString(CreateRuleRequest(rule)))
        )

    override suspend fun createRuleFromConnection(connection: TrafficConnectionPayload, rule: RulePayload): RulesPayload =
        ApiJson.decodeFromString(
            send(
                "POST",
                "/api/v1/rules/from-connection",
                ApiJson.encodeToString(
                    CreateRuleFromConnectionRequest(
                        connId = connection.connId,
                        profile = connection.profile,
                        name = rule.name,
                        action = rule.action
                    )
                )
            )
        )

    override suspend fun createTemporaryRuleFromConnection(connection: TrafficConnectionPayload, action: String, ttlSeconds: Int): TemporaryRuleCreateResponsePayload =
        ApiJson.decodeFromString(
            send(
                "POST",
                "/api/v1/rules/temporary/from-connection",
                ApiJson.encodeToString(
                    CreateTemporaryRuleFromConnectionRequest(
                        connId = connection.connId,
                        profile = connection.profile,
                        action = action,
                        ttlSeconds = ttlSeconds
                    )
                )
            )
        )

    override suspend fun cleanupRule(suggestion: TrafficCleanupSuggestionPayload): RulesPayload =
        ApiJson.decodeFromString(
            send(
                "POST",
                "/api/v1/rules/cleanup",
                ApiJson.encodeToString(
                    CleanupRuleRequest(
                        profile = suggestion.profile,
                        kind = suggestion.kind,
                        ruleName = suggestion.ruleName,
                        targetRuleName = suggestion.targetRuleName.ifBlank { suggestion.ruleName },
                        operation = suggestion.operation
                    )
                )
            )
        )

    override suspend fun replaceRules(profile: String, rules: List<RulePayload>): RulesPayload =
        ApiJson.decodeFromString(
            send("PUT", "/api/v1/rules", ApiJson.encodeToString(ReplaceRulesRequest(profile, rules)))
        )

    override suspend fun developerStatus(): DeveloperStatusPayload =
        ApiJson.decodeFromString(send("GET", "/api/v1/developer/status"))

    override suspend fun developerEntries(): DeveloperEntriesPayload =
        ApiJson.decodeFromString(send("GET", "/api/v1/developer/entries"))

    override suspend fun developerHar(): String =
        send("GET", "/api/v1/developer/har")

    override suspend fun clearDeveloperEntries() {
        send("DELETE", "/api/v1/developer/entries")
    }

    fun eventsUrl(): String {
        val scheme = when {
            baseUrl.startsWith("https://") -> "wss://"
            baseUrl.startsWith("http://") -> "ws://"
            else -> "ws://"
        }
        val hostAndPath = baseUrl
            .removePrefix("https://")
            .removePrefix("http://")
            .trimEnd('/')
        return "$scheme$hostAndPath/api/v1/events?types=connection.*,rule.*,hop.*,log.*"
    }

    fun eventsRequest(): Request =
        authorizedRequestBuilder(eventsUrl())
            .get()
            .build()

    override fun openEventStream(
        onEvent: (DaemonEvent) -> Unit,
        onFailure: (Throwable) -> Unit
    ): Closeable {
        val webSocket = okHttpClient.newWebSocket(
            eventsRequest(),
            object : WebSocketListener() {
                override fun onMessage(webSocket: WebSocket, text: String) {
                    runCatching { ApiJson.decodeFromString<DaemonEvent>(text) }
                        .onSuccess(onEvent)
                        .onFailure(onFailure)
                }

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                    onFailure(t)
                }
            }
        )
        return Closeable { webSocket.close(1000, null) }
    }

    private suspend fun send(method: String, path: String, body: String? = null): String =
        withContext(Dispatchers.IO) {
            val builder = authorizedRequestBuilder("$baseUrl$path")
            val requestBody = body?.toRequestBody(jsonMediaType)
            if (requestBody != null) {
                builder.header("Content-Type", "application/json")
            }
            when (method) {
                "GET" -> builder.get()
                "POST" -> builder.post(requestBody ?: ByteArray(0).toRequestBody(null))
                "PUT" -> builder.put(requireNotNull(requestBody))
                "DELETE" -> builder.delete(requestBody)
                else -> error("unsupported method: $method")
            }
            okHttpClient.newCall(builder.build()).execute().use { response ->
                val responseBody = response.body?.string().orEmpty()
                if (!response.isSuccessful) {
                    throw ApiHttpException(response.code, responseBody.take(1024).trim())
                }
                responseBody
            }
        }

    private fun authorizedRequestBuilder(url: String): Request.Builder {
        val builder = Request.Builder().url(url)
        val token = tokenProvider()?.trim().orEmpty()
        if (token.isNotEmpty()) {
            builder.header("Authorization", "Bearer $token")
        }
        return builder
    }
}
