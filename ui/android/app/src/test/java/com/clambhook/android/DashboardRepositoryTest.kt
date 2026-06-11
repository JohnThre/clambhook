package com.clambhook.android

import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonPrimitive
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class DashboardRepositoryTest {
    @Test
    fun refreshDashboardLoadsStatusProfilesAndServers() = runBlocking {
        val api = FakeApi(
            status = StatusPayload(
                running = true,
                profile = "A",
                listeners = listOf(ListenerStatusPayload("socks5", "127.0.0.1:1080", 3))
            ),
            profiles = ProfilesPayload(profiles = listOf("A", "B"), active = "A"),
            servers = ServersPayload(
                profile = "A",
                chains = listOf(
                    ChainPayload(
                        name = "default",
                        servers = listOf(ServerPayload(name = "london", address = "uk.example:443", protocol = "clambback"))
                    )
                )
            ),
            policyGroups = PolicyGroupsPayload(
                profile = "A",
                groups = listOf(
                    PolicyGroupPayload(
                        name = "auto",
                        chains = listOf("default"),
                        selectedChain = "default",
                        results = listOf(PolicyProbeResultPayload(chainName = "default", healthy = true))
                    )
                )
            ),
            traffic = TrafficSnapshotPayload(
                summary = TrafficSummaryPayload(activeConnections = 1, rxBps = 1024.0),
                connections = listOf(TrafficConnectionPayload(connId = "c1", state = "active", target = "example.com:443"))
            )
        )
        val repository = DashboardRepository(api)

        repository.refreshDashboard()

        val state = repository.state.value
        assertTrue(state.apiOnline)
        assertTrue(state.status.running)
        assertEquals("A", state.activeProfile)
        assertEquals(3, state.activeConnections)
        assertEquals("london", state.servers.chains.single().servers.single().name)
        assertEquals("default", state.policyGroups.groups.single().selectedChain)
        assertEquals("example.com:443", state.traffic.connections.single().target)
        assertTrue(state.lastUpdatedEpochMillis > 0)
        assertFalse(state.isRefreshing)
    }

    @Test
    fun refreshDashboardStoresOfflineErrorOnFailure() = runBlocking {
        val repository = DashboardRepository(FakeApi(error = IllegalStateException("boom")))

        repository.refreshDashboard()

        val state = repository.state.value
        assertFalse(state.apiOnline)
        assertEquals("boom", state.errorText)
    }

    @Test
    fun refreshStatusUpdatesPolicyGroupsForSelector() = runBlocking {
        val api = FakeApi(
            policyGroups = PolicyGroupsPayload(
                profile = "A",
                groups = listOf(
                    PolicyGroupPayload(
                        name = "auto",
                        chains = listOf("proxy", "backup"),
                        selectedChain = "proxy",
                        results = listOf(
                            PolicyProbeResultPayload(chainName = "proxy", healthy = false, error = "timeout"),
                            PolicyProbeResultPayload(chainName = "backup", healthy = true, latencyNs = 30_000_000)
                        )
                    )
                )
            )
        )
        val repository = DashboardRepository(api)

        repository.refreshStatus()

        val state = repository.state.value
        val summary = policySelectorSummary(state.policyGroups, state.servers, state.traffic)
        assertEquals("proxy", state.policyGroups.groups.single().selectedChain)
        assertEquals(PolicySelectorHealthState.Fallback, summary.routes.single().healthState)
        assertEquals("Fallback / 1/2 healthy", summary.routes.single().healthText)
        assertEquals(1, api.policyGroupCalls)
    }

    @Test
    fun actionsRefreshDashboardAfterSuccess() = runBlocking {
        val api = FakeApi()
        val repository = DashboardRepository(api)

        repository.connect()
        repository.disconnect()
        repository.setActiveProfile("B")

        assertEquals(listOf("connect", "disconnect", "profile:B"), api.actions)
        assertEquals(3, api.statusCalls)
        assertEquals(3, api.profileCalls)
        assertEquals(3, api.serverCalls)
        assertEquals(3, api.policyGroupCalls)
        assertEquals(3, api.ruleCalls)
        assertEquals(3, api.trafficCalls)
        assertNull(repository.state.value.actionInProgress)
        assertEquals("", repository.state.value.pendingProfile)
    }

    @Test
    fun createRuleRefreshesDashboardAfterSuccess() = runBlocking {
        val api = FakeApi()
        val repository = DashboardRepository(api)

        repository.createRule(RulePayload(
            name = "block-example-com",
            action = "block",
            domains = listOf("example.com")
        ))

        assertEquals(listOf("rule:block-example-com"), api.actions)
        assertEquals(1, api.ruleCalls)
        assertEquals("block-example-com", repository.state.value.rules.rules.single().name)
    }

    @Test
    fun createTemporaryRuleFromConnectionRefreshesDashboardAfterSuccess() = runBlocking {
        val api = FakeApi()
        val repository = DashboardRepository(api)

        repository.createTemporaryRuleFromConnection(
            TrafficConnectionPayload(connId = "c1", profile = "Work", targetHost = "api.example.com"),
            "block"
        )

        assertEquals(listOf("temporary-rule:c1:block:900"), api.actions)
        assertEquals(1, api.ruleCalls)
        assertNull(repository.state.value.actionInProgress)
    }

    @Test
    fun appliesBandwidthAndLogEventsWithCaps() = runBlocking {
        val repository = DashboardRepository(FakeApi())

        repository.applyEvent(
            DaemonEvent(
                shardId = 1u,
                lamport = 1u,
                tsNs = 1,
                type = "connection.bytes",
                data = mapOf(
                    "rx_delta" to JsonPrimitive(2048),
                    "tx_delta" to JsonPrimitive(1024),
                    "interval_ns" to JsonPrimitive(1_000_000_000)
                )
            )
        )
        repeat(maxLogLines + 5) { index ->
            repository.applyEvent(
                DaemonEvent(
                    shardId = 0u,
                    lamport = index.toULong(),
                    tsNs = index.toLong(),
                    type = "log.line",
                    data = mapOf("line" to JsonPrimitive("line-$index"))
                )
            )
        }

        val state = repository.state.value
        assertEquals(2048.0, state.currentBandwidth.rxBps, 0.001)
        assertEquals(1024.0, state.currentBandwidth.txBps, 0.001)
        assertEquals(maxLogLines, state.logs.size)
        assertEquals("line-5", state.logs.first())
    }
}

private class FakeApi(
    private val status: StatusPayload = StatusPayload(),
    private val profiles: ProfilesPayload = ProfilesPayload(profiles = listOf("A", "B"), active = "A"),
    private val servers: ServersPayload = ServersPayload(profile = "A"),
    private val policyGroups: PolicyGroupsPayload = PolicyGroupsPayload(profile = "A"),
    private var rules: RulesPayload = RulesPayload(profile = "A"),
    private var ruleSets: RuleSetsPayload = RuleSetsPayload(profile = "A"),
    private val traffic: TrafficSnapshotPayload = TrafficSnapshotPayload(),
    private val error: Throwable? = null
) : ClambhookApi {
    val actions = mutableListOf<String>()
    var statusCalls = 0
    var profileCalls = 0
    var serverCalls = 0
    var policyGroupCalls = 0
    var ruleCalls = 0
    var ruleSetCalls = 0
    var trafficCalls = 0

    override suspend fun status(): StatusPayload {
        statusCalls += 1
        error?.let { throw it }
        return status
    }

    override suspend fun profiles(): ProfilesPayload {
        profileCalls += 1
        error?.let { throw it }
        return profiles
    }

    override suspend fun servers(): ServersPayload {
        serverCalls += 1
        error?.let { throw it }
        return servers
    }

    override suspend fun policyGroups(): PolicyGroupsPayload {
        policyGroupCalls += 1
        error?.let { throw it }
        return policyGroups
    }

    override suspend fun selectPolicyGroup(profile: String, group: String, chain: String): PolicyGroupsPayload {
        actions += "policy:$profile:$group:$chain"
        return policyGroups
    }

    override suspend fun rules(): RulesPayload {
        ruleCalls += 1
        error?.let { throw it }
        return rules
    }

    override suspend fun ruleSets(): RuleSetsPayload {
        ruleSetCalls += 1
        error?.let { throw it }
        return ruleSets
    }

    override suspend fun replaceRuleSets(profile: String, ruleSets: List<RuleSetPayload>): RuleSetsPayload {
        actions += "rule-sets:$profile"
        this.ruleSets = RuleSetsPayload(profile = profile, ruleSets = ruleSets)
        return this.ruleSets
    }

    override suspend fun refreshRuleSets(profile: String, names: List<String>): RuleSetsPayload {
        actions += "rule-sets-refresh:$profile"
        return ruleSets
    }

    override suspend fun explainRoute(profile: String, network: String, target: String, source: String): RuleTestResponse =
        RuleTestResponse(profile = profile)

    override suspend fun traffic(): TrafficSnapshotPayload {
        trafficCalls += 1
        error?.let { throw it }
        return traffic
    }

    override suspend fun connect() {
        actions += "connect"
    }

    override suspend fun disconnect() {
        actions += "disconnect"
    }

    override suspend fun setActiveProfile(name: String) {
        actions += "profile:$name"
    }

    override suspend fun createRule(rule: RulePayload): RulesPayload {
        actions += "rule:${rule.name}"
        rules = RulesPayload(profile = "A", rules = listOf(rule))
        return rules
    }

    override suspend fun createRuleFromConnection(connection: TrafficConnectionPayload, rule: RulePayload): RulesPayload {
        actions += "connection-rule:${connection.connId}:${rule.name}"
        rules = RulesPayload(profile = connection.profile, rules = listOf(rule))
        return rules
    }

    override suspend fun createTemporaryRuleFromConnection(connection: TrafficConnectionPayload, action: String, ttlSeconds: Int): TemporaryRuleCreateResponsePayload {
        actions += "temporary-rule:${connection.connId}:$action:$ttlSeconds"
        return TemporaryRuleCreateResponsePayload(
            temporaryRule = TemporaryRulePayload(
                id = "tmp1",
                profile = connection.profile,
                rule = RulePayload(name = "tmp", action = action)
            )
        )
    }

    override suspend fun cleanupRule(suggestion: TrafficCleanupSuggestionPayload): RulesPayload {
        actions += "cleanup:${suggestion.operation}:${suggestion.targetRuleName.ifBlank { suggestion.ruleName }}"
        return rules
    }

    override suspend fun replaceRules(profile: String, rules: List<RulePayload>): RulesPayload {
        actions += "replace:$profile"
        this.rules = RulesPayload(profile = profile, rules = rules)
        return this.rules
    }

    override suspend fun developerStatus(): DeveloperStatusPayload = DeveloperStatusPayload()

    override suspend fun developerEntries(): DeveloperEntriesPayload = DeveloperEntriesPayload()

    override suspend fun developerHar(): String = "{}"

    override suspend fun clearDeveloperEntries() {
        actions += "developer:clear"
    }
}
