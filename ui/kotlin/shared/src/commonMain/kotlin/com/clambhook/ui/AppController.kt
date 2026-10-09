// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import com.clambhook.ui.json.Json
import com.clambhook.ui.model.DashboardData
import com.clambhook.ui.model.DashboardLoader
import com.clambhook.ui.platform.PlatformServices
import com.clambhook.ui.platform.PlatformServices.Capability
import com.clambhook.ui.runtime.Document
import com.clambhook.ui.runtime.EventCursor
import com.clambhook.ui.runtime.RuntimeClient
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.catch
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/** Top-level sections, in navigation and keyboard-shortcut order (Ctrl/⌘ + 1–9). */
enum class Page(val title: String, val shortTitle: String) {
    DASHBOARD("Dashboard", "Home"),
    PROFILES("Profiles", "Profiles"),
    ACTIVITY("Activity", "Activity"),
    ROUTING("Routing", "Routing"),
    RULES("Rules", "Rules"),
    NETWORK("Network", "Network"),
    PROMPTS("Prompts", "Prompts"),
    DEVELOPER("Developer", "Developer"),
    SETTINGS("Settings", "Settings"),
}

/** Editable or read-only JSON/TOML/PEM documents shown in the views. */
enum class Doc {
    CONFIG_TRANSFER, DECISIONS, TEMPORARY_RULES, POLICY_GROUPS, RULES, RULE_SETS,
    SUBSCRIPTIONS, DNS, SETTINGS, CONDITIONER, PENDING_PROMPTS, PROMPT_DECISIONS,
    DEVELOPER_SETTINGS, DEVELOPER_MAP_RULES, DEVELOPER_BREAKPOINT_RULES,
    DEVELOPER_REWRITE_RULES, DEVELOPER_PENDING_BREAKPOINTS, DEVELOPER_CA,
    DEVELOPER_COMPOSER, DEVELOPER_RESULT, LICENSE, UPDATE, CONVERTER_SOURCE,
}

/**
 * UI state and actions for the shared controller. All runtime and platform
 * calls are suspending; state lives in Compose snapshot state so any thread
 * may complete an operation without blocking the UI.
 */
class AppController(
    val runtime: RuntimeClient,
    val platform: PlatformServices,
    private val scope: CoroutineScope,
    private val refreshIntervalMillis: Long = 3_000,
) {
    private val loader = DashboardLoader(runtime)

    var data by mutableStateOf(DashboardData.EMPTY)
        private set
    var page by mutableStateOf(Page.DASHBOARD)
        private set
    var errorMessage by mutableStateOf<String?>(null)
        private set
    var statusText by mutableStateOf("Checking…")
        private set
    var statusKind by mutableStateOf(StatusKind.PENDING)
        private set
    var refreshing by mutableStateOf(false)
        private set
    var lastUpdated by mutableStateOf("Not refreshed yet")
        private set
    var connectionPending by mutableStateOf(false)
        private set
    var connectionStarting by mutableStateOf(false)
        private set
    var hasLoaded by mutableStateOf(false)
        private set
    var supporterBadge by mutableStateOf("Supporter status unavailable")
        private set
    var supporterThanks by mutableStateOf("")
        private set
    var outlineKeyRequest by mutableStateOf<String?>(null)
        private set

    val documents = mutableStateMapOf<Doc, String>().apply {
        put(Doc.DEVELOPER_COMPOSER, "{\n  \"method\": \"GET\",\n  \"url\": \"https://example.com/\"\n}")
    }

    private var refreshJob: Job? = null
    private var loopJob: Job? = null
    private var eventsJob: Job? = null
    private var outlineLinkRead = false

    fun document(key: Doc): String = documents[key].orEmpty()

    fun setDocument(key: Doc, value: String) {
        documents[key] = value
    }

    /** Starts periodic refresh and, where supported, the live event stream. */
    fun start() {
        refresh()
        showPage(page)
        if (loopJob == null) {
            loopJob = scope.launch {
                while (isActive) {
                    delay(refreshIntervalMillis)
                    refresh()
                }
            }
        }
        startLiveEvents()
    }

    fun close() {
        loopJob?.cancel()
        eventsJob?.cancel()
        refreshJob?.cancel()
    }

    val connectEnabled: Boolean
        get() = !connectionPending && runtime.supportsConnectionControl && !missingProfile

    val missingProfile: Boolean
        get() = !data.running && data.activeProfile.isBlank()

    val connectLabel: String
        get() = when {
            connectionPending && connectionStarting -> "Connecting…"
            connectionPending -> "Disconnecting…"
            !hasLoaded -> "Connect"
            data.running -> "Disconnect"
            else -> "Connect"
        }

    val connectHint: String
        get() = when {
            !hasLoaded -> "Waiting for ClambHook runtime status"
            connectionPending -> "Wait for the current connection action to finish"
            missingProfile -> "Add and select a profile before connecting"
            data.running -> "Disconnect ClambHook"
            else -> "Connect with the active profile"
        }

    fun refresh() {
        consumePendingOutlineLink()
        if (refreshJob?.isActive == true) return
        refreshing = true
        lastUpdated = "Refreshing dashboard…"
        refreshJob = scope.launch {
            try {
                val loaded = loader.load()
                data = loaded
                hasLoaded = true
                clearError()
                statusText = if (loaded.running) "Connected" else "Disconnected"
                statusKind = if (loaded.running) StatusKind.CONNECTED else StatusKind.DISCONNECTED
                lastUpdated = "Updated just now · ${runtime.displayName}"
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                showError(error)
                statusText = "Unavailable"
                statusKind = StatusKind.ERROR
            } finally {
                refreshing = false
            }
        }
    }

    private fun consumePendingOutlineLink() {
        if (outlineLinkRead) return
        outlineLinkRead = true
        scope.launch {
            try {
                val value = platform.takePendingOutlineAccessKey()
                if (value.isNotBlank()) showOutlineAccessKey(value)
            } catch (error: CancellationException) {
                throw error
            } catch (_: Throwable) {
                // A missing link is not an error; the next refresh retries.
            } finally {
                outlineLinkRead = false
            }
        }
    }

    private fun startLiveEvents() {
        if (!runtime.supportsLiveEvents || eventsJob?.isActive == true) return
        eventsJob = scope.launch {
            while (isActive) {
                try {
                    runtime.liveEvents(EventCursor.beginning())
                        .catch { /* reconnect below */ }
                        .collect { refresh() }
                } catch (error: CancellationException) {
                    throw error
                } catch (_: Throwable) {
                    // Reconnect after a short backoff without blocking the UI.
                }
                delay(3_000)
            }
        }
    }

    fun restartLiveEvents() {
        eventsJob?.cancel()
        eventsJob = null
        startLiveEvents()
    }

    fun showPage(target: Page) {
        page = target
        pageRefresh(target)
    }

    /** Routes an external key into review; it never imports or connects. */
    fun showOutlineAccessKey(accessKey: String) {
        val value = accessKey.trim()
        if (!value.startsWith("ss://", ignoreCase = true) &&
            !value.startsWith("ssconf://", ignoreCase = true)) {
            showError(IllegalArgumentException("External Outline links must use ss:// or ssconf://"))
            return
        }
        outlineKeyRequest = value
        showPage(Page.PROFILES)
    }

    fun outlineKeyRequestHandled() {
        outlineKeyRequest = null
    }

    fun changeConnectionState() {
        if (!runtime.supportsConnectionControl || !connectEnabled) return
        connectionPending = true
        connectionStarting = !data.running
        val stopping = data.running
        scope.launch {
            try {
                if (stopping) {
                    platform.stopVpn()
                } else {
                    check(platform.requestVpnConsent()) { "VPN consent was not granted" }
                    platform.startVpn()
                }
                clearError()
                connectionPending = false
                refresh()
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                connectionPending = false
                showError(error)
            }
        }
    }

    fun mutate(afterSuccess: (() -> Unit)? = { refresh() }, operation: suspend () -> Any?) {
        scope.launch {
            try {
                operation()
                clearError()
                afterSuccess?.invoke()
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                showError(error)
            }
        }
    }

    fun loadDocument(key: Doc, operation: suspend () -> Document) =
        loadText(key) { operation().rawJson }

    fun loadText(key: Doc, operation: suspend () -> String) {
        scope.launch {
            try {
                documents[key] = operation()
                clearError()
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                showError(error)
            }
        }
    }

    /** Validates [key] as JSON, applies it with [saver], then reloads it. */
    fun applyDocument(key: Doc, saver: suspend (String) -> Any?, loader: suspend () -> Document) {
        val text = document(key)
        try {
            Json.parse(text)
        } catch (error: Throwable) {
            showError(error)
            return
        }
        mutate(afterSuccess = { loadDocument(key, loader) }) { saver(text) }
    }

    fun loadPlatformResult(key: Doc, operation: suspend () -> PlatformServices.Result) {
        scope.launch {
            try {
                val result = operation()
                documents[key] = result.payload.ifBlank { result.message }
                if (result.successful) clearError() else showError(IllegalStateException(result.message))
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                showError(error)
            }
        }
    }

    fun loadLicenseStatus(key: Doc?, operation: suspend () -> PlatformServices.Result) {
        scope.launch {
            try {
                val result = operation()
                if (key != null) documents[key] = result.payload.ifBlank { result.message }
                applySupporterStatus(result.payload)
                if (result.successful) clearError() else showError(IllegalStateException(result.message))
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                showError(error)
            }
        }
    }

    private fun applySupporterStatus(payload: String) {
        try {
            val decision = Json.parse(payload)["status"]["decision"]
            val tier = decision["supporterTier"].text("none")
            val active = decision["supporterActive"].bool(false)
            if (tier == "none") {
                supporterBadge = "No verified supporter entitlement"
                supporterThanks = ""
            } else {
                val title = tier.replaceFirstChar { it.uppercaseChar() } + " Supporter"
                supporterBadge = title + if (active) " · Active" else " · Perpetual fallback"
                supporterThanks = "Thank you for supporting independent ClambHook development. " +
                    if (active) "Your paid-through period is current."
                    else "Your compatible fallback and supporter badge remain yours."
            }
        } catch (_: Throwable) {
            supporterBadge = "Supporter status unavailable"
        }
    }

    fun showError(throwable: Throwable) {
        val message = throwable.message
        errorMessage = if (message.isNullOrBlank()) throwable.toString() else message
    }

    fun clearError() {
        errorMessage = null
    }

    /** Re-requests every document a page shows when the page opens. */
    fun pageRefresh(target: Page) {
        when (target) {
            Page.DASHBOARD -> if (platform.supports(Capability.LICENSING)) {
                loadLicenseStatus(null) { platform.licensing("status", "{}") }
            }
            Page.PROFILES -> loadText(Doc.CONFIG_TRANSFER) { runtime.exportConfig() }
            Page.ACTIVITY -> {
                loadDocument(Doc.DECISIONS) { runtime.decisions(com.clambhook.ui.runtime.TrafficQuery.recent(500)) }
                loadDocument(Doc.TEMPORARY_RULES) { runtime.temporaryRules() }
            }
            Page.ROUTING -> loadDocument(Doc.POLICY_GROUPS) { runtime.policyGroups() }
            Page.RULES -> {
                loadDocument(Doc.RULES) { runtime.rules() }
                loadDocument(Doc.RULE_SETS) { runtime.ruleSets() }
                loadDocument(Doc.SUBSCRIPTIONS) { runtime.ruleSubscriptions() }
            }
            Page.NETWORK -> {
                loadDocument(Doc.DNS) { runtime.dns() }
                loadDocument(Doc.SETTINGS) { runtime.settings() }
                loadDocument(Doc.CONDITIONER) { runtime.conditioner() }
            }
            Page.PROMPTS -> refreshPromptDocuments()
            Page.DEVELOPER -> refreshDeveloperDocuments()
            Page.SETTINGS -> {
                if (platform.supports(Capability.LICENSING)) {
                    loadLicenseStatus(Doc.LICENSE) { platform.licensing("status", "{}") }
                }
                if (platform.supports(Capability.UPDATES)) {
                    loadPlatformResult(Doc.UPDATE) { platform.updates("check", "{}") }
                }
            }
        }
    }

    fun refreshPromptDocuments() {
        loadDocument(Doc.PENDING_PROMPTS) { runtime.pendingPrompts() }
        loadDocument(Doc.PROMPT_DECISIONS) { runtime.promptDecisions() }
    }

    fun refreshDeveloperDocuments() {
        loadDocument(Doc.DEVELOPER_SETTINGS) { runtime.developerSettings() }
        loadDocument(Doc.DEVELOPER_MAP_RULES) { runtime.developerMapRules() }
        loadDocument(Doc.DEVELOPER_BREAKPOINT_RULES) { runtime.developerBreakpointRules() }
        loadDocument(Doc.DEVELOPER_REWRITE_RULES) { runtime.developerRewriteRules() }
        loadDocument(Doc.DEVELOPER_PENDING_BREAKPOINTS) { runtime.developerPendingBreakpoints() }
    }

    fun createConnectionRule(connectionId: String?, name: String, action: String, persistent: Boolean) {
        if (connectionId.isNullOrBlank()) {
            showError(IllegalArgumentException("Select a connection first"))
            return
        }
        val request = Json.obj(
            "conn_id" to connectionId,
            "profile" to data.activeProfile,
            "name" to name,
            "action" to action.ifBlank { "direct" },
            "scope" to "auto",
            "ttl_seconds" to 900,
        )
        mutate(afterSuccess = {
            refresh()
            pageRefresh(Page.ACTIVITY)
        }) {
            if (persistent) runtime.createRuleFromConnection(request)
            else runtime.createTemporaryRuleFromConnection(request)
        }
    }

    fun resolvePrompt(identifier: String, allow: Boolean) {
        if (identifier.isBlank()) {
            showError(IllegalArgumentException("Enter a prompt identifier"))
            return
        }
        mutate(afterSuccess = ::refreshPromptDocuments) {
            runtime.resolvePrompt(
                identifier,
                Json.obj("action" to if (allow) "allow" else "block", "scope" to "once"),
            )
        }
    }

    fun promotePrompt(identifier: String) {
        mutate(afterSuccess = ::refreshPromptDocuments) {
            runtime.promotePromptDecision(identifier, "{\"action\":\"block\",\"scope\":\"forever\"}")
        }
    }

    fun resolveDeveloperBreakpoint(identifier: String, action: String) {
        if (identifier.isBlank()) {
            showError(IllegalArgumentException("Enter a breakpoint identifier"))
            return
        }
        mutate(afterSuccess = {
            loadDocument(Doc.DEVELOPER_PENDING_BREAKPOINTS) { runtime.developerPendingBreakpoints() }
        }) {
            runtime.resolveDeveloperBreakpoint(identifier, Json.obj("action" to action))
        }
    }

    fun toggleCapture() {
        val enabled = !data.developer.enabled
        mutate {
            runtime.request("PUT", "/api/v1/developer/settings", Json.obj("enabled" to enabled))
        }
    }

    fun setActiveProfile(profile: String) {
        if (profile.isBlank() || profile == data.activeProfile) return
        mutate { runtime.setActiveProfile(profile) }
    }

    fun openBrowser(uri: String) = mutate(afterSuccess = null) { platform.openBrowser(uri) }

    enum class StatusKind { PENDING, CONNECTED, DISCONNECTED, ERROR }

    companion object {
        const val CLAMBHOOK_BUY_URL = "https://store.swiphtgroup.com/clambhook/buy/"
        const val CLAMBHOOK_PORTAL_URL = "https://store.swiphtgroup.com/clambhook/portal/"
        val PAYMENT_PROVIDER_URLS: Map<String, String> = linkedMapOf(
            "Creem" to "https://creem.io/",
            "NOWPayments" to "https://nowpayments.io/",
        )
        const val PAYMENT_PROVIDER_TRUST_SUMMARY = "Card via Creem · Cryptocurrency via NOWPayments"
        val DONATION_URLS: Map<String, String> = linkedMapOf(
            "Ko-fi" to "https://ko-fi.com/jpfchang",
            "Liberapay" to "https://en.liberapay.com/jpfchang/",
            "IssueHunt" to "https://oss.issuehunt.io/u/johnthre",
            "Donate crypto" to "https://nowpayments.io/donation?api_key=4f798f1e-c93e-456e-8067-b03b200790cd",
        )
    }
}
