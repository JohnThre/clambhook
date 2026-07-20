package com.clambhook.android

import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class LocalTunnelApiTest {
    private val runtime = FakeTunnelRuntime()

    private fun api(
        onConnect: () -> Unit = {},
        onDisconnect: () -> Unit = {},
    ): LocalTunnelApi {
        ClambhookTunnelSession.publish(runtime, "cfg-path")
        return LocalTunnelApi(onConnect = onConnect, onDisconnect = onDisconnect)
    }

    @After
    fun tearDown() = ClambhookTunnelSession.clear()

    @Test
    fun decodesStatusFromRuntime() = runBlocking {
        runtime.statusJson = """{"running":true,"profile":"work"}"""
        val status = api().status()
        assertEquals(true, status.running)
        assertEquals("work", status.profile)
    }

    @Test
    fun decodesProfilesFromRuntime() = runBlocking {
        runtime.profilesJson = """{"profiles":["work","home"],"active":"work"}"""
        val profiles = api().profiles()
        assertEquals(listOf("work", "home"), profiles.profiles)
        assertEquals("work", profiles.active)
    }

    @Test
    fun decodesPolicyGroupsFromDashboardBundle() = runBlocking {
        runtime.dashboardJson = """
            {"policy_groups":{"profile":"work","groups":[{"name":"g1","selected_chain":"c1"}]}}
        """.trimIndent()
        val groups = api().policyGroups()
        assertEquals("work", groups.profile)
        assertEquals("g1", groups.groups.single().name)
        assertEquals("c1", groups.groups.single().selectedChain)
    }

    @Test
    fun setActiveProfileDispatchesToRuntime() = runBlocking {
        api().setActiveProfile("home")
        assertEquals("home", runtime.recordedProfile)
    }

    @Test
    fun selectPolicyGroupDispatchesThenReturnsGroups() = runBlocking {
        runtime.dashboardJson = """{"policy_groups":{"profile":"work","groups":[{"name":"g1"}]}}"""
        val groups = api().selectPolicyGroup("work", "g1", "c2")
        assertEquals(Triple("work", "g1", "c2"), runtime.selectedPolicyGroup)
        assertEquals("g1", groups.groups.single().name)
    }

    @Test
    fun createTemporaryRuleForwardsArgsAndDecodesResponse() = runBlocking {
        runtime.temporaryRuleJson = """{"temporary_rule":{"id":"tmp-1","profile":"work"}}"""
        val response = api().createTemporaryRuleFromConnection(
            TrafficConnectionPayload(connId = "c1", profile = "work"),
            action = "block",
            ttlSeconds = 120,
        )
        assertEquals("tmp-1", response.temporaryRule.id)
        assertEquals(
            listOf("c1", "work", "", "block", "auto", "120"),
            runtime.temporaryRuleArgs,
        )
    }

    @Test
    fun connectAndDisconnectInvokeInjectedActions() = runBlocking {
        var connected = false
        var disconnected = false
        val api = api(onConnect = { connected = true }, onDisconnect = { disconnected = true })
        api.connect()
        api.disconnect()
        assertTrue(connected)
        assertTrue(disconnected)
    }

    @Test
    fun readsThrowWhenTunnelNotRunning() {
        ClambhookTunnelSession.clear()
        val api = LocalTunnelApi(onConnect = {}, onDisconnect = {})
        assertThrows(IllegalStateException::class.java) {
            runBlocking { api.status() }
        }
    }
}

/** In-memory [ClambhookTunnelRuntime] for decode/dispatch tests. */
private class FakeTunnelRuntime : ClambhookTunnelRuntime {
    var statusJson = "{}"
    var profilesJson = "{}"
    var serversJson = "{}"
    var rulesJson = "{}"
    var trafficJson = "{}"
    var dashboardJson = "{}"
    var temporaryRuleJson = "{}"

    var recordedProfile: String? = null
    var selectedPolicyGroup: Triple<String, String, String>? = null
    var temporaryRuleArgs: List<String> = emptyList()

    override fun start(configPath: String) = Unit
    override fun stop() = Unit
    override fun reload(configPath: String) = Unit
    override fun injectPacket(packet: ByteArray) = Unit
    override fun isRunning(): Boolean = true

    override fun statusJson(): String = statusJson
    override fun profilesJson(): String = profilesJson
    override fun serversJson(): String = serversJson
    override fun rulesJson(): String = rulesJson
    override fun trafficJson(): String = trafficJson
    override fun dashboardJson(): String = dashboardJson
    override fun developerStatusJson(): String = "{}"
    override fun developerEntriesJson(): String = "{}"
    override fun developerHarJson(): String = "{}"
    override fun developerCaPem(): String = ""

    override fun clearDeveloperEntries() = Unit

    override fun setActiveProfile(name: String) {
        recordedProfile = name
    }

    override fun selectPolicyGroup(profile: String, group: String, chain: String) {
        selectedPolicyGroup = Triple(profile, group, chain)
    }

    override fun createTemporaryRuleFromConnectionJson(
        connId: String,
        profile: String,
        name: String,
        action: String,
        scope: String,
        ttlSeconds: Long,
    ): String {
        temporaryRuleArgs = listOf(connId, profile, name, action, scope, ttlSeconds.toString())
        return temporaryRuleJson
    }

    override fun createRuleFromConnectionJson(
        configPath: String,
        connId: String,
        profile: String,
        name: String,
        action: String,
        scope: String,
    ): String = "{}"

    override fun cleanupRuleJson(
        configPath: String,
        profile: String,
        kind: String,
        ruleName: String,
        targetRuleName: String,
        operation: String,
    ): String = "{}"

    override fun testRuleJson(profile: String, network: String, target: String, source: String): String = "{}"
}
