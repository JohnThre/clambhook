package com.clambhook.android

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

const val bandwidthSampleLimit = 60
const val maxLogLines = 200

data class DashboardState(
    val status: StatusPayload = StatusPayload(),
    val profiles: ProfilesPayload = ProfilesPayload(),
    val servers: ServersPayload = ServersPayload(),
    val bandwidthSamples: List<BandwidthSample> = emptyList(),
    val logs: List<String> = emptyList(),
    val apiOnline: Boolean = false,
    val errorText: String = ""
) {
    val activeProfile: String
        get() = profiles.active.ifBlank { status.profile }

    val currentBandwidth: BandwidthSample
        get() = bandwidthSamples.lastOrNull() ?: BandwidthSample()

    val activeConnections: Int
        get() = status.listeners.sumOf { it.activeConns }
}

class DashboardRepository(
    private val api: ClambhookApi
) {
    private val _state = MutableStateFlow(DashboardState())
    val state: StateFlow<DashboardState> = _state.asStateFlow()

    suspend fun refreshDashboard() {
        try {
            val status = api.status()
            val profiles = api.profiles()
            val servers = api.servers()
            _state.update {
                it.copy(
                    status = status,
                    profiles = profiles,
                    servers = servers,
                    apiOnline = true,
                    errorText = ""
                )
            }
        } catch (error: Throwable) {
            markOffline(error)
        }
    }

    suspend fun refreshStatus() {
        try {
            val status = api.status()
            _state.update { it.copy(status = status, apiOnline = true, errorText = "") }
        } catch (error: Throwable) {
            markOffline(error)
        }
    }

    suspend fun connect() {
        performAction { api.connect() }
    }

    suspend fun disconnect() {
        performAction { api.disconnect() }
    }

    suspend fun setActiveProfile(name: String) {
        performAction { api.setActiveProfile(name) }
    }

    fun applyEvent(event: DaemonEvent) {
        when (event.type) {
            "connection.bytes" -> applyConnectionBytes(event)
            "log.line" -> applyLogLine(event)
        }
    }

    private suspend fun performAction(action: suspend () -> Unit) {
        try {
            action()
            refreshDashboard()
        } catch (error: Throwable) {
            markOffline(error)
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
            it.copy(bandwidthSamples = samples)
        }
    }

    private fun applyLogLine(event: DaemonEvent) {
        val line = event.data["line"]?.stringValueOrNull() ?: return
        _state.update {
            it.copy(logs = (it.logs + line).takeLast(maxLogLines))
        }
    }
}
