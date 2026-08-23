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
    suspend fun traffic(filter: TrafficMonitorFilter): TrafficSnapshotPayload
    suspend fun pendingPrompts(): PromptsPayload
    suspend fun resolvePrompt(id: String, action: String, scope: String, matchHost: Boolean, matchPort: Boolean, matchProtocol: Boolean, ttlSeconds: Long = 0)
    suspend fun silentDecisions(): SilentDecisionsPayload
    suspend fun promoteSilentDecision(id: String, scope: String, matchHost: Boolean, matchPort: Boolean, matchProtocol: Boolean)
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
    suspend fun developerEntries(filter: DeveloperEntriesFilter): List<DeveloperEntryPayload>
    suspend fun developerEntryCurl(id: String): String
    suspend fun importCurl(text: String): ParsedCurlResponse
    suspend fun sendComposed(request: ComposedRequestPayload): DeveloperEntryPayload
    suspend fun developerHar(): String
    suspend fun clearDeveloperEntries()
    suspend fun conditioner(profile: String = ""): ConditionerPayload
    suspend fun updateConditioner(request: ConditionerUpdateRequest): ConditionerPayload

    // Whether [updateConditioner] can mutate the conditioner. On-device impls
    // that only proxy the packet runtime cannot edit it (no config primitive),
    // so the UI renders the snapshot read-only. HTTP-backed impls override this.
    val supportsConditionerEditing: Boolean get() = true
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
