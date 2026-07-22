package com.clambhook.linux.store

import com.clambhook.linux.api.ClambhookApi
import com.clambhook.linux.model.*
import kotlinx.coroutines.runBlocking
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private class FakeApi : ClambhookApi {
    var statusPayload = StatusPayload()
    var profilesPayload = ProfilesPayload()
    var serversPayload = ServersPayload()
    var rulesPayload = RulesPayload()
    var trafficPayload = TrafficSnapshotPayload()
    val actions = mutableListOf<String>()

    override suspend fun status(): StatusPayload = statusPayload
    override suspend fun profiles(): ProfilesPayload = profilesPayload
    override suspend fun servers(): ServersPayload = serversPayload
    override suspend fun rules(): RulesPayload = rulesPayload
    override suspend fun traffic(): TrafficSnapshotPayload = trafficPayload
    override suspend fun connect() { actions.add("connect") }
    override suspend fun disconnect() { actions.add("disconnect") }
    override suspend fun setActiveProfile(name: String) { actions.add("profile:$name") }
    override suspend fun createRule(rule: RulePayload): RulesPayload { actions.add("rule:${rule.name}"); rulesPayload = rulesPayload.copy(rules = rulesPayload.rules + rule); return rulesPayload }
    override suspend fun createRuleFromConnection(connection: TrafficConnectionPayload, rule: RulePayload): RulesPayload { actions.add("connection-rule:${connection.connId}:${rule.name}"); rulesPayload = rulesPayload.copy(rules = rulesPayload.rules + rule); return rulesPayload }
    override suspend fun cleanupRule(suggestion: TrafficCleanupSuggestionPayload): RulesPayload { actions.add("cleanup:${suggestion.operation}:${suggestion.targetRuleName}"); return rulesPayload }
    override suspend fun policyGroups(): PolicyGroupsPayload = PolicyGroupsPayload()
    override suspend fun selectPolicyGroup(group: String, chain: String): PolicyGroupsPayload { actions.add("policy-select:$group:$chain"); return PolicyGroupsPayload() }
    override suspend fun testPolicyGroups(group: String): PolicyGroupsPayload { actions.add("policy-test:$group"); return PolicyGroupsPayload() }
    override suspend fun pendingPrompts(): PromptsPayload = PromptsPayload()
    override suspend fun resolvePrompt(id: String, action: String, scope: String, matchHost: Boolean) { actions.add("prompt:$id:$action:$scope") }
    override suspend fun dns(): DnsPayload = DnsPayload()
    override suspend fun developerStatus(): DeveloperStatusPayload = DeveloperStatusPayload()
    override suspend fun setDeveloperCapture(enabled: Boolean): DeveloperStatusPayload { actions.add("capture:$enabled"); return DeveloperStatusPayload() }
    override suspend fun developerEntries(): List<DeveloperEntryPayload> = emptyList()
    override suspend fun developerEntry(id: String): DeveloperEntryPayload = DeveloperEntryPayload()
    override suspend fun repeatDeveloperEntry(id: String): DeveloperEntryPayload = DeveloperEntryPayload()
    override fun eventsUri(): String = "ws://localhost:9090/api/v1/events"
    override fun authorizationHeader(): String = ""
    override fun configureBaseUrl(baseUrl: String) {}
}

class DashboardStoreTest {
    @Test
    fun refreshLoadsStatusProfilesAndServers() = runBlocking {
        val api = FakeApi()
        api.statusPayload = ApiJson.decodeFromString(StatusPayload.serializer(), """{"running":true,"profile":"A","listeners":[{"protocol":"socks5","addr":"127.0.0.1:1080","active_conns":3}]}""")
        api.profilesPayload = ApiJson.decodeFromString(ProfilesPayload.serializer(), """{"profiles":["A","B"],"active":"A"}""")
        api.serversPayload = ApiJson.decodeFromString(ServersPayload.serializer(), """{"profile":"A","chains":[{"name":"default","servers":[{"name":"london","address":"uk.example:443","protocol":"clambback"}]}]}""")
        api.rulesPayload = ApiJson.decodeFromString(RulesPayload.serializer(), """{"profile":"A","rules":[{"name":"ads","action":"block","domains":["ads.example.com"]}]}""")
        api.trafficPayload = ApiJson.decodeFromString(TrafficSnapshotPayload.serializer(), """{"summary":{"active_connections":1,"rx_bps":2048},"connections":[{"conn_id":"c1","state":"active","target":"example.com:443"}]}""")

        val store = DashboardStore(api)
        store.refreshDashboard()
        assertTrue(store.state.value.status.running)
        assertEquals(3, store.activeConnections())
        assertEquals("B", store.state.value.profiles.profiles[1])
        assertEquals("london", store.state.value.servers.chains[0].servers[0].name)
        assertEquals("ads", store.state.value.rules.rules[0].name)
        assertEquals("example.com:443", store.state.value.traffic.connections[0].target)
    }

    @Test
    fun eventRateAndLogRetention() = runBlocking {
        val store = DashboardStore(FakeApi())
        for (i in 0 until 65) {
            store.applyEvent(DaemonEvent(type = "connection.bytes", data = mapOf(
                "rx_delta" to kotlinx.serialization.json.JsonPrimitive((i + 1) * 1024),
                "tx_delta" to kotlinx.serialization.json.JsonPrimitive((i + 1) * 512),
                "interval_ns" to kotlinx.serialization.json.JsonPrimitive(1_000_000_000)
            )))
        }
        assertEquals(BANDWIDTH_SAMPLE_LIMIT, store.state.value.bandwidthSamples.size)
        assertEquals(65 * 1024.0, store.currentBandwidth().rxBps)

        store.seedTrafficConnections(listOf(TrafficConnectionPayload(connId = "c1")))
        val connection = TrafficConnectionPayload(connId = "c1")
        store.applyEvent(DaemonEvent(type = "connection.bytes", data = mapOf(
            "conn_id" to kotlinx.serialization.json.JsonPrimitive("c1"),
            "rx_delta" to kotlinx.serialization.json.JsonPrimitive(2048),
            "tx_delta" to kotlinx.serialization.json.JsonPrimitive(1024),
            "interval_ns" to kotlinx.serialization.json.JsonPrimitive(1_000_000_000)
        )))
        val updatedConn = store.state.value.traffic.connections.firstOrNull { it.connId == "c1" }
        assertEquals(2048.0, updatedConn?.rxBps)

        for (i in 0 until 205) {
            store.applyEvent(DaemonEvent(type = "log.line", data = mapOf("line" to kotlinx.serialization.json.JsonPrimitive("line-$i"))))
        }
        assertEquals(MAX_LOG_LINES, store.state.value.logs.size)
        assertEquals("line-5", store.state.value.logs[0])
        assertEquals("line-204", store.state.value.logs[199])

        store.setLogRetention(50)
        assertEquals(50, store.state.value.logs.size)
        assertEquals("line-155", store.state.value.logs[0])
    }

    @Test
    fun actionsRefreshAfterChange() = runBlocking {
        val api = FakeApi()
        val store = DashboardStore(api)
        store.connect()
        store.disconnect()
        store.setActiveProfile("B")
        assertEquals("connect", api.actions[0])
        assertEquals("disconnect", api.actions[1])
        assertEquals("profile:B", api.actions[2])
    }

    @Test
    fun createRuleRefreshesDashboard() = runBlocking {
        val api = FakeApi()
        val store = DashboardStore(api)
        val rule = RulePayload(name = "block-example-com", action = "block", domains = listOf("example.com"))
        store.createRule(rule)
        assertEquals("rule:block-example-com", api.actions[0])
        assertEquals("block-example-com", store.state.value.rules.rules[0].name)
    }
}