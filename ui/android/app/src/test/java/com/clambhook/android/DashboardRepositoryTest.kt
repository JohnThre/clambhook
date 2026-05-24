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
                        servers = listOf(ServerPayload(name = "london", address = "uk.example:443", protocol = "vless"))
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
        assertEquals(3, api.trafficCalls)
        assertNull(repository.state.value.actionInProgress)
        assertEquals("", repository.state.value.pendingProfile)
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
    private val traffic: TrafficSnapshotPayload = TrafficSnapshotPayload(),
    private val error: Throwable? = null
) : ClambhookApi {
    val actions = mutableListOf<String>()
    var statusCalls = 0
    var profileCalls = 0
    var serverCalls = 0
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
}
