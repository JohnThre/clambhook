package com.clambhook.android

import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import okhttp3.WebSocket

class DashboardViewModel(
    private val repository: DashboardRepository,
    private val apiClient: ClambhookApiClient
) : ViewModel() {
    val state: StateFlow<DashboardState> = repository.state.stateIn(
        scope = viewModelScope,
        started = SharingStarted.WhileSubscribed(5_000),
        initialValue = repository.state.value
    )

    private var pollingJob: Job? = null
    private var webSocket: WebSocket? = null

    fun refresh() {
        viewModelScope.launch { repository.refreshDashboard(showProgress = true) }
    }

    fun connect() {
        viewModelScope.launch { repository.connect() }
    }

    fun disconnect() {
        viewModelScope.launch { repository.disconnect() }
    }

    fun setActiveProfile(name: String) {
        viewModelScope.launch { repository.setActiveProfile(name) }
    }

    fun startPolling(intervalSeconds: Int) {
        pollingJob?.cancel()
        pollingJob = viewModelScope.launch {
            repository.refreshDashboard()
            while (true) {
                delay(intervalSeconds.coerceIn(2, 60) * 1_000L)
                repository.refreshStatus()
            }
        }
    }

    fun startEventStream(enabled: Boolean) {
        webSocket?.close(1000, null)
        webSocket = null
        if (!enabled) {
            repository.setEventStreamState("Events paused")
            return
        }
        repository.setEventStreamState("Events listening")
        webSocket = apiClient.openEventStream(
            onEvent = { event ->
                repository.setEventStreamState("Events listening")
                repository.applyEvent(event)
            },
            onFailure = { error ->
                val message = error.message ?: error.toString()
                repository.setEventStreamState("Events disconnected", message)
                viewModelScope.launch {
                    repository.applyEvent(
                        DaemonEvent(
                            shardId = 0u,
                            lamport = 0u,
                            tsNs = 0,
                            type = "log.line",
                            data = mapOf("line" to kotlinx.serialization.json.JsonPrimitive("events: $message"))
                        )
                    )
                }
            }
        )
    }

    override fun onCleared() {
        pollingJob?.cancel()
        webSocket?.close(1000, null)
    }
}

class DashboardViewModelFactory(
    private val apiClient: ClambhookApiClient
) : ViewModelProvider.Factory {
    @Suppress("UNCHECKED_CAST")
    override fun <T : ViewModel> create(modelClass: Class<T>): T {
        return DashboardViewModel(DashboardRepository(apiClient), apiClient) as T
    }
}
