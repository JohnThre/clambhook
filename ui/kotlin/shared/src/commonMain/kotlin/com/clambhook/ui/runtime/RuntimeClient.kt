// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.runtime

import com.clambhook.ui.json.Json
import com.clambhook.ui.json.JsonNode
import kotlinx.coroutines.flow.Flow

/**
 * Typed, asynchronous view of the frozen C runtime control and event
 * contracts. Views depend on this interface instead of HTTP, JNI, or Android
 * lifecycle classes.
 */
interface RuntimeClient : AutoCloseable {
    suspend fun status(): Status
    suspend fun profiles(): Profiles
    suspend fun servers(profile: String = ""): Document
    suspend fun rules(profile: String = ""): Document
    suspend fun traffic(query: TrafficQuery): Document
    suspend fun decisions(query: TrafficQuery): Document
    suspend fun temporaryRules(profile: String = ""): Document
    suspend fun policyGroups(profile: String = ""): Document
    suspend fun ruleSets(profile: String = ""): Document
    suspend fun ruleSubscriptions(profile: String = ""): Document
    suspend fun dns(profile: String = ""): Document
    suspend fun settings(profile: String = ""): Document
    suspend fun conditioner(profile: String = ""): Document
    suspend fun pendingPrompts(): Document
    suspend fun promptDecisions(): Document
    suspend fun developerStatus(): Document
    suspend fun developerEntries(query: String): Document
    suspend fun developerSettings(): Document
    suspend fun developerMapRules(profile: String = ""): Document
    suspend fun developerBreakpointRules(profile: String = ""): Document
    suspend fun developerRewriteRules(profile: String = ""): Document
    suspend fun developerPendingBreakpoints(): Document
    suspend fun developerCaPem(): String
    suspend fun events(cursor: EventCursor): EventBatch

    val supportsLiveEvents: Boolean

    fun liveEvents(cursor: EventCursor): Flow<Event>

    suspend fun rawRequest(method: String, path: String, body: String): String

    suspend fun request(method: String, path: String, body: String): Document =
        Document.parse(rawRequest(method, path, body))

    suspend fun connect(): Document = request("POST", "/api/v1/connect", "{}")

    suspend fun disconnect(): Document = request("POST", "/api/v1/disconnect", "{}")

    suspend fun setActiveProfile(profile: String): Document =
        request("PUT", "/api/v1/profiles/active", Json.obj("name" to profile))

    suspend fun exportConfig(): String = rawRequest("GET", "/api/v1/config/export", "")

    suspend fun importConfig(toml: String): Document =
        Document.parse(rawRequest("POST", "/api/v1/config/import", toml))

    suspend fun reviewOutlineAccessKey(accessKey: String): Document =
        request("POST", "/api/v1/outline/review", Json.obj("access_key" to accessKey))

    suspend fun importOutlineAccessKey(accessKey: String, profileName: String, activate: Boolean): Document =
        request(
            "POST",
            "/api/v1/outline/import",
            Json.obj("access_key" to accessKey, "profile_name" to profileName, "activate" to activate),
        )

    suspend fun refreshOutlineProfile(profile: String): Document =
        request("POST", "/api/v1/outline/refresh", Json.obj("profile" to profile))

    suspend fun reviewProfileConversion(source: String, format: String, profileName: String): Document =
        request(
            "POST",
            "/api/v1/config/converter/review",
            Json.obj("source" to source, "format" to format.ifBlank { "auto" }, "profile_name" to profileName),
        )

    suspend fun importProfileConversion(
        source: String,
        format: String,
        profileName: String,
        expectedSha256: String,
        activate: Boolean,
    ): Document = request(
        "POST",
        "/api/v1/config/converter/import",
        Json.obj(
            "source" to source,
            "format" to format.ifBlank { "auto" },
            "profile_name" to profileName,
            "expected_sha256" to expectedSha256,
            "activate" to activate,
        ),
    )

    suspend fun updateDns(json: String) = request("PUT", "/api/v1/dns", json)
    suspend fun updateSettings(json: String) = request("PUT", "/api/v1/config/settings", json)
    suspend fun updateConditioner(json: String) = request("PUT", "/api/v1/conditioner", json)
    suspend fun replaceRules(json: String) = request("PUT", "/api/v1/rules", json)
    suspend fun createRule(json: String) = request("POST", "/api/v1/rules", json)
    suspend fun replacePolicyGroups(json: String) = request("PUT", "/api/v1/policy-groups", json)
    suspend fun testPolicyGroups(json: String) = request("POST", "/api/v1/policy-groups/test", json)
    suspend fun selectPolicyGroup(json: String) = request("PUT", "/api/v1/policy-groups/selection", json)
    suspend fun replaceRuleSets(json: String) = request("PUT", "/api/v1/rule-sets", json)
    suspend fun refreshRuleSets(json: String) = request("POST", "/api/v1/rule-sets/refresh", json)
    suspend fun replaceRuleSubscriptions(json: String) = request("PUT", "/api/v1/rule-subscriptions", json)
    suspend fun refreshRuleSubscriptions(json: String) =
        request("POST", "/api/v1/rule-subscriptions/refresh", json)
    suspend fun createRuleFromConnection(json: String) = request("POST", "/api/v1/rules/from-connection", json)
    suspend fun createTemporaryRuleFromConnection(json: String) =
        request("POST", "/api/v1/rules/temporary/from-connection", json)
    suspend fun removeTemporaryRule(identifier: String) =
        request("DELETE", "/api/v1/rules/temporary/" + encodePathSegment(identifier), "{}")
    suspend fun resolvePrompt(identifier: String, json: String) =
        request("POST", "/api/v1/prompts/" + encodePathSegment(identifier) + "/resolve", json)
    suspend fun promotePromptDecision(identifier: String, json: String) =
        request("POST", "/api/v1/prompts/decisions/" + encodePathSegment(identifier) + "/promote", json)
    suspend fun updateDeveloperSettings(json: String) = request("PUT", "/api/v1/developer/settings", json)
    suspend fun clearDeveloperEntries() = request("DELETE", "/api/v1/developer/entries", "{}")
    suspend fun importCurl(json: String) = request("POST", "/api/v1/developer/curl/import", json)
    suspend fun sendDeveloperRequest(json: String) = request("POST", "/api/v1/developer/send", json)
    suspend fun replaceDeveloperMapRules(json: String) = request("PUT", "/api/v1/developer/map-rules", json)
    suspend fun replaceDeveloperBreakpointRules(json: String) =
        request("PUT", "/api/v1/developer/breakpoint-rules", json)
    suspend fun replaceDeveloperRewriteRules(json: String) =
        request("PUT", "/api/v1/developer/rewrite-rules", json)
    suspend fun regenerateDeveloperCa() = request("POST", "/api/v1/developer/ca/regenerate", "{}")
    suspend fun resolveDeveloperBreakpoint(identifier: String, json: String) =
        request("POST", "/api/v1/developer/breakpoints/" + encodePathSegment(identifier) + "/resolve", json)

    val displayName: String

    val supportsConnectionControl: Boolean

    /** The configurable loopback endpoint, or null where the transport is fixed. */
    val endpointSettings: EndpointSettings?

    fun configureEndpoint(settings: EndpointSettings)

    override fun close()
}

/** Loopback control endpoint; the backend trims and validates both values. */
data class EndpointSettings(val baseUrl: String, val bearerToken: String)

data class Document(val rawJson: String, val root: JsonNode) {
    companion object {
        fun parse(rawJson: String): Document = Document(rawJson, Json.parse(rawJson))
    }
}

data class Listener(val protocol: String, val address: String, val activeConnections: Long)

data class Status(
    val running: Boolean,
    val profile: String,
    val tunnelMode: String,
    val listeners: List<Listener>,
    val document: Document,
) {
    companion object {
        fun parse(rawJson: String): Status {
            val document = Document.parse(rawJson)
            val root = document.root
            return Status(
                running = root["running"].bool(false),
                profile = root["profile"].text(),
                tunnelMode = root["tunnel_mode"].text(),
                listeners = root["listeners"].elements().map {
                    Listener(it["protocol"].text(), it["addr"].text(), it["active_conns"].long(0))
                },
                document = document,
            )
        }
    }
}

data class Profiles(val names: List<String>, val active: String, val document: Document) {
    companion object {
        fun parse(rawJson: String): Profiles {
            val document = Document.parse(rawJson)
            return Profiles(
                names = document.root["profiles"].elements().map { it.text() }.filter { it.isNotBlank() },
                active = document.root["active"].text(),
                document = document,
            )
        }
    }
}

data class TrafficQuery(
    val profile: String = "",
    val search: String = "",
    val state: String = "",
    val network: String = "",
    val rule: String = "",
    val chain: String = "",
    val application: String = "",
    val after: Long = 0,
    val limit: Int = 0,
) {
    fun queryString(): String = buildQuery {
        add("profile", profile)
        add("query", search)
        add("state", state)
        add("network", network)
        add("rule", rule)
        add("chain", chain)
        add("application", application)
        if (after > 0) add("after", after.toString())
        if (limit > 0) add("limit", limit.toString())
    }

    companion object {
        fun recent(limit: Int) = TrafficQuery(limit = limit)
    }
}

data class EventCursor(
    val after: Long = 0,
    val limit: Int = 256,
    val types: List<String> = emptyList(),
    val connectionIds: List<String> = emptyList(),
) {
    private val effectiveAfter get() = after.coerceAtLeast(0)
    private val effectiveLimit get() = if (limit <= 0) 256 else limit.coerceAtMost(4096)

    fun queryString(): String = buildQuery {
        add("after", effectiveAfter.toString())
        add("limit", effectiveLimit.toString())
        types.forEach { add("types", it) }
        connectionIds.forEach { add("conn_id", it) }
    }

    companion object {
        fun beginning() = EventCursor()
    }
}

data class Event(
    val sequence: Long,
    val shardId: Long,
    val lamport: Long,
    val timestampNs: Long,
    val type: String,
    val data: JsonNode,
) {
    companion object {
        fun parse(rawJson: String): Event {
            val value = Json.parse(rawJson)
            require(value.isObject) { "event must be a JSON object" }
            return from(value)
        }

        internal fun from(value: JsonNode) = Event(
            sequence = value["sequence"].long(0),
            shardId = value["shard_id"].long(0),
            lamport = value["lamport"].long(0),
            timestampNs = value["ts_ns"].long(value["timestamp_ns"].long(0)),
            type = value["type"].text(),
            data = value["data"],
        )
    }
}

data class EventBatch(
    val events: List<Event>,
    val nextSequence: Long,
    val complete: Boolean,
    val document: Document,
) {
    companion object {
        fun parse(rawJson: String): EventBatch {
            val document = Document.parse(rawJson)
            return EventBatch(
                events = document.root["events"].elements().map(Event::from),
                nextSequence = document.root["next_sequence"].long(0),
                complete = document.root["complete"].bool(true),
                document = document,
            )
        }
    }
}

private class QueryBuilder {
    val output = StringBuilder()

    fun add(key: String, value: String) {
        val clean = value.trim()
        if (clean.isEmpty()) return
        output.append(if (output.isEmpty()) '?' else '&')
            .append(formEncode(key))
            .append('=')
            .append(formEncode(clean))
    }
}

private inline fun buildQuery(block: QueryBuilder.() -> Unit): String =
    QueryBuilder().apply(block).output.toString()

/** `application/x-www-form-urlencoded` encoding, byte-compatible with `java.net.URLEncoder`. */
fun formEncode(value: String): String {
    val output = StringBuilder()
    for (byte in value.encodeToByteArray()) {
        val code = byte.toInt() and 0xFF
        val char = code.toChar()
        when {
            char in 'A'..'Z' || char in 'a'..'z' || char in '0'..'9' ||
                char == '.' || char == '-' || char == '*' || char == '_' -> output.append(char)
            char == ' ' -> output.append('+')
            else -> output.append('%').append(HEX[code shr 4]).append(HEX[code and 0x0F])
        }
    }
    return output.toString()
}

/** Percent-encodes one path segment (spaces as `%20`). */
fun encodePathSegment(value: String): String = formEncode(value.trim()).replace("+", "%20")

private const val HEX = "0123456789ABCDEF"
