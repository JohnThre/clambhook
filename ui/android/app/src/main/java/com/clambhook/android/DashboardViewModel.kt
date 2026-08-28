// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.io.Closeable

class DashboardViewModel(
    private val repository: DashboardRepository,
    private val eventStream: ClambhookEventStream?
) : ViewModel() {
    private val _uiState = MutableStateFlow(repository.state.value)
    val uiState: StateFlow<DashboardState> = _uiState.asStateFlow()

    init {
        viewModelScope.launch {
            repository.state.collect { _uiState.value = it }
        }
    }

    private var pollingJob: Job? = null
    private var eventStreamHandle: Closeable? = null

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

    fun selectPolicyGroup(group: String, chain: String) {
        viewModelScope.launch { repository.selectPolicyGroup(uiState.value.activeProfile, group, chain) }
    }

    fun createRule(rule: RulePayload) {
        viewModelScope.launch { repository.createRule(rule) }
    }

    fun createRuleFromConnection(connection: TrafficConnectionPayload, rule: RulePayload) {
        viewModelScope.launch { repository.createRuleFromConnection(connection, rule) }
    }

    fun createTemporaryRuleFromConnection(connection: TrafficConnectionPayload, action: String) {
        viewModelScope.launch { repository.createTemporaryRuleFromConnection(connection, action) }
    }

    fun cleanupRule(suggestion: TrafficCleanupSuggestionPayload) {
        viewModelScope.launch { repository.cleanupRule(suggestion) }
    }

    fun replaceRules(profile: String, rules: List<RulePayload>) {
        viewModelScope.launch { repository.replaceRules(profile, rules) }
    }

    fun clearDeveloperEntries() {
        viewModelScope.launch { repository.clearDeveloperEntries() }
    }

    suspend fun developerHar(): String = repository.developerHar()

    // Server-side capture flow-list filter (method/status/host/scheme/content-type/
    // free-text/errors). Sets the filter and reloads the capture list with it.
    fun applyDeveloperEntriesFilter(filter: DeveloperEntriesFilter) {
        viewModelScope.launch {
            repository.setDeveloperEntriesFilter(filter)
            repository.refreshStatus()
        }
    }

    suspend fun copyEntryCurl(id: String): String = repository.developerEntryCurl(id)
    suspend fun importCurl(text: String): ParsedCurlResponse = repository.importCurl(text)
    suspend fun sendComposed(request: ComposedRequestPayload): DeveloperEntryPayload = repository.sendComposed(request)
    suspend fun repeatDeveloperEntry(id: String): DeveloperEntryPayload = repository.repeatDeveloperEntry(id)

    fun loadConditioner() {
        viewModelScope.launch { repository.loadConditioner() }
    }

    fun updateConditioner(request: ConditionerUpdateRequest) {
        viewModelScope.launch { repository.updateConditioner(request) }
    }

    fun loadPendingPrompts() { viewModelScope.launch { repository.loadPendingPrompts() } }
    fun resolvePrompt(id: String, action: String, scope: String, matchHost: Boolean, matchPort: Boolean, matchProtocol: Boolean) {
        viewModelScope.launch { repository.resolvePrompt(id, action, scope, matchHost, matchPort, matchProtocol) }
    }
    fun loadSilentDecisions() { viewModelScope.launch { repository.loadSilentDecisions() } }
    fun promoteSilentDecision(id: String, scope: String, matchHost: Boolean, matchPort: Boolean, matchProtocol: Boolean) {
        viewModelScope.launch { repository.promoteSilentDecision(id, scope, matchHost, matchPort, matchProtocol) }
    }
    fun loadTraffic(filter: TrafficMonitorFilter) { viewModelScope.launch { repository.loadTraffic(filter) } }

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
        eventStreamHandle?.close()
        eventStreamHandle = null
        val stream = eventStream
        if (!enabled || stream == null) {
            repository.setEventStreamState(if (stream == null) "Live events unavailable" else "Events paused")
            return
        }
        repository.setEventStreamState("Events listening")
        eventStreamHandle = stream.openEventStream(
            onEvent = { event ->
                repository.setEventStreamState("Events listening")
                if (repository.applyEvent(event)) {
                    viewModelScope.launch { repository.refreshStatus() }
                }
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
        eventStreamHandle?.close()
    }
}

class DashboardViewModelFactory(
    private val api: ClambhookApi,
    private val eventStream: ClambhookEventStream?
) : ViewModelProvider.Factory {
    @Suppress("UNCHECKED_CAST")
    override fun <T : ViewModel> create(modelClass: Class<T>): T {
        return DashboardViewModel(DashboardRepository(api), eventStream) as T
    }
}
