package com.clambhook.linux.ui

import com.clambhook.linux.api.ClambhookApiClient
import com.clambhook.linux.daemon.DaemonSupervisor
import com.clambhook.linux.license.LicenseManager
import com.clambhook.linux.settings.AppSettings
import com.clambhook.linux.settings.FileSettingsStore
import com.clambhook.linux.settings.TokenVault
import com.clambhook.linux.settings.normalized
import com.clambhook.linux.store.DashboardStore
import com.clambhook.linux.event.EventStreamClient
import com.clambhook.linux.model.DaemonEvent
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import java.awt.Desktop
import java.net.URI

class MainViewModel(
    val store: DashboardStore,
    val client: ClambhookApiClient,
    val settingsStore: FileSettingsStore,
    val tokenVault: TokenVault,
    val daemon: DaemonSupervisor,
    val license: LicenseManager,
    initialSettings: AppSettings,
    private val onTokenLoaded: (String) -> Unit
) {
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private val eventStream = EventStreamClient()
    private var refreshJob: Job? = null
    private var eventReconnectAttempts = 0
    private var eventReconnectJob: Job? = null
    private var closing = false
    private var eventStreamActive = false

    private val _settings = MutableStateFlow(initialSettings)
    val settings: StateFlow<AppSettings> = _settings.asStateFlow()
    var apiToken: String = ""
        private set

    init {
        eventStream.onEvent = { event -> store.applyEvent(event); scope.launch { store.refreshStatus() } }
        eventStream.onFailed = { msg -> store.setError("events: $msg"); scheduleEventReconnect() }
        eventStream.onClosed = { if (eventStreamActive && !closing) scheduleEventReconnect() }
        scope.launch {
            try {
                apiToken = tokenVault.readToken()
                onTokenLoaded(apiToken)
            } catch (e: Exception) { apiToken = "" }
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

    private fun maybeLaunchDaemon() { if (_settings.value.launchDaemonOnStart) startDaemon() }

    fun saveSettings(newSettings: AppSettings, newToken: String) {
        settingsStore.save(newSettings)
        _settings.value = newSettings.normalized()
        store.setLogRetention(_settings.value.logRetention)
        scope.launch {
            tokenVault.saveToken(newToken)
            apiToken = newToken.trim()
            onTokenLoaded(apiToken)
            client.configureBaseUrl(_settings.value.apiEndpoint)
            scheduleRefresh()
            startEventStream()
            store.refreshDashboard()
        }
    }

    fun activateLicense(key: String, email: String) { scope.launch { license.activate(key, email) } }
    fun deactivateDevice() { scope.launch { license.deactivateCurrentDevice() } }
    fun resolvePrompt(id: String, action: String, scope: String, matchHost: Boolean) {
        this.scope.launch { client.resolvePrompt(id, action, scope, matchHost); refreshActivePage() }
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
        eventStream.start(client.eventsUri(), client.authorizationHeader())
    }

    private fun stopEventStream() {
        eventStreamActive = false
        eventReconnectJob?.cancel(); eventReconnectJob = null
        eventStream.stop()
    }

    private fun scheduleEventReconnect() {
        if (closing || !_settings.value.eventStreamEnabled) return
        eventReconnectAttempts++
        val delayMs = (3.0 * Math.pow(2.0, (eventReconnectAttempts - 1).toDouble())).toLong().coerceAtMost(30) * 1000L
        eventReconnectJob = scope.launch { delay(delayMs); startEventStream() }
    }

    fun close() {
        closing = true
        stopEventStream()
        refreshJob?.cancel()
        if (_settings.value.stopDaemonOnExit) daemon.stop()
        scope.cancel()
    }

    fun openUrl(url: String) { try { Desktop.getDesktop().browse(URI(url)) } catch (e: Exception) {} }
}