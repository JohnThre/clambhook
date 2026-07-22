package com.clambhook.linux.store

import com.clambhook.linux.api.ClambhookApi
import com.clambhook.linux.model.*
import com.clambhook.linux.settings.MAX_LOG_RETENTION
import com.clambhook.linux.settings.MIN_LOG_RETENTION
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

const val BANDWIDTH_SAMPLE_LIMIT = 60
const val MAX_LOG_LINES = 200

data class DashboardState(
    val status: StatusPayload = StatusPayload(),
    val profiles: ProfilesPayload = ProfilesPayload(),
    val servers: ServersPayload = ServersPayload(),
    val rules: RulesPayload = RulesPayload(),
    val traffic: TrafficSnapshotPayload = TrafficSnapshotPayload(),
    val bandwidthSamples: List<BandwidthSample> = emptyList(),
    val logs: List<String> = emptyList(),
    val apiOnline: Boolean = false,
    val errorText: String = ""
)

class DashboardStore(
    private val api: ClambhookApi,
    private var logRetention: Int = MAX_LOG_LINES
) {
    private val _state = MutableStateFlow(DashboardState())
    val state: StateFlow<DashboardState> = _state.asStateFlow()
    private val mutex = Mutex()

    /** Test helper: seed traffic connections so applyTrafficBytes can find them. */
    internal fun seedTrafficConnections(connections: List<TrafficConnectionPayload>) {
        _state.value = _state.value.copy(traffic = _state.value.traffic.copy(connections = connections))
    }

    fun activeProfile(): String =
        if (_state.value.profiles.active.isNotEmpty()) _state.value.profiles.active
        else _state.value.status.profile

    fun activeConnections(): Int = _state.value.status.listeners.sumOf { it.activeConns }

    fun currentBandwidth(): BandwidthSample = _state.value.bandwidthSamples.lastOrNull() ?: BandwidthSample()

    suspend fun refreshDashboard() = mutex.withLock {
        try {
            val status = api.status()
            val profiles = api.profiles()
            val servers = api.servers()
            val rules = api.rules()
            val traffic = api.traffic()
            _state.value = _state.value.copy(status = status, profiles = profiles, servers = servers, rules = rules, traffic = traffic, apiOnline = true, errorText = "")
        } catch (e: Exception) {
            _state.value = _state.value.copy(apiOnline = false, errorText = e.message ?: "error")
        }
    }

    suspend fun refreshStatus() = mutex.withLock {
        try {
            val status = api.status()
            val traffic = api.traffic()
            _state.value = _state.value.copy(status = status, traffic = traffic, apiOnline = true, errorText = "")
        } catch (e: Exception) {
            _state.value = _state.value.copy(apiOnline = false, errorText = e.message ?: "error")
        }
    }

    suspend fun connect() {
        try { api.connect(); refreshDashboard() }
        catch (e: Exception) { _state.value = _state.value.copy(apiOnline = false, errorText = e.message ?: "error") }
    }

    suspend fun disconnect() {
        try { api.disconnect(); refreshDashboard() }
        catch (e: Exception) { _state.value = _state.value.copy(apiOnline = false, errorText = e.message ?: "error") }
    }

    suspend fun setActiveProfile(name: String) {
        if (name == activeProfile()) return
        try { api.setActiveProfile(name); refreshDashboard() }
        catch (e: Exception) { _state.value = _state.value.copy(apiOnline = false, errorText = e.message ?: "error") }
    }

    suspend fun createRule(rule: RulePayload) {
        try { val rules = api.createRule(rule); _state.value = _state.value.copy(rules = rules); refreshDashboard() }
        catch (e: Exception) { _state.value = _state.value.copy(apiOnline = false, errorText = e.message ?: "error") }
    }

    suspend fun createRuleFromConnection(connection: TrafficConnectionPayload, rule: RulePayload) {
        try { val rules = api.createRuleFromConnection(connection, rule); _state.value = _state.value.copy(rules = rules); refreshDashboard() }
        catch (e: Exception) { _state.value = _state.value.copy(apiOnline = false, errorText = e.message ?: "error") }
    }

    suspend fun cleanupRule(suggestion: TrafficCleanupSuggestionPayload) {
        try { val rules = api.cleanupRule(suggestion); _state.value = _state.value.copy(rules = rules); refreshDashboard() }
        catch (e: Exception) { _state.value = _state.value.copy(apiOnline = false, errorText = e.message ?: "error") }
    }

    fun applyEvent(event: DaemonEvent) {
        when (event.type) {
            "connection.bytes" -> applyConnectionBytes(event)
            "log.line" -> applyLogLine(event)
            else -> {
                if (event.type.startsWith("connection.") || event.type.startsWith("rule.") || event.type.startsWith("hop.")) {
                    // trigger refresh — caller should call refreshStatus
                }
            }
        }
    }

    fun setLogRetention(value: Int) {
        logRetention = value.coerceIn(MIN_LOG_RETENTION, MAX_LOG_RETENTION)
        _state.value = _state.value.copy(logs = _state.value.logs.takeLast(logRetention))
    }

    fun setError(message: String) {
        if (message.trim().isEmpty()) return
        _state.value = _state.value.copy(errorText = message)
    }

    private fun applyConnectionBytes(event: DaemonEvent) {
        val intervalNs = event.doubleData("interval_ns")
        if (intervalNs <= 0) return
        val seconds = intervalNs / 1_000_000_000.0
        val sample = BandwidthSample(event.doubleData("rx_delta") / seconds, event.doubleData("tx_delta") / seconds)
        val samples = (_state.value.bandwidthSamples + sample).takeLast(BANDWIDTH_SAMPLE_LIMIT)
        _state.value = _state.value.copy(bandwidthSamples = samples)
        applyTrafficBytes(event, seconds)
    }

    private fun applyLogLine(event: DaemonEvent) {
        val line = event.stringData("line")
        if (line.isEmpty()) return
        val logs = (_state.value.logs + line).takeLast(logRetention)
        _state.value = _state.value.copy(logs = logs)
    }

    private fun applyTrafficBytes(event: DaemonEvent, seconds: Double) {
        val connId = event.stringData("conn_id")
        if (connId.isEmpty() || seconds <= 0) return
        val rxDelta = event.doubleData("rx_delta")
        val txDelta = event.doubleData("tx_delta")
        val rxBps = rxDelta / seconds
        val txBps = txDelta / seconds
        val connections = _state.value.traffic.connections.map { c ->
            if (c.connId != connId) c else c.copy(rxBps = rxBps, txBps = txBps,
                rxTotal = c.rxTotal + rxDelta.toULong(), txTotal = c.txTotal + txDelta.toULong())
        }
        val summary = _state.value.traffic.summary
        val updatedSummary = summary.copy(
            rxBps = summary.rxBps + rxBps,
            txBps = summary.txBps + txBps,
            rxTotal = summary.rxTotal + rxDelta.toULong(),
            txTotal = summary.txTotal + txDelta.toULong()
        )
        _state.value = _state.value.copy(traffic = _state.value.traffic.copy(connections = connections, summary = updatedSummary))
    }
}
