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

fun TrafficSnapshotPayload.actionCounts(): Map<String, Int> =
    connections.groupingBy { it.actionFamily() }.eachCount()

data class RuleHitSummary(
    val ruleName: String,
    val action: String,
    val count: Int
)

fun TrafficSnapshotPayload.ruleHitSummaries(): List<RuleHitSummary> =
    connections
        .filter { it.ruleName.isNotBlank() || it.ruleAction.isNotBlank() }
        .groupBy { "${it.ruleName}|${it.actionFamily()}" }
        .map { (_, rows) ->
            RuleHitSummary(rows.first().ruleName.ifBlank { "default" }, rows.first().actionFamily(), rows.size)
        }
        .sortedWith(compareByDescending<RuleHitSummary> { it.count }.thenBy { it.ruleName }.thenBy { it.action })

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
