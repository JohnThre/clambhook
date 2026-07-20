package com.clambhook.android

import java.io.Closeable
import java.io.IOException

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
