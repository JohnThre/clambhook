package com.clambhook.android

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

const val bandwidthSampleLimit = 60
const val maxLogLines = 200

enum class DashboardAction {
    Refresh,
    Connect,
    Disconnect,
    SwitchProfile
}

data class DashboardState(
    val status: StatusPayload = StatusPayload(),
    val profiles: ProfilesPayload = ProfilesPayload(),
    val servers: ServersPayload = ServersPayload(),
    val policyGroups: PolicyGroupsPayload = PolicyGroupsPayload(),
    val rules: RulesPayload = RulesPayload(),
    val ruleSets: RuleSetsPayload = RuleSetsPayload(),
    val traffic: TrafficSnapshotPayload = TrafficSnapshotPayload(),
    val developerStatus: DeveloperStatusPayload = DeveloperStatusPayload(),
    val developerEntries: List<DeveloperEntryPayload> = emptyList(),
    val bandwidthSamples: List<BandwidthSample> = emptyList(),
    val logs: List<String> = emptyList(),
    val apiOnline: Boolean = false,
    val errorText: String = "",
    val isRefreshing: Boolean = false,
    val actionInProgress: DashboardAction? = null,
    val pendingProfile: String = "",
    val lastUpdatedEpochMillis: Long = 0,
    val eventStreamStatus: String = "Events paused",
    val eventStreamError: String = ""
) {
    val activeProfile: String
        get() = profiles.active.ifBlank { status.profile }

    val currentBandwidth: BandwidthSample
        get() = bandwidthSamples.lastOrNull() ?: BandwidthSample()

    val activeConnections: Int
        get() = status.listeners.sumOf { it.activeConns }

    val isBusy: Boolean
        get() = isRefreshing || actionInProgress != null
}

class DashboardRepository(
    private val api: ClambhookApi
) {
    private val _state = MutableStateFlow(DashboardState())
    val state: StateFlow<DashboardState> = _state.asStateFlow()

    suspend fun refreshDashboard(showProgress: Boolean = false) {
        if (showProgress) {
            _state.update {
                it.copy(
                    isRefreshing = true,
                    actionInProgress = DashboardAction.Refresh,
                    errorText = ""
                )
            }
        }
        try {
            val status = api.status()
            val profiles = api.profiles()
            val servers = api.servers()
            val policyGroups = api.policyGroups()
            val rules = api.rules()
            val ruleSets = runCatching { api.ruleSets() }.getOrDefault(RuleSetsPayload(profile = rules.profile, statuses = rules.ruleSets))
            val traffic = api.traffic()
            val developerStatus = runCatching { api.developerStatus() }.getOrDefault(DeveloperStatusPayload())
            val developerEntries = runCatching { api.developerEntries().entries }.getOrDefault(emptyList())
            _state.update {
                it.copy(
                    status = status,
                    profiles = profiles,
                    servers = servers,
                    policyGroups = policyGroups,
                    rules = rules,
                    ruleSets = ruleSets,
                    traffic = traffic,
                    developerStatus = developerStatus,
                    developerEntries = developerEntries,
                    apiOnline = true,
                    errorText = "",
                    lastUpdatedEpochMillis = System.currentTimeMillis()
                )
            }
        } catch (error: Throwable) {
            markOffline(error)
        } finally {
            if (showProgress) {
                _state.update { it.copy(isRefreshing = false, actionInProgress = null) }
            }
        }
    }

    suspend fun refreshStatus() {
        try {
            val status = api.status()
            val policyGroups = api.policyGroups()
            val traffic = api.traffic()
            val ruleSets = runCatching { api.ruleSets() }.getOrDefault(_state.value.ruleSets)
            val developerStatus = runCatching { api.developerStatus() }.getOrDefault(_state.value.developerStatus)
            val developerEntries = runCatching { api.developerEntries().entries }.getOrDefault(_state.value.developerEntries)
            _state.update {
                it.copy(
                    status = status,
                    policyGroups = policyGroups,
                    traffic = traffic,
                    ruleSets = ruleSets,
                    developerStatus = developerStatus,
                    developerEntries = developerEntries,
                    apiOnline = true,
                    errorText = "",
                    lastUpdatedEpochMillis = System.currentTimeMillis()
                )
            }
        } catch (error: Throwable) {
            markOffline(error)
        }
    }

    suspend fun connect() {
        performAction(DashboardAction.Connect) { api.connect() }
    }

    suspend fun disconnect() {
        performAction(DashboardAction.Disconnect) { api.disconnect() }
    }

    suspend fun setActiveProfile(name: String) {
        performAction(DashboardAction.SwitchProfile, pendingProfile = name) { api.setActiveProfile(name) }
    }

    suspend fun createRule(rule: RulePayload) {
        performAction(DashboardAction.Refresh) { api.createRule(rule) }
    }

    suspend fun createRuleFromConnection(connection: TrafficConnectionPayload, rule: RulePayload) {
        performAction(DashboardAction.Refresh) { api.createRuleFromConnection(connection, rule) }
    }

    suspend fun createTemporaryRuleFromConnection(connection: TrafficConnectionPayload, action: String) {
        performAction(DashboardAction.Refresh) { api.createTemporaryRuleFromConnection(connection, action) }
    }

    suspend fun cleanupRule(suggestion: TrafficCleanupSuggestionPayload) {
        performAction(DashboardAction.Refresh) { api.cleanupRule(suggestion) }
    }

    suspend fun replaceRules(profile: String, rules: List<RulePayload>) {
        performAction(DashboardAction.Refresh) { api.replaceRules(profile, rules) }
    }

    suspend fun selectPolicyGroup(profile: String, group: String, chain: String) {
        performAction(DashboardAction.Refresh) { api.selectPolicyGroup(profile, group, chain) }
    }

    suspend fun replaceRuleSets(profile: String, ruleSets: List<RuleSetPayload>) {
        performAction(DashboardAction.Refresh) { api.replaceRuleSets(profile, ruleSets) }
    }

    suspend fun refreshRuleSets(profile: String, names: List<String> = emptyList()) {
        performAction(DashboardAction.Refresh) { api.refreshRuleSets(profile, names) }
    }

    suspend fun clearDeveloperEntries() {
        performAction(DashboardAction.Refresh) { api.clearDeveloperEntries() }
    }

    suspend fun developerHar(): String = api.developerHar()

    fun applyEvent(event: DaemonEvent): Boolean {
        when (event.type) {
            "connection.bytes" -> {
                applyConnectionBytes(event)
                return false
            }
            "log.line" -> {
                applyLogLine(event)
                return false
            }
            else -> return event.type.startsWith("connection.") || event.type.startsWith("rule.") || event.type.startsWith("hop.")
        }
        return false
    }

    fun setEventStreamState(status: String, error: String = "") {
        _state.update { it.copy(eventStreamStatus = status, eventStreamError = error) }
    }

    private suspend fun performAction(
        actionType: DashboardAction,
        pendingProfile: String = "",
        action: suspend () -> Unit
    ) {
        _state.update {
            it.copy(
                actionInProgress = actionType,
                pendingProfile = pendingProfile,
                errorText = ""
            )
        }
        try {
            action()
            refreshDashboard()
        } catch (error: Throwable) {
            markOffline(error)
        } finally {
            _state.update {
                it.copy(
                    actionInProgress = null,
                    pendingProfile = ""
                )
            }
        }
    }

    private fun markOffline(error: Throwable) {
        _state.update { it.copy(apiOnline = false, errorText = error.message ?: error.toString()) }
    }

    private fun applyConnectionBytes(event: DaemonEvent) {
        val rxDelta = event.data["rx_delta"]?.doubleValueOrNull() ?: return
        val txDelta = event.data["tx_delta"]?.doubleValueOrNull() ?: return
        val intervalNs = event.data["interval_ns"]?.doubleValueOrNull() ?: return
        if (intervalNs <= 0) {
            return
        }
        val seconds = intervalNs / 1_000_000_000.0
        val sample = BandwidthSample(rxBps = rxDelta / seconds, txBps = txDelta / seconds)
        _state.update {
            val samples = (it.bandwidthSamples + sample).takeLast(bandwidthSampleLimit)
            it.copy(
                bandwidthSamples = samples,
                traffic = updateTrafficBytes(it.traffic, event, sample, rxDelta, txDelta)
            )
        }
    }

    private fun applyLogLine(event: DaemonEvent) {
        val line = event.data["line"]?.stringValueOrNull() ?: return
        _state.update {
            it.copy(logs = (it.logs + line).takeLast(maxLogLines))
        }
    }
}

private fun updateTrafficBytes(
    traffic: TrafficSnapshotPayload,
    event: DaemonEvent,
    sample: BandwidthSample,
    rxDelta: Double,
    txDelta: Double
): TrafficSnapshotPayload {
    val connId = event.data["conn_id"]?.stringValueOrNull() ?: return traffic
    var oldRxBps = 0.0
    var oldTxBps = 0.0
    var found = false
    val connections = traffic.connections.map { connection ->
        if (connection.connId != connId) {
            connection
        } else {
            found = true
            oldRxBps = connection.rxBps
            oldTxBps = connection.txBps
            connection.copy(
                rxBps = sample.rxBps,
                txBps = sample.txBps,
                rxTotal = connection.rxTotal + rxDelta.toLong(),
                txTotal = connection.txTotal + txDelta.toLong()
            )
        }
    }
    if (!found) {
        return traffic
    }
    return traffic.copy(
        summary = traffic.summary.copy(
            rxBps = traffic.summary.rxBps + sample.rxBps - oldRxBps,
            txBps = traffic.summary.txBps + sample.txBps - oldTxBps,
            rxTotal = traffic.summary.rxTotal + rxDelta.toLong(),
            txTotal = traffic.summary.txTotal + txDelta.toLong()
        ),
        connections = connections
    )
}
