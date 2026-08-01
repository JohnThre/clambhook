package com.clambhook.linux.ui

import com.clambhook.linux.api.ClambhookApi
import com.clambhook.linux.api.ClambhookApiClient
import com.clambhook.linux.daemon.DaemonSupervisor
import com.clambhook.linux.license.LicenseManager
import com.clambhook.linux.settings.AppSettings
import com.clambhook.linux.settings.FileSettingsStore
import com.clambhook.linux.settings.TokenVault
import com.clambhook.linux.settings.normalized
import com.clambhook.linux.store.DashboardStore
import com.clambhook.linux.event.EventStream
import com.clambhook.linux.event.EventStreamClient
import com.clambhook.linux.model.ConditionerPayload
import com.clambhook.linux.model.ConditionerUpdateRequest
import com.clambhook.linux.model.DaemonEvent
import com.clambhook.linux.model.PolicyGroupsPayload
import com.clambhook.linux.model.PromptsPayload
import com.clambhook.linux.model.DnsPayload
import com.clambhook.linux.model.DeveloperStatusPayload
import com.clambhook.linux.model.DeveloperEntryPayload
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import java.awt.Desktop
import java.net.URI

class MainViewModel(
    val store: DashboardStore,
    val client: ClambhookApi,
    val settingsStore: FileSettingsStore,
    val tokenVault: TokenVault,
    val daemon: DaemonSupervisor,
    val license: LicenseManager,
    initialSettings: AppSettings,
    private val apiTokenRef: java.util.concurrent.atomic.AtomicReference<String>,
    eventStream: EventStream = EventStreamClient(),
    dispatcher: CoroutineDispatcher = Dispatchers.Default
) {
    internal val scope = CoroutineScope(SupervisorJob() + dispatcher)
    private val eventStream: EventStream = eventStream

    private var refreshJob: Job? = null
    private var eventReconnectAttempts = 0
    private var eventReconnectJob: Job? = null
    private var closing = false
    private var eventStreamActive = false
    private var inFlightSettingsJob: Job? = null

    private val _settings = MutableStateFlow(initialSettings)
    val settings: StateFlow<AppSettings> = _settings.asStateFlow()
    val apiToken: String get() = apiTokenRef.get()

    private val _conditioner = MutableStateFlow(ConditionerUiState())
    val conditioner: StateFlow<ConditionerUiState> = _conditioner.asStateFlow()

    private var eventReconnectScheduled = false

    init {
        eventStream.onEvent = { event -> scope.launch { store.applyEvent(event); store.refreshStatus() } }
        eventStream.onFailed = { msg ->
            eventReconnectScheduled = true
            store.setError("events: $msg")
            scheduleEventReconnect()
        }
        eventStream.onClosed = {
            if (eventStreamActive && !closing && !eventReconnectScheduled) scheduleEventReconnect()
            eventReconnectScheduled = false
        }
        scope.launch {
            try {
                apiTokenRef.set(tokenVault.readToken())
            } catch (e: Exception) {
                apiTokenRef.set("")
            }
            maybeLaunchDaemon()
            store.refreshDashboard()
            scheduleRefresh()
            startEventStream()
            license.start()
        }
    }

    fun refreshNow() { scope.launch { store.refreshDashboard() } }

    fun connect() { scope.launch { store.connect() } }
    fun disconnect() { scope.launch { store.disconnect() } }
    fun setActiveProfile(name: String) { scope.launch { store.setActiveProfile(name) } }
    fun createRule(rule: com.clambhook.linux.model.RulePayload) { scope.launch { store.createRule(rule) } }
    fun createRuleFromConnection(connection: com.clambhook.linux.model.TrafficConnectionPayload, rule: com.clambhook.linux.model.RulePayload) { scope.launch { store.createRuleFromConnection(connection, rule) } }
    fun cleanupRule(suggestion: com.clambhook.linux.model.TrafficCleanupSuggestionPayload) { scope.launch { store.cleanupRule(suggestion) } }

    fun toggleDaemon() {
        if (daemon.isRunning) daemon.stop()
        else startDaemon()
    }

    private fun startDaemon() {
        scope.launch {
            try { daemon.start(_settings.value, apiToken, DaemonSupervisor.defaultAppBaseDir(), license.daemonSnapshotPath()); store.refreshDashboard() }
            catch (e: Exception) { store.setError(e.message ?: "error") }
        }
    }

    suspend fun loadPolicyGroups(): PolicyGroupsPayload = client.policyGroups()
    suspend fun testPolicyGroups(group: String): PolicyGroupsPayload = client.testPolicyGroups(group)
    suspend fun selectPolicyGroup(group: String, chain: String): PolicyGroupsPayload = client.selectPolicyGroup(group, chain)
    suspend fun loadPendingPrompts(): PromptsPayload = client.pendingPrompts()
    suspend fun loadDns(): DnsPayload = client.dns()
    suspend fun loadCaptureStatus(): DeveloperStatusPayload = client.developerStatus()
    suspend fun setCaptureEnabled(enabled: Boolean): DeveloperStatusPayload = client.setDeveloperCapture(enabled)
    suspend fun loadCaptureEntries(): List<DeveloperEntryPayload> = client.developerEntries()
    suspend fun loadCaptureEntry(id: String): DeveloperEntryPayload = client.developerEntry(id)
    suspend fun repeatCaptureEntry(id: String): DeveloperEntryPayload = client.repeatDeveloperEntry(id)

    private fun maybeLaunchDaemon() { if (_settings.value.launchDaemonOnStart) startDaemon() }

    fun saveSettings(newSettings: AppSettings, newToken: String) {
        val normalized = newSettings.normalized()
        val trimmed = newToken.trim()
        _settings.value = normalized
        store.setLogRetention(normalized.logRetention)
        apiTokenRef.set(trimmed)
        client.configureBaseUrl(normalized.apiEndpoint)
        inFlightSettingsJob = scope.launch {
            settingsStore.save(normalized)
            tokenVault.saveToken(newToken)
            scheduleRefresh()
            startEventStream()
            store.refreshDashboard()
        }
    }

    fun loadConditioner() {
        scope.launch {
            _conditioner.value = _conditioner.value.copy(loading = true, error = "")
            try {
                val payload = client.conditioner()
                _conditioner.value = ConditionerUiState(payload = payload, loading = false)
            } catch (e: Exception) {
                _conditioner.value = _conditioner.value.copy(loading = false, error = e.message ?: "error")
            }
        }
    }

    fun updateConditioner(request: ConditionerUpdateRequest) {
        scope.launch {
            _conditioner.value = _conditioner.value.copy(loading = true, error = "")
            try {
                val payload = client.updateConditioner(request)
                _conditioner.value = ConditionerUiState(payload = payload, loading = false)
            } catch (e: Exception) {
                _conditioner.value = _conditioner.value.copy(loading = false, error = e.message ?: "error")
            }
        }
    }

    fun activateLicense(key: String, email: String) { scope.launch { license.activate(key, email) } }
    fun deactivateDevice() { scope.launch { license.deactivateCurrentDevice() } }
    fun resolvePrompt(id: String, action: String, resolutionScope: String, matchHost: Boolean) {
        this.scope.launch { client.resolvePrompt(id, action, resolutionScope, matchHost); refreshActivePage() }
    }

    private var activePage = "now"
    fun onPageChanged(page: String) { activePage = page; scope.launch { refreshActivePage() } }

    private suspend fun refreshActivePage() {
        when (activePage) {
            "policies" -> { try { client.policyGroups() } catch (e: Exception) {} }
            "firewall" -> { try { client.pendingPrompts() } catch (e: Exception) {} }
            "dns" -> { try { client.dns() } catch (e: Exception) {} }
            "capture" -> { try { client.developerStatus() } catch (e: Exception) {} }
        }
    }

    private fun scheduleRefresh() {
        refreshJob?.cancel()
        refreshJob = scope.launch {
            while (isActive) {
                delay(_settings.value.refreshIntervalSeconds * 1000L)
                store.refreshStatus()
                refreshActivePage()
            }
        }
    }

    private fun startEventStream() {
        stopEventStream()
        if (!_settings.value.eventStreamEnabled) return
        eventStreamActive = true
        eventReconnectAttempts = 0
        eventStream.start(client.eventsUri(), client.authorizationHeader())
    }

    private fun stopEventStream() {
        eventStreamActive = false
        eventReconnectJob?.cancel(); eventReconnectJob = null
        eventReconnectScheduled = false
        eventStream.stop()
    }

    private fun scheduleEventReconnect() {
        if (closing || !_settings.value.eventStreamEnabled) return
        eventReconnectAttempts++
        val delayMs = (3.0 * Math.pow(2.0, (eventReconnectAttempts - 1).toDouble())).toLong().coerceAtMost(30) * 1000L
        eventReconnectJob = scope.launch { delay(delayMs); startEventStream() }
    }

    fun close() {
        if (closing) return
        closing = true
        stopEventStream()
        refreshJob?.cancel()
        inFlightSettingsJob?.cancel()
        if (_settings.value.stopDaemonOnExit) daemon.stop()
        scope.cancel()
    }

    fun openUrl(url: String) { try { Desktop.getDesktop().browse(URI(url)) } catch (e: Exception) {} }
}

data class ConditionerUiState(
    val payload: ConditionerPayload? = null,
    val loading: Boolean = false,
    val error: String = ""
)