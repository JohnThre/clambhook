// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.model

import com.clambhook.ui.json.Json
import com.clambhook.ui.json.JsonNode
import com.clambhook.ui.runtime.RuntimeClient
import com.clambhook.ui.runtime.TrafficQuery
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope

/** Immutable view state shared by the Android and GNU/Linux layouts. */
data class DashboardData(
    val running: Boolean,
    val activeProfile: String,
    val profiles: List<String>,
    val listeners: List<Listener>,
    val servers: List<Server>,
    val rules: List<Rule>,
    val traffic: TrafficSummary,
    val connections: List<Connection>,
    val dns: Dns,
    val developer: Developer,
) {
    data class Listener(val protocol: String, val address: String, val activeConnections: Long)

    data class Server(
        val chain: String,
        val name: String,
        val address: String,
        val protocol: String,
        val location: String,
    )

    data class Rule(val name: String, val action: String, val matchSummary: String)

    data class TrafficSummary(
        val activeConnections: Long,
        val downloadBytesPerSecond: Double,
        val uploadBytesPerSecond: Double,
        val receivedBytes: Long,
        val transmittedBytes: Long,
    )

    data class Connection(
        val id: String,
        val target: String,
        val network: String,
        val state: String,
        val action: String,
        val chain: String,
        val application: String,
        val downloadBytesPerSecond: Double,
        val uploadBytesPerSecond: Double,
        val receivedBytes: Long,
        val transmittedBytes: Long,
    ) {
        /** Chain name when routed through one, otherwise the rule action. */
        val route: String get() = chain.ifBlank { action.ifBlank { "—" } }
    }

    data class Dns(val enabled: Boolean, val strategy: String, val upstreams: List<String>)

    data class Developer(val enabled: Boolean, val captureCount: Long, val captures: List<Capture>)

    data class Capture(val id: String, val method: String, val url: String, val status: Long, val error: String)

    companion object {
        val EMPTY = DashboardData(
            running = false,
            activeProfile = "",
            profiles = emptyList(),
            listeners = emptyList(),
            servers = emptyList(),
            rules = emptyList(),
            traffic = TrafficSummary(0, 0.0, 0.0, 0, 0),
            connections = emptyList(),
            dns = Dns(false, "route", emptyList()),
            developer = Developer(false, 0, emptyList()),
        )
    }
}

/** Fetches one coherent dashboard generation concurrently. */
class DashboardLoader(private val runtime: RuntimeClient) {
    suspend fun load(): DashboardData = coroutineScope {
        val status = async { runtime.status() }
        val profiles = async { runtime.profiles() }
        val servers = async { runtime.servers() }
        val rules = async { runtime.rules() }
        val traffic = async { runtime.traffic(TrafficQuery.recent(200)) }
        val dns = async { runtime.dns() }
        val developer = async { runtime.developerStatus() }
        val entries = async { runtime.developerEntries("limit=200") }
        DashboardMapper.map(
            statusJson = status.await().document.rawJson,
            profilesJson = profiles.await().document.rawJson,
            serversJson = servers.await().rawJson,
            rulesJson = rules.await().rawJson,
            trafficJson = traffic.await().rawJson,
            dnsJson = dns.await().rawJson,
            developerStatusJson = developer.await().rawJson,
            developerEntriesJson = entries.await().rawJson,
        )
    }
}

/** Maps stable daemon/native JSON contracts into compact view state. */
object DashboardMapper {
    fun map(
        statusJson: String,
        profilesJson: String,
        serversJson: String,
        rulesJson: String,
        trafficJson: String,
        dnsJson: String,
        developerStatusJson: String,
        developerEntriesJson: String,
    ): DashboardData {
        val status = Json.parse(statusJson)
        val profiles = Json.parse(profilesJson)
        val servers = Json.parse(serversJson)
        val rules = Json.parse(rulesJson)
        val traffic = Json.parse(trafficJson)
        val dns = Json.parse(dnsJson)
        val developer = Json.parse(developerStatusJson)
        val entries = Json.parse(developerEntriesJson)

        val serverRows = servers["chains"].elements().flatMap { chain ->
            val chainName = chain["name"].text()
            chain["servers"].elements().map { server ->
                val geo = server["geo"]
                DashboardData.Server(
                    chain = chainName,
                    name = server["name"].text(),
                    address = server["address"].text(),
                    protocol = server["protocol"].text(),
                    location = listOf(geo["city"].text(), geo["country"].text())
                        .filter { it.isNotBlank() }.joinToString(", "),
                )
            }
        }

        val summary = traffic["summary"]
        val connections = traffic["connections"].elements().map { connection ->
            DashboardData.Connection(
                id = connection["conn_id"].text(),
                target = firstText(connection["target"], connection["target_host"]),
                network = connection["network"].text(),
                state = connection["state"].text(),
                action = connection["rule_action"].text(),
                chain = connection["chain_name"].text(),
                application = connection["application"].text(),
                downloadBytesPerSecond = connection["rx_bps"].double(0.0),
                uploadBytesPerSecond = connection["tx_bps"].double(0.0),
                receivedBytes = connection["rx_total"].long(0),
                transmittedBytes = connection["tx_total"].long(0),
            )
        }

        val upstreams = dns["upstreams"].elements().map { upstream ->
            listOf(
                upstream["name"].text(),
                upstream["protocol"].text().uppercase(),
                firstText(upstream["url"], upstream["address"], upstream["server_name"]),
            ).filter { it.isNotBlank() }.joinToString(" · ")
        }

        var captureArray = entries["entries"]
        if (!captureArray.isArray && entries.isArray) captureArray = entries
        val captures = captureArray.elements().map { entry ->
            DashboardData.Capture(
                id = entry["id"].text(),
                method = entry["method"].text(),
                url = entry["url"].text(),
                status = entry["status"].long(entry["status_code"].long(0)),
                error = entry["error"].text(),
            )
        }

        return DashboardData(
            running = status["running"].bool(false),
            activeProfile = firstText(status["profile"], profiles["active"]),
            profiles = profiles["profiles"].elements().map { it.text() }.filter { it.isNotBlank() },
            listeners = status["listeners"].elements().map {
                DashboardData.Listener(it["protocol"].text(), it["addr"].text(), it["active_conns"].long(0))
            },
            servers = serverRows,
            rules = rules["rules"].elements().map {
                DashboardData.Rule(it["name"].text(), it["action"].text(), ruleMatches(it))
            },
            traffic = DashboardData.TrafficSummary(
                activeConnections = summary["active_connections"].long(0),
                downloadBytesPerSecond = summary["rx_bps"].double(0.0),
                uploadBytesPerSecond = summary["tx_bps"].double(0.0),
                receivedBytes = summary["rx_total"].long(0),
                transmittedBytes = summary["tx_total"].long(0),
            ),
            connections = connections,
            dns = DashboardData.Dns(dns["enabled"].bool(false), dns["strategy"].text("route"), upstreams),
            developer = DashboardData.Developer(
                enabled = developer["enabled"].bool(false),
                captureCount = developer["capture_count"].long(captures.size.toLong()),
                captures = captures,
            ),
        )
    }

    private fun ruleMatches(rule: JsonNode): String {
        val parts = listOfNotNull(
            matches("domain", rule["domains"]),
            matches("suffix", rule["domain_suffixes"]),
            matches("keyword", rule["domain_keywords"]),
            matches("CIDR", rule["cidrs"]),
            matches("port", rule["ports"]),
            matches("network", rule["networks"]),
        )
        return if (parts.isEmpty()) "All traffic" else parts.joinToString(" · ")
    }

    private fun matches(label: String, values: JsonNode): String? {
        val elements = values.elements()
        if (elements.isEmpty()) return null
        val visible = elements.take(3).map { item ->
            item.text().ifBlank {
                val number = item.long(Long.MIN_VALUE)
                if (number == Long.MIN_VALUE) item.toString() else number.toString()
            }
        }
        val items = if (elements.size > 3) visible + "+${elements.size - 3}" else visible
        return "$label: ${items.joinToString(", ")}"
    }

    private fun firstText(vararg values: JsonNode): String =
        values.map { it.text() }.firstOrNull { it.isNotBlank() }.orEmpty()
}
