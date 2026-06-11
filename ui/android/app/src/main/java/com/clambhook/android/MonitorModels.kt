package com.clambhook.android

fun TrafficConnectionPayload.actionFamily(): String =
    when (ruleAction.lowercase()) {
        "direct" -> "direct"
        "block", "reject" -> "block"
        else -> "proxy"
    }

fun TrafficConnectionPayload.monitorHost(): String {
    val raw = targetHost.ifBlank { visibility?.host.orEmpty().ifBlank { target.substringBeforeLast(":", target) } }
    return raw.trim().trim('[', ']').trimEnd('.').lowercase()
}

fun TrafficConnectionPayload.ruleDraft(actionOverride: String? = null): RulePayload? {
    val host = monitorHost()
    if (host.isBlank()) return null
    val family = actionOverride ?: actionFamily()
    val action = when (family) {
        "direct" -> "direct"
        "block" -> if (ruleAction.lowercase() == "reject") "reject" else "block"
        else -> if (chainName.isBlank()) "direct" else "chain:$chainName"
    }
    val name = "${family}-${host.ruleNameToken()}"
    return when {
        host.looksLikeIpv4() -> RulePayload(name = name, action = action, cidrs = listOf("$host/32"))
        host.contains(":") -> RulePayload(name = name, action = action, cidrs = listOf("$host/128"))
        else -> RulePayload(name = name, action = action, domains = listOf(host))
    }
}

fun TrafficConnectionPayload.canCreateTemporaryRule(): Boolean =
    connId.isNotBlank() && monitorHost().isNotBlank()

fun TrafficConnectionPayload.temporaryProxyAction(fallbackChain: String = ""): String =
    when {
        groupName.isNotBlank() -> "group:$groupName"
        chainName.isNotBlank() -> "chain:$chainName"
        fallbackChain.isNotBlank() -> "chain:$fallbackChain"
        else -> ""
    }

fun TrafficSnapshotPayload.actionCounts(): Map<String, Int> =
    connections.groupingBy { it.actionFamily() }.eachCount()

data class RuleHitSummary(
    val ruleName: String,
    val action: String,
    val count: Int
)

enum class PolicySelectorHealthState {
    StaticRoute,
    Pending,
    Healthy,
    Fallback
}

data class PolicySelectorRouteSummary(
    val groupName: String = "",
    val selectedChain: String = "",
    val healthState: PolicySelectorHealthState = PolicySelectorHealthState.Pending,
    val healthText: String = ""
)

data class PolicySelectorSummary(
    val proxyCount: Int = 0,
    val directCount: Int = 0,
    val blockCount: Int = 0,
    val routes: List<PolicySelectorRouteSummary> = emptyList(),
    val topRuleHits: List<RuleHitSummary> = emptyList()
)

fun policySelectorSummary(
    policyGroups: PolicyGroupsPayload,
    servers: ServersPayload,
    traffic: TrafficSnapshotPayload
): PolicySelectorSummary {
    val counts = traffic.actionCounts()
    val routes = if (policyGroups.groups.isEmpty()) {
        servers.chains.firstOrNull()?.let { chain ->
            listOf(
                PolicySelectorRouteSummary(
                    groupName = "Default route",
                    selectedChain = chain.name,
                    healthState = PolicySelectorHealthState.StaticRoute,
                    healthText = "Static / no health probes"
                )
            )
        }.orEmpty()
    } else {
        policyGroups.groups.map { it.policyRouteSummary() }
    }
    return PolicySelectorSummary(
        proxyCount = counts["proxy"] ?: 0,
        directCount = counts["direct"] ?: 0,
        blockCount = counts["block"] ?: 0,
        routes = routes,
        topRuleHits = traffic.ruleHitSummaries().take(3)
    )
}

fun TrafficSnapshotPayload.ruleHitSummaries(): List<RuleHitSummary> =
    if (ruleHits.isNotEmpty()) {
        ruleHits
            .map { RuleHitSummary(it.ruleName.ifBlank { "default" }, it.action, it.count) }
            .sortedWith(compareByDescending<RuleHitSummary> { it.count }.thenBy { it.ruleName }.thenBy { it.action })
    } else {
        connections
            .filter { it.ruleName.isNotBlank() || it.ruleAction.isNotBlank() }
            .groupBy { "${it.ruleName}|${it.actionFamily()}" }
            .map { (_, rows) ->
                RuleHitSummary(rows.first().ruleName.ifBlank { "default" }, rows.first().actionFamily(), rows.size)
            }
            .sortedWith(compareByDescending<RuleHitSummary> { it.count }.thenBy { it.ruleName }.thenBy { it.action })
    }

private fun PolicyGroupPayload.policyRouteSummary(): PolicySelectorRouteSummary {
    val selected = selectedChain.ifBlank { chains.firstOrNull().orEmpty() }
    if (results.isEmpty()) {
        return PolicySelectorRouteSummary(
            groupName = name,
            selectedChain = selected,
            healthState = PolicySelectorHealthState.Pending,
            healthText = "Pending health"
        )
    }
    val healthy = results.count { it.healthy }
    val total = results.size
    val selectedHealthy = results.firstOrNull { it.chainName == selected }?.healthy == true
    return if (selectedHealthy) {
        PolicySelectorRouteSummary(
            groupName = name,
            selectedChain = selected,
            healthState = PolicySelectorHealthState.Healthy,
            healthText = "Healthy / $healthy/$total"
        )
    } else {
        PolicySelectorRouteSummary(
            groupName = name,
            selectedChain = selected,
            healthState = PolicySelectorHealthState.Fallback,
            healthText = "Fallback / $healthy/$total healthy"
        )
    }
}

private fun String.ruleNameToken(): String =
    lowercase()
        .map { if (it.isLetterOrDigit() || it == '-') it else '-' }
        .joinToString("")
        .trim('-')
        .ifBlank { "connection" }

private fun String.looksLikeIpv4(): Boolean {
    val parts = split(".")
    return parts.size == 4 && parts.all { part -> part.toIntOrNull()?.let { it in 0..255 } == true }
}
