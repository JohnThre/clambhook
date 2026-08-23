package com.clambhook.linux.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.rememberWindowState
import com.clambhook.linux.license.*
import com.clambhook.linux.model.*
import com.clambhook.linux.format.Formatters
import com.clambhook.linux.settings.AppSettings
import com.clambhook.linux.store.DashboardState
import com.clambhook.linux.daemon.DaemonStatus
import kotlinx.coroutines.launch

@Composable
fun MainWindow(viewModel: MainViewModel, onClose: () -> Unit = {}) {
    val storeState by viewModel.store.state.collectAsState()
    val daemonStatus by viewModel.daemon.status.collectAsState()
    val licenseState by viewModel.license.state.collectAsState()
    val settings by viewModel.settings.collectAsState()
    var currentPage by remember { mutableStateOf("now") }
    var showSettings by remember { mutableStateOf(false) }
    var showRuleDialog by remember { mutableStateOf<RuleDraft?>(null) }
    var showCleanupDialog by remember { mutableStateOf<TrafficCleanupSuggestionPayload?>(null) }

    LaunchedEffect(currentPage) { viewModel.onPageChanged(currentPage) }
    DisposableEffect(Unit) {
        onDispose { viewModel.close() }
    }

    val pages = listOf("now", "activity", "policies", "firewall", "dns", "capture", "conditioner", "library", "license")

    Window(
        title = "clambhook",
        state = rememberWindowState(width = 960.dp, height = 720.dp),
        onCloseRequest = { viewModel.close(); onClose() }
    ) {
        Column(modifier = Modifier.fillMaxSize()) {
            LicenseBanner(licenseState, currentPage, onGoToLicense = { currentPage = "license" })

            // Header / navigation
            Row(modifier = Modifier.fillMaxWidth().padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
                Text("clambhook", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(end = 16.dp))
                pages.forEach { page ->
                    TextButton(onClick = { currentPage = page }) {
                        Text(page.replaceFirstChar { it.uppercase() }, color = if (currentPage == page) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurface)
                    }
                }
                Spacer(Modifier.weight(1f))
                val daemonLabel = if (daemonStatus.let { it.state.let { it.name } } == "RUNNING") "Stop daemon" else "Start daemon"
                TextButton(onClick = { viewModel.toggleDaemon() }) { Text(daemonLabel) }
                IconButton(onClick = { viewModel.refreshNow() }) { Icon(Icons.Default.Refresh, "Refresh") }
                IconButton(onClick = { showSettings = true }) { Icon(Icons.Default.Settings, "Settings") }
            }
            Divider()

            // Page content
            Box(modifier = Modifier.fillMaxSize().padding(16.dp)) {
                when (currentPage) {
                    "now" -> NowPage(storeState, daemonStatus, licenseState, viewModel, onShowRuleDialog = { showRuleDialog = it })
                    "activity" -> ActivityPage(storeState, viewModel, onShowRuleDialog = { showRuleDialog = it }, onShowCleanupDialog = { showCleanupDialog = it })
                    "policies" -> PoliciesPage(viewModel)
                    "firewall" -> FirewallPage(viewModel)
                    "dns" -> DnsPage(viewModel)
                    "capture" -> CapturePage(viewModel)
                    "conditioner" -> ConditionerPage(viewModel)
                    "library" -> LibraryPage(storeState)
                    "license" -> LicensePage(viewModel, licenseState)
                }
            }
        }

        if (showSettings) {
            SettingsDialog(viewModel, settings, viewModel.apiToken, onDismiss = { showSettings = false })
        }
        showRuleDialog?.let { draft ->
            RuleDialog(draft, storeState.servers.chains, onSave = { rule, fromConnection ->
                if (fromConnection != null) viewModel.createRuleFromConnection(fromConnection, rule)
                else viewModel.createRule(rule)
                showRuleDialog = null
            }, onDismiss = { showRuleDialog = null })
        }
        showCleanupDialog?.let { suggestion ->
            CleanupDialog(suggestion, onApply = { viewModel.cleanupRule(suggestion); showCleanupDialog = null }, onDismiss = { showCleanupDialog = null })
        }
    }
}

@Composable
private fun LicenseBanner(licenseState: LicenseManagerState, currentPage: String, onGoToLicense: () -> Unit) {
    if (!licenseState.initialized) return
    if (licenseState.status.decision.canUseApp() && licenseState.status.decision.reason != "trial" && licenseState.status.decision.reason != "offlineGrace") return
    Card(modifier = Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 4.dp), colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.primaryContainer)) {
        Row(modifier = Modifier.padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(
                if (!licenseState.status.decision.canUseApp()) "ClambHook trial ended. Activate a license key to continue."
                else "${licenseState.status.decision.trialDaysRemaining} days left in your ClambHook trial.",
                modifier = Modifier.weight(1f), color = MaterialTheme.colorScheme.onPrimaryContainer
            )
            TextButton(onClick = onGoToLicense) { Text("License") }
        }
    }
}

@Composable
private fun NowPage(state: DashboardState, daemonStatus: DaemonStatus, licenseState: LicenseManagerState, vm: MainViewModel, onShowRuleDialog: (RuleDraft) -> Unit) {
    Column(modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
        Card(modifier = Modifier.fillMaxWidth()) {
            Column(modifier = Modifier.padding(16.dp)) {
                InfoRow("Status", if (state.status.running) "Running" else "Stopped")
                InfoRow("Daemon", daemonStatus.stateLabel())
                InfoRow("Profile", if (state.profiles.active.isNotEmpty()) state.profiles.active else "No profile")
                InfoRow("API", if (state.apiOnline) "API online" else "API offline")
                InfoRow("Connections", "${state.traffic.summary.activeConnections} active connections")
                val bandwidth = state.let { val s = it.bandwidthSamples.lastOrNull(); s ?: BandwidthSample() }
                InfoRow("Bandwidth", "${Formatters.formatRate(bandwidth.rxBps)} down / ${Formatters.formatRate(bandwidth.txBps)} up")
                InfoRow("Traffic", "${state.traffic.summary.activeConnections} active · ${Formatters.formatRate(state.traffic.summary.rxBps)} down / ${Formatters.formatRate(state.traffic.summary.txBps)} up · ${Formatters.formatBytes(state.traffic.summary.rxTotal)} down total / ${Formatters.formatBytes(state.traffic.summary.txTotal)} up total")
                if (state.errorText.isNotEmpty()) Text(state.errorText, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(top = 8.dp))
                if (daemonStatus.message.isNotEmpty()) Text(daemonStatus.message, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 8.dp))
                Row(modifier = Modifier.padding(top = 12.dp)) {
                    if (!state.status.running) Button(onClick = { vm.connect() }, enabled = state.apiOnline && licenseState.status.decision.canUseApp()) { Text("Connect") }
                    else Button(onClick = { vm.disconnect() }) { Text("Disconnect") }
                }
            }
        }
    }
}

@Composable
private fun ActivityPage(state: DashboardState, vm: MainViewModel, onShowRuleDialog: (RuleDraft) -> Unit, onShowCleanupDialog: (TrafficCleanupSuggestionPayload) -> Unit) {
    var query by remember { mutableStateOf("") }
    Column(modifier = Modifier.fillMaxSize()) {
        Row(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            Text("Traffic Monitor", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(end = 16.dp))
            state.traffic.quickFilters.forEach { chip ->
                FilterChip(
                    selected = isChipSelected(state.monitorFilter, chip.key),
                    onClick = { vm.loadTraffic(state.monitorFilter.applyingQuickFilter(chip.key)) },
                    label = { Text("${chip.label} ${chip.count}") },
                    modifier = Modifier.padding(start = 4.dp)
                )
            }
            Spacer(Modifier.width(8.dp))
            OutlinedTextField(value = query, onValueChange = { query = it }, placeholder = { Text("Search hosts, rules, chains") }, singleLine = true, modifier = Modifier.weight(1f))
        }
        LazyColumn(modifier = Modifier.weight(1f)) {
            items(state.traffic.cleanupSuggestions.take(4)) { suggestion -> CleanupSuggestionRow(suggestion, onShowCleanupDialog) }
            items(state.traffic.ruleSuggestions.take(4)) { suggestion -> RuleSuggestionRow(suggestion, onShowRuleDialog) }
            items(state.traffic.connections.filter { trafficMatches(it, "all", query) }) { conn ->
                TrafficRow(conn, onShowRuleDialog)
            }
        }
        Divider(modifier = Modifier.padding(vertical = 8.dp))
        Text("Recent logs", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(bottom = 8.dp))
        LazyColumn(modifier = Modifier.weight(1f)) {
            items(state.logs.takeLast(12)) { line -> LogRow(line) }
        }
    }
}

@Composable
private fun TrafficRow(connection: TrafficConnectionPayload, onShowRuleDialog: (RuleDraft) -> Unit) {
    val family = actionFamily(connection)
    Row(modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(modifier = Modifier.weight(1f)) {
            SelectionContainer { Text("${family.uppercase()}  ${emptyDash(connection.target)}", fontWeight = FontWeight.Medium) }
            Text("${trafficLabelFor(connection)} / ${emptyDash(connection.ruleName)} / ${Formatters.formatBytes(connection.rxTotal)} down / ${Formatters.formatBytes(connection.txTotal)} up / ${Formatters.formatDurationNs(connection.durationNs)}", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        OutlinedButton(onClick = { ruleDraftFromConnection(connection)?.let { onShowRuleDialog(RuleDraft(it, connection)) } }, enabled = ruleDraftFromConnection(connection) != null) { Text("Rule") }
    }
    Divider()
}

@Composable
private fun CleanupSuggestionRow(suggestion: TrafficCleanupSuggestionPayload, onShow: (TrafficCleanupSuggestionPayload) -> Unit) {
    Row(modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(modifier = Modifier.weight(1f)) {
            Text("${cleanupActionTitle(suggestion)}  ${cleanupTargetName(suggestion)}", fontWeight = FontWeight.Medium)
            Text(suggestion.message, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        OutlinedButton(onClick = { onShow(suggestion) }, enabled = suggestion.operation.isNotEmpty()) { Text(cleanupActionTitle(suggestion)) }
    }
    Divider()
}

@Composable
private fun RuleSuggestionRow(suggestion: TrafficRuleSuggestionPayload, onShow: (RuleDraft) -> Unit) {
    Row(modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(modifier = Modifier.weight(1f)) {
            Text("${suggestion.draftRule.action.uppercase()}  ${emptyDash(suggestion.draftRule.name)}", fontWeight = FontWeight.Medium)
            Text("${ruleMatchText(suggestion.draftRule)} / ${suggestion.count} hits / ${suggestion.reason}", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        OutlinedButton(onClick = { if (suggestion.draftRule.name.trim().isNotEmpty()) onShow(RuleDraft(suggestion.draftRule, null)) }, enabled = suggestion.draftRule.name.trim().isNotEmpty()) { Text("Create") }
    }
    Divider()
}

@Composable
private fun LogRow(line: String) {
    SelectionContainer { Text(line, fontSize = 12.sp, modifier = Modifier.padding(vertical = 2.dp)) }
}

@Composable
private fun LibraryPage(state: DashboardState) {
    Column(modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
        Row(modifier = Modifier.fillMaxWidth()) {
            Column(modifier = Modifier.weight(1f)) {
                Text("Listeners", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(bottom = 8.dp))
                if (state.status.listeners.isEmpty()) Text("No listeners active")
                else state.status.listeners.forEach { listener ->
                    val icon = Icons.Default.Cable
                    Column {
                        Text(listener.protocol.uppercase())
                        Text("${listener.addr} / ${listener.activeConns} active", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    Divider()
                }
            }
            Spacer(Modifier.width(16.dp))
            Column(modifier = Modifier.weight(1f)) {
                Text("Servers", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(bottom = 8.dp))
                if (state.servers.chains.isEmpty()) Text("No servers in this profile")
                else state.servers.chains.flatMap { chain -> chain.servers.map { server -> Pair(chain.name, server) } }.forEach { (chainName, server) ->
                    Column {
                        Text(server.name)
                        Text("$chainName / ${server.protocol} / ${Formatters.serverLocation(server)}", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    Divider()
                }
            }
        }
    }
}

@Composable
private fun PoliciesPage(vm: MainViewModel) {
    var payload by remember { mutableStateOf<PolicyGroupsPayload?>(null) }
    var status by remember { mutableStateOf("Manual select and url-test policy groups from the active profile.") }
    val scope = rememberCoroutineScope()
    LaunchedEffect(Unit) { try { payload = vm.loadPolicyGroups() } catch (e: Exception) { status = "Policy groups unavailable: ${e.message}" } }
    Column(modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("Policy groups", style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
            OutlinedButton(onClick = {
                status = "Running latency test..."
                scope.launch { try { payload = vm.testPolicyGroups(""); status = "Latency test complete." } catch (e: Exception) { status = "Latency test failed: ${e.message}" } }
            }) { Text("Latency test") }
        }
        Text(status, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.height(12.dp))
        payload?.let { p ->
            if (p.groups.isEmpty()) Text("No policy groups")
            else p.groups.filter { !it.hidden }.forEach { group -> PolicyGroupRow(group, vm) { status = it } }
        }
    }
}

@Composable
private fun PolicyGroupRow(group: PolicyGroupPayload, vm: MainViewModel, onStatus: (String) -> Unit) {
    val scope = rememberCoroutineScope()
    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
        Column(modifier = Modifier.padding(12.dp)) {
            Text("${group.name} · ${group.type}", fontWeight = FontWeight.Medium)
            if (group.isSelect()) {
                var selected by remember { mutableStateOf(group.activeChain()) }
                var expanded by remember { mutableStateOf(false) }
                Box {
                    OutlinedButton(onClick = { expanded = true }) { Text(selected) }
                    DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
                        group.chains.forEach { chain ->
                            DropdownMenuItem(onClick = {
                                selected = chain; expanded = false
                                if (chain != group.activeChain()) {
                                    onStatus("Selecting ${group.name} → $chain...")
                                    scope.launch { try { vm.selectPolicyGroup(group.name, chain); onStatus("${group.name} now uses $chain.") } catch (e: Exception) { onStatus("Selection failed: ${e.message}") } }
                                }
                            }, text = { Text(chain) })
                        }
                    }
                }
            } else {
                Text("Auto: ${if (group.activeChain().isEmpty()) "none" else group.activeChain()}", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            group.chains.forEach { chain ->
                val probe = group.resultFor(chain)
                val latency = when {
                    probe == null -> "not tested"
                    !probe.healthy -> if (probe.error.isEmpty()) "unreachable" else "unreachable (${probe.error})"
                    else -> "${probe.latencyNs / 1_000_000} ms"
                }
                Text("$chain — $latency", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}

@Composable
private fun FirewallPage(vm: MainViewModel) {
    var payload by remember { mutableStateOf<PromptsPayload?>(null) }
    var silentPayload by remember { mutableStateOf<SilentDecisionsPayload?>(null) }
    var status by remember { mutableStateOf("Allow or block connections that no rule already decides.") }
    var matchHost by remember { mutableStateOf(false) }
    var matchPort by remember { mutableStateOf(false) }
    var matchProtocol by remember { mutableStateOf(false) }
    LaunchedEffect(Unit) {
        try { payload = vm.loadPendingPrompts() } catch (e: Exception) { status = "Prompts unavailable: ${e.message}" }
        try { silentPayload = vm.loadSilentDecisions() } catch (e: Exception) {}
    }
    Column(modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
        Text("Connection prompts", style = MaterialTheme.typography.titleMedium)
        Text(status, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Row(verticalAlignment = Alignment.CenterVertically) {
            Checkbox(checked = matchHost, onCheckedChange = { matchHost = it })
            Text("This host")
            Spacer(Modifier.width(8.dp))
            Checkbox(checked = matchPort, onCheckedChange = { matchPort = it })
            Text("This port")
            Spacer(Modifier.width(8.dp))
            Checkbox(checked = matchProtocol, onCheckedChange = { matchProtocol = it })
            Text("This protocol")
        }
        Spacer(Modifier.height(8.dp))
        payload?.let { p ->
            if (p.prompts.isEmpty()) Text("No pending prompts")
            else p.prompts.forEach { prompt ->
                Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                    Column(modifier = Modifier.padding(12.dp)) {
                        Text("${if (prompt.processName.isEmpty()) "Unknown process" else prompt.processName} → ${prompt.target}", fontWeight = FontWeight.Medium)
                        val detail = buildString {
                            append(if (prompt.network.isEmpty()) "tcp" else prompt.network)
                            append(" · ").append(prompt.processPath)
                            if (prompt.wouldUseChain.isNotEmpty()) append(" · via ").append(prompt.wouldUseChain)
                            if (prompt.codeSignID.isNotEmpty()) append(" · signed:").append(prompt.codeSignID)
                            if (prompt.expiresAt.isNotEmpty()) append(" · expires ").append(prompt.expiresAt)
                        }
                        Text(detail, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        Row(modifier = Modifier.padding(top = 8.dp)) {
                            OutlinedButton(onClick = { vm.resolvePrompt(prompt.id, "allow", "once", matchHost, matchPort, matchProtocol); status = "Allowed once." }) { Text("Allow once") }
                            Spacer(Modifier.width(4.dp))
                            OutlinedButton(onClick = { vm.resolvePrompt(prompt.id, "allow", "session", matchHost, matchPort, matchProtocol); status = "Allowed session." }) { Text("Allow session") }
                            Spacer(Modifier.width(4.dp))
                            OutlinedButton(onClick = { vm.resolvePrompt(prompt.id, "allow", "until_quit", matchHost, matchPort, matchProtocol); status = "Allowed until quit." }) { Text("Allow until quit") }
                            Spacer(Modifier.width(4.dp))
                            OutlinedButton(onClick = { vm.resolvePrompt(prompt.id, "allow", "forever", matchHost, matchPort, matchProtocol); status = "Allowed forever." }) { Text("Allow forever") }
                            Spacer(Modifier.width(4.dp))
                            Button(onClick = { vm.resolvePrompt(prompt.id, "block", "forever", matchHost, matchPort, matchProtocol); status = "Blocked forever." }, colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error)) { Text("Block forever") }
                        }
                    }
                }
            }
        }
        Spacer(Modifier.height(12.dp))
        Text("Silent Mode Decisions", style = MaterialTheme.typography.titleMedium)
        silentPayload?.let { sp ->
            if (sp.decisions.isEmpty()) {
                Text("No auto-decisions logged", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
            } else {
                sp.decisions.forEach { decision ->
                    Card(modifier = Modifier.fillMaxWidth().padding(bottom = 8.dp)) {
                        Column(modifier = Modifier.padding(12.dp)) {
                            Text("${decision.action.uppercase()} · ${if (decision.processName.isEmpty()) "Unknown process" else decision.processName} → ${decision.target}", fontWeight = FontWeight.Medium)
                            Row(modifier = Modifier.padding(top = 8.dp)) {
                                OutlinedButton(onClick = { vm.promoteSilentDecision(decision.id, "session"); status = "Promoted (session)." }) { Text("Promote session") }
                                Spacer(Modifier.width(4.dp))
                                OutlinedButton(onClick = { vm.promoteSilentDecision(decision.id, "forever"); status = "Promoted (forever)." }) { Text("Promote forever") }
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun DnsPage(vm: MainViewModel) {
    var payload by remember { mutableStateOf<DnsPayload?>(null) }
    var status by remember { mutableStateOf("DNS strategy for the active profile.") }
    LaunchedEffect(Unit) { try { payload = vm.loadDns() } catch (e: Exception) { status = "DNS status unavailable: ${e.message}" } }
    Column(modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
        Text("Encrypted DNS", style = MaterialTheme.typography.titleMedium)
        Text(status, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
        Spacer(Modifier.height(12.dp))
        payload?.let { dns ->
            if (dns.upstreams.isEmpty()) Text("No encrypted upstreams")
            else dns.upstreams.forEach { upstream ->
                val name = if (upstream.name.isEmpty()) upstream.protocol.uppercase() else upstream.name
                Column {
                    Text("$name · ${upstream.protocol.uppercase()}")
                    Text(upstream.endpoint(), fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                Divider()
            }
            dns.upstreamRoutes.forEach { route ->
                if (route.error.isNotEmpty()) {
                    Column {
                        Text("Route: ${route.target}")
                        Text("error: ${route.error}", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                } else {
                    val via = if (route.chainName.isEmpty()) route.action else "${route.action} via ${route.chainName}"
                    Column {
                        Text("Route: ${route.target}")
                        Text(via, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
                Divider()
            }
        }
    }
}

@Composable
private fun CapturePage(vm: MainViewModel) {
    val scope = rememberCoroutineScope()
    var status by remember { mutableStateOf<DeveloperStatusPayload?>(null) }
    var entries by remember { mutableStateOf<List<DeveloperEntryPayload>>(emptyList()) }
    var selectedEntry by remember { mutableStateOf<DeveloperEntryPayload?>(null) }
    var message by remember { mutableStateOf("Opt-in local capture of traffic routed through the daemon HTTP proxy.") }
    var query by remember { mutableStateOf("") }
    var methodFilter by remember { mutableStateOf("") }
    var errorOnly by remember { mutableStateOf(false) }
    var showImportCurl by remember { mutableStateOf(false) }
    var composeSeed by remember { mutableStateOf<DeveloperEntryPayload?>(null) }
    fun reload() {
        scope.launch {
            try { entries = vm.loadCaptureEntries(DeveloperEntriesFilter(query = query, method = methodFilter, errorOnly = errorOnly)) }
            catch (e: Exception) { message = "Could not load captures: ${e.message}" }
        }
    }
    LaunchedEffect(Unit) { try { status = vm.loadCaptureStatus() } catch (e: Exception) { message = "Capture status unavailable: ${e.message}" } }
    LaunchedEffect(status?.enabled) { if (status?.enabled == true) reload() else entries = emptyList() }
    Column(modifier = Modifier.fillMaxSize()) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("HTTP(S) capture", style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
            Button(onClick = {
                scope.launch {
                    try { status = vm.setCaptureEnabled(!(status?.enabled ?: false)); if (status?.enabled == true) reload() }
                    catch (e: Exception) { message = "Could not update capture: ${e.message}" }
                }
            }) { Text(if (status?.enabled == true) "Disable capture" else "Enable capture") }
        }
        Text(message, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
        if (status?.enabled == true) {
            Spacer(Modifier.height(8.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                OutlinedTextField(query, { query = it }, label = { Text("Search") }, singleLine = true, modifier = Modifier.weight(1f))
                Spacer(Modifier.width(8.dp))
                OutlinedTextField(methodFilter, { methodFilter = it }, label = { Text("Method") }, singleLine = true, modifier = Modifier.width(90.dp))
                Spacer(Modifier.width(8.dp))
                Row(verticalAlignment = Alignment.CenterVertically) { Checkbox(errorOnly, { errorOnly = it; reload() }); Text("Errors") }
                Spacer(Modifier.width(8.dp))
                Button(onClick = { reload() }) { Text("Filter") }
                Spacer(Modifier.width(8.dp))
                Button(onClick = { showImportCurl = true }) { Text("Import cURL") }
            }
        }
        Spacer(Modifier.height(12.dp))
        LazyColumn(modifier = Modifier.weight(1f)) {
            if (status?.enabled != true) item { Text("Capture disabled. Turn on capture to record request/response metadata.") }
            else if (entries.isEmpty()) item { Text("No transactions match the filter.") }
            else items(entries) { entry ->
                val method = if (entry.method.isEmpty()) "GET" else entry.method
                val code = if (entry.statusCode == 0) "pending" else entry.statusCode.toString()
                Row(modifier = Modifier.fillMaxWidth().clickable { selectedEntry = entry }.padding(vertical = 4.dp)) {
                    Column(modifier = Modifier.weight(1f)) {
                        Text("$method ${if (entry.url.isEmpty()) entry.host else entry.url}")
                        Text("status $code · ${entry.responseBytes} B · click for details", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    Icon(Icons.Default.ChevronRight, "")
                }
                Divider()
            }
        }
    }
    selectedEntry?.let { entry -> CaptureDetailDialog(entry, vm, onDismiss = { selectedEntry = null }) }
    composeSeed?.let { seed -> ComposeRequestDialog(seed, vm, onDismiss = { composeSeed = null }) }
    if (showImportCurl) {
        ImportCurlDialog(vm, onDismiss = { showImportCurl = false }, onParsed = { parsed ->
            val bodyBytes = parsed.body.encodeToByteArray().size.toLong()
            composeSeed = DeveloperEntryPayload(
                method = parsed.method,
                url = parsed.url,
                host = parsed.url.substringAfter("://").substringBefore("/"),
                request = CapturedMessagePayload(
                    headers = parsed.headers,
                    body = CapturedBodyPayload(size = bodyBytes, preview = parsed.body, previewBytes = bodyBytes, encoding = if (parsed.body.isEmpty()) "" else "utf8")
                )
            )
            showImportCurl = false
        })
    }
}

@Composable
private fun CaptureDetailDialog(entry: DeveloperEntryPayload, vm: MainViewModel, onDismiss: () -> Unit) {
    val scope = rememberCoroutineScope()
    val decoded = entry.decoded
    val hasDecoded = decoded != null && decoded.frames.isNotEmpty()
    val method = if (entry.method.isEmpty()) "GET" else entry.method
    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = {
            Row {
                TextButton(onClick = {
                    scope.launch {
                        try {
                            val curl = vm.copyEntryCurl(entry.id)
                            java.awt.Toolkit.getDefaultToolkit().systemClipboard.setContents(java.awt.datatransfer.StringSelection(curl), null)
                        } catch (_: Exception) {}
                    }
                }) { Text("Copy cURL") }
                TextButton(onClick = { scope.launch { try { vm.repeatCaptureEntry(entry.id) } catch (_: Exception) {} } }) { Text("Repeat") }
                TextButton(onClick = onDismiss) { Text("Close") }
            }
        },
        title = { Text("$method ${if (entry.host.isEmpty()) entry.url else entry.host}") },
        text = {
            Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                val code = if (entry.statusCode == 0) "pending" else entry.statusCode.toString()
                Text("Status $code · ${entry.responseBytes} B", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                entry.timings?.let { t ->
                    Text("connect ${t.connect.toInt()}ms · ssl ${t.ssl.toInt()}ms · send ${t.send.toInt()}ms · wait ${t.wait.toInt()}ms · receive ${t.receive.toInt()}ms", fontSize = 11.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                if (entry.error.isNotEmpty()) Text(entry.error, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(top = 4.dp))
                Spacer(Modifier.height(8.dp))
                Text("Request body", fontWeight = FontWeight.Medium)
                val reqBody = entry.request.bodyText()
                Text(if (reqBody.isEmpty()) "No body preview" else reqBody, fontFamily = FontFamily.Monospace, fontSize = 12.sp)
                Spacer(Modifier.height(8.dp))
                Text("Response pretty", fontWeight = FontWeight.Medium)
                val pretty = entry.response.body.viewer?.pretty
                Text(if (pretty.isNullOrEmpty()) entry.response.bodyText().ifEmpty { "No body preview" } else pretty, fontFamily = FontFamily.Monospace, fontSize = 12.sp)
                Spacer(Modifier.height(8.dp))
                Text("Response hex", fontWeight = FontWeight.Medium)
                val hex = entry.response.body.viewer?.hex
                Text(if (hex.isNullOrEmpty()) "No hex preview" else hex, fontFamily = FontFamily.Monospace, fontSize = 12.sp)
                if (hasDecoded) {
                    Spacer(Modifier.height(8.dp))
                    Text("Decoded ${decoded!!.kind}", fontWeight = FontWeight.Medium, color = MaterialTheme.colorScheme.primary)
                    decoded.frames.forEach { frame ->
                        val label = listOf(frame.direction, frame.opcode).filter { it.isNotEmpty() }.joinToString(" · ") + if (frame.truncated) " · truncated" else ""
                        Spacer(Modifier.height(6.dp))
                        Text(label, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        Text(frame.preview, fontFamily = FontFamily.Monospace, fontSize = 12.sp)
                    }
                }
            }
        }
    )
}

@Composable
private fun ComposeRequestDialog(seed: DeveloperEntryPayload, vm: MainViewModel, onDismiss: () -> Unit) {
    val scope = rememberCoroutineScope()
    var method by remember { mutableStateOf(seed.method.ifEmpty { "GET" }) }
    var url by remember { mutableStateOf(seed.url) }
    var body by remember { mutableStateOf(seed.request.bodyText()) }
    var msg by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = {
            Row {
                TextButton(onClick = onDismiss) { Text("Cancel") }
                Button(onClick = {
                    scope.launch {
                        try { vm.sendComposed(ComposedRequestPayload(method = method, url = url, body = body.ifBlank { null })); onDismiss() }
                        catch (e: Exception) { msg = e.message ?: "send failed" }
                    }
                }, enabled = url.isNotBlank()) { Text("Send") }
            }
        },
        title = { Text("Compose & send") },
        text = {
            Column {
                OutlinedTextField(method, { method = it }, label = { Text("Method") }, singleLine = true)
                Spacer(Modifier.height(8.dp))
                OutlinedTextField(url, { url = it }, label = { Text("URL") }, singleLine = true)
                Spacer(Modifier.height(8.dp))
                OutlinedTextField(body, { body = it }, label = { Text("Body") }, minLines = 3, maxLines = 6)
                if (msg.isNotEmpty()) Text(msg, fontSize = 12.sp, color = MaterialTheme.colorScheme.error)
            }
        }
    )
}

@Composable
private fun ImportCurlDialog(vm: MainViewModel, onDismiss: () -> Unit, onParsed: (ParsedCurlResponse) -> Unit) {
    val scope = rememberCoroutineScope()
    var text by remember { mutableStateOf("") }
    var err by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        confirmButton = {
            Row {
                TextButton(onClick = onDismiss) { Text("Cancel") }
                Button(onClick = {
                    scope.launch {
                        try { onParsed(vm.importCurl(text)) }
                        catch (e: Exception) { err = e.message ?: "parse failed" }
                    }
                }, enabled = text.isNotBlank()) { Text("Parse & Compose") }
            }
        },
        title = { Text("Import cURL") },
        text = {
            Column {
                Text("Paste a cURL command to parse method, URL, headers, and body.", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(Modifier.height(8.dp))
                OutlinedTextField(text, { text = it }, label = { Text("cURL") }, minLines = 3, maxLines = 8)
                if (err.isNotEmpty()) Text(err, fontSize = 12.sp, color = MaterialTheme.colorScheme.error)
            }
        }
    )
}

@Composable
private fun ConditionerPage(vm: MainViewModel) {
    val state by vm.conditioner.collectAsState()
    LaunchedEffect(Unit) { if (state.payload == null) vm.loadConditioner() }

    val payload = state.payload
    var enabled by remember(payload) { mutableStateOf(payload?.enabled ?: false) }
    var download by remember(payload) { mutableStateOf(payload?.downloadKbps?.takeIf { it > 0 }?.toString() ?: "") }
    var upload by remember(payload) { mutableStateOf(payload?.uploadKbps?.takeIf { it > 0 }?.toString() ?: "") }
    var latency by remember(payload) { mutableStateOf(payload?.latency ?: "") }
    var jitter by remember(payload) { mutableStateOf(payload?.jitter ?: "") }
    var loss by remember(payload) { mutableStateOf(payload?.lossPercent?.takeIf { it > 0.0 }?.toString() ?: "") }

    Column(modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text("Network Conditioner", style = MaterialTheme.typography.titleMedium, modifier = Modifier.weight(1f))
            IconButton(onClick = { vm.loadConditioner() }) { Icon(Icons.Default.Refresh, "Reload") }
        }
        Text(
            "Shape bandwidth, latency, jitter and loss for the active profile.",
            fontSize = 12.sp,
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )
        Spacer(Modifier.height(12.dp))
        when {
            state.loading && payload == null -> {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(modifier = Modifier.height(20.dp).width(20.dp), strokeWidth = 2.dp)
                    Spacer(Modifier.width(8.dp))
                    Text("Loading conditioner…")
                }
            }
            state.error.isNotEmpty() && payload == null -> {
                Text(state.error, color = MaterialTheme.colorScheme.error)
                Button(onClick = { vm.loadConditioner() }, modifier = Modifier.padding(top = 8.dp)) { Text("Retry") }
            }
            payload == null -> {
                Text("No conditioner data yet.", color = MaterialTheme.colorScheme.onSurfaceVariant)
                Button(onClick = { vm.loadConditioner() }, modifier = Modifier.padding(top = 8.dp)) { Text("Load") }
            }
            else -> {
                if (payload.profile.isNotEmpty()) {
                    Text("Profile: ${payload.profile}", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(vertical = 4.dp)) {
                    Text("Enabled", modifier = Modifier.weight(1f))
                    Switch(checked = enabled, onCheckedChange = { enabled = it })
                }
                OutlinedTextField(value = download, onValueChange = { download = it.filter(Char::isDigit) }, label = { Text("Download (kbps)") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp))
                OutlinedTextField(value = upload, onValueChange = { upload = it.filter(Char::isDigit) }, label = { Text("Upload (kbps)") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp))
                OutlinedTextField(value = latency, onValueChange = { latency = it }, label = { Text("Latency (e.g. 40ms)") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp))
                OutlinedTextField(value = jitter, onValueChange = { jitter = it }, label = { Text("Jitter (e.g. 5ms)") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp))
                OutlinedTextField(value = loss, onValueChange = { v -> loss = v.filter { it.isDigit() || it == '.' } }, label = { Text("Loss (%)") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp))
                if (state.error.isNotEmpty()) {
                    Text(state.error, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(top = 4.dp))
                }
                Button(
                    onClick = {
                        vm.updateConditioner(
                            ConditionerUpdateRequest(
                                profile = payload.profile.ifEmpty { null },
                                enabled = enabled,
                                downloadKbps = download.toIntOrNull(),
                                uploadKbps = upload.toIntOrNull(),
                                latency = latency.ifEmpty { null },
                                jitter = jitter.ifEmpty { null },
                                lossPercent = loss.toDoubleOrNull()
                            )
                        )
                    },
                    enabled = !state.loading,
                    modifier = Modifier.padding(top = 8.dp)
                ) { Text("Save conditioner") }
            }
        }
    }
}

@Composable
private fun LicensePage(vm: MainViewModel, licenseState: LicenseManagerState) {
    var key by remember { mutableStateOf("") }
    var email by remember(licenseState.email) { mutableStateOf(licenseState.email) }
    Column(modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
        Text("License", style = MaterialTheme.typography.titleMedium)
        Card(modifier = Modifier.fillMaxWidth().padding(vertical = 8.dp)) {
            Column(modifier = Modifier.padding(12.dp)) {
                Text(licenseState.status.decision.title(), fontWeight = FontWeight.Medium)
                Text(licenseState.status.decision.detail(), fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
                licenseState.status.expiredTrial?.let { recovery ->
                    if (recovery.title.isNotEmpty()) {
                        Text(if (recovery.detail.isEmpty()) recovery.title else "${recovery.title} — ${recovery.detail}", color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(top = 4.dp))
                    }
                }
                licenseState.status.licenseExpiredForUpdates?.let { recovery ->
                    if (recovery.title.isNotEmpty()) {
                        Text(if (recovery.detail.isEmpty()) recovery.title else "${recovery.title} — ${recovery.detail}", color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(top = 4.dp))
                    }
                }
                Row(modifier = Modifier.padding(top = 8.dp)) {
                    Button(onClick = { vm.openUrl(LICENSE_BUY_URL) }) { Text("Buy license") }
                    Spacer(Modifier.width(4.dp))
                    OutlinedButton(onClick = { vm.openUrl(LICENSE_PORTAL_URL) }) { Text("Device portal") }
                }
            }
        }
        Text("Activate this device", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(top = 16.dp))
        OutlinedTextField(value = key, onValueChange = { key = it }, label = { Text("License key") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp))
        OutlinedTextField(value = email, onValueChange = { email = it }, label = { Text("Email") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp))
        Button(onClick = { if (key.trim().isNotEmpty()) vm.activateLicense(key.trim(), email.trim()) }, enabled = !licenseState.loading, modifier = Modifier.padding(top = 8.dp)) { Text("Activate license") }
        if (licenseState.message.isNotEmpty()) Text(licenseState.message, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 8.dp))
        Text("ClambHook starts with a one-month trial. Buy a license with Creem or NOWPayments, then enter your key here. PayPal is not accepted.", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 8.dp))

        Text("Devices", style = MaterialTheme.typography.titleMedium, modifier = Modifier.padding(top = 16.dp))
        Text("${licenseState.deviceState.activeCount()} of ${licenseState.deviceState.maxActiveDevices} device seats active", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
        if (licenseState.deviceState.devices.isEmpty()) Text("No devices activated", fontSize = 12.sp)
        else licenseState.deviceState.devices.forEach { device ->
            val name = if (device.displayName.isEmpty()) "Device" else device.displayName
            val devStatus = if (device.active()) "active" else "deactivated"
            Column {
                Text(name)
                Text("${device.platform} · ${device.architecture} · $devStatus", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            Divider()
        }
        Button(onClick = { vm.deactivateDevice() }, enabled = licenseState.hasLicenseKey && !licenseState.loading, modifier = Modifier.padding(top = 8.dp)) { Text("Deactivate this device") }
    }
}

@Composable
private fun LegalFooter() {
    SelectionContainer {
        Text(
            text = "© 2025 Pengfan Chang. All rights reserved. Confidential and proprietary. support@swiphtgroup.com",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(top = 12.dp),
        )
    }
}

@Composable
private fun SettingsDialog(vm: MainViewModel, settings: AppSettings, token: String, onDismiss: () -> Unit) {
    var apiEndpoint by remember { mutableStateOf(settings.apiEndpoint) }
    var tokenText by remember { mutableStateOf(token) }
    var daemonPath by remember { mutableStateOf(settings.daemonPath) }
    var configPath by remember { mutableStateOf(settings.configPath) }
    var refresh by remember { mutableStateOf(settings.refreshIntervalSeconds.toString()) }
    var logRetention by remember { mutableStateOf(settings.logRetention.toString()) }
    var launchOnStart by remember { mutableStateOf(settings.launchDaemonOnStart) }
    var stopOnExit by remember { mutableStateOf(settings.stopDaemonOnExit) }
    var eventsEnabled by remember { mutableStateOf(settings.eventStreamEnabled) }
    val valid = com.clambhook.linux.settings.isSupportedApiEndpoint(apiEndpoint)
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Settings") },
        text = {
            Column(modifier = Modifier.verticalScroll(rememberScrollState())) {
                OutlinedTextField(value = apiEndpoint, onValueChange = { apiEndpoint = it }, label = { Text("API endpoint") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp))
                if (!valid) Text("Use an http:// or https:// endpoint with a host.", color = MaterialTheme.colorScheme.error, fontSize = 12.sp)
                OutlinedTextField(value = tokenText, onValueChange = { tokenText = it }, label = { Text("Bearer token") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp))
                OutlinedTextField(value = daemonPath, onValueChange = { daemonPath = it }, label = { Text("Daemon path") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp))
                OutlinedTextField(value = configPath, onValueChange = { configPath = it }, label = { Text("Config path") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp))
                OutlinedTextField(value = refresh, onValueChange = { refresh = it.filter { c -> c.isDigit() } }, label = { Text("Refresh interval") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp))
                OutlinedTextField(value = logRetention, onValueChange = { logRetention = it.filter { c -> c.isDigit() } }, label = { Text("Log retention") }, singleLine = true, modifier = Modifier.fillMaxWidth().padding(vertical = 4.dp))
                Row(verticalAlignment = Alignment.CenterVertically) { Checkbox(checked = launchOnStart, onCheckedChange = { launchOnStart = it }); Text("Launch daemon on start") }
                Row(verticalAlignment = Alignment.CenterVertically) { Checkbox(checked = stopOnExit, onCheckedChange = { stopOnExit = it }); Text("Stop daemon on quit") }
                Row(verticalAlignment = Alignment.CenterVertically) { Checkbox(checked = eventsEnabled, onCheckedChange = { eventsEnabled = it }); Text("Enable event stream") }
                LegalFooter()
            }
        },
        confirmButton = {
            TextButton(onClick = {
                val newSettings = AppSettings(apiEndpoint, daemonPath, configPath, launchOnStart, stopOnExit, eventsEnabled, refresh.toIntOrNull() ?: 5, logRetention.toIntOrNull() ?: 200)
                vm.saveSettings(newSettings, tokenText)
                onDismiss()
            }, enabled = valid) { Text("Save") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } }
    )
}

data class RuleDraft(val rule: RulePayload, val fromConnection: TrafficConnectionPayload?)

@Composable
private fun RuleDialog(draft: RuleDraft, chains: List<ChainPayload>, onSave: (RulePayload, TrafficConnectionPayload?) -> Unit, onDismiss: () -> Unit) {
    var name by remember { mutableStateOf(draft.rule.name) }
    var action by remember { mutableStateOf(draft.rule.action) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Create Rule") },
        text = {
            Column {
                OutlinedTextField(value = name, onValueChange = { name = it }, label = { Text("Name") }, singleLine = true, modifier = Modifier.fillMaxWidth())
                Spacer(Modifier.height(8.dp))
                var expanded by remember { mutableStateOf(false) }
                Box {
                    OutlinedButton(onClick = { expanded = true }) { Text("Action: $action") }
                    DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
                        DropdownMenuItem(onClick = { action = "block"; expanded = false }, text = { Text("Block") })
                        DropdownMenuItem(onClick = { action = "direct"; expanded = false }, text = { Text("Direct") })
                        chains.forEach { chain -> DropdownMenuItem(onClick = { action = "chain:${chain.name}"; expanded = false }, text = { Text("Proxy: ${chain.name}") }) }
                    }
                }
                Text("Match: ${ruleMatchText(draft.rule)}", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        },
        confirmButton = { TextButton(onClick = { onSave(draft.rule.copy(name = name.trim(), action = action), draft.fromConnection) }) { Text("Save") } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } }
    )
}

@Composable
private fun CleanupDialog(suggestion: TrafficCleanupSuggestionPayload, onApply: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("${cleanupActionTitle(suggestion)} ${cleanupTargetName(suggestion)}") },
        text = { Text(suggestion.message) },
        confirmButton = { Button(onClick = onApply, colors = ButtonDefaults.buttonColors(containerColor = MaterialTheme.colorScheme.error)) { Text(cleanupActionTitle(suggestion)) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } }
    )
}

@Composable
private fun InfoRow(label: String, value: String) {
    Row(modifier = Modifier.fillMaxWidth().padding(vertical = 2.dp)) {
        Text(label, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.width(120.dp))
        Text(value)
    }
}

// Helpers — mirror the Vala main-window statics
private fun actionFamily(connection: TrafficConnectionPayload): String {
    val action = connection.ruleAction.lowercase()
    return when {
        action == "direct" -> "direct"
        action == "block" || action == "reject" -> "block"
        else -> "proxy"
    }
}

private fun trafficLabelFor(connection: TrafficConnectionPayload): String {
    if (connection.application.isNotEmpty()) return connection.application
    if (connection.network.isNotEmpty()) return connection.network
    if (connection.chainName.isNotEmpty()) return connection.chainName
    return connection.listener.protocol
}

private fun trafficMatches(connection: TrafficConnectionPayload, filter: String, query: String): Boolean {
    if (filter != "all" && actionFamily(connection) != filter) return false
    if (query.isBlank()) return true
    val q = query.lowercase()
    return connection.target.lowercase().contains(q) ||
        connection.targetHost.lowercase().contains(q) ||
        connection.ruleName.lowercase().contains(q) ||
        connection.ruleAction.lowercase().contains(q) ||
        connection.chainName.lowercase().contains(q) ||
        connection.application.lowercase().contains(q) ||
        connection.network.lowercase().contains(q)
}

private fun monitorHost(connection: TrafficConnectionPayload): String {
    var host = connection.targetHost.trim()
    if (host.isEmpty()) {
        host = connection.target
        val idx = host.lastIndexOf(":")
        if (idx > 0) host = host.substring(0, idx)
    }
    host = host.replace("[", "").replace("]", "").lowercase()
    if (host.endsWith(".")) host = host.dropLast(1)
    return host
}

private fun ruleDraftFromConnection(connection: TrafficConnectionPayload): RulePayload? {
    val host = monitorHost(connection)
    if (host.isEmpty()) return null
    val family = actionFamily(connection)
    val rule = RulePayload(name = "$family-${ruleToken(host)}")
    val action = when (family) {
        "direct" -> "direct"
        "block" -> if (connection.ruleAction.lowercase() == "reject") "reject" else "block"
        else -> if (connection.chainName.isEmpty()) "direct" else "chain:${connection.chainName}"
    }
    val ruleWithAction = rule.copy(action = action)
    return when {
        looksLikeIpv4(host) -> ruleWithAction.copy(cidrs = listOf("$host/32"))
        host.contains(":") -> ruleWithAction.copy(cidrs = listOf("$host/128"))
        else -> ruleWithAction.copy(domains = listOf(host))
    }
}

private fun ruleToken(host: String): String {
    val token = host.lowercase().replace(".", "-").replace(":", "-").replace("_", "-").replace(" ", "-")
    return if (token.isEmpty()) "connection" else token
}

private fun looksLikeIpv4(host: String): Boolean {
    val parts = host.split(".")
    if (parts.size != 4) return false
    return parts.all { it.toIntOrNull()?.let { v -> v in 0..255 } ?: false }
}

private fun ruleMatchText(rule: RulePayload): String {
    if (rule.domains.isNotEmpty()) return rule.domains.joinToString(", ")
    if (rule.domainSuffixes.isNotEmpty()) return rule.domainSuffixes.map { "*.$it" }.joinToString(", ")
    if (rule.cidrs.isNotEmpty()) return rule.cidrs.joinToString(", ")
    if (rule.domainKeywords.isNotEmpty()) return "contains ${rule.domainKeywords.joinToString(", ")}"
    return "any"
}

private fun cleanupTargetName(suggestion: TrafficCleanupSuggestionPayload): String =
    if (suggestion.targetRuleName.isEmpty()) suggestion.ruleName else suggestion.targetRuleName

private fun cleanupActionTitle(suggestion: TrafficCleanupSuggestionPayload): String =
    if (suggestion.operation == "move_rule_to_end") "Move to end" else "Delete"

private fun emptyDash(value: String): String = if (value.trim().isEmpty()) "--" else value

private fun isChipSelected(filter: TrafficMonitorFilter, key: String): Boolean {
    if (key == "all") return filter.isEmpty()
    if (key == "active") return filter.state == "active"
    if (key == "proxy" || key == "direct" || key == "block") return filter.action == key
    val colon = key.indexOf(':')
    if (colon > 0) {
        val name = key.substring(0, colon); val value = key.substring(colon + 1)
        return when (name) {
            "country" -> filter.country == value
            "port" -> filter.port == value
            "process" -> filter.process == value
            "network" -> filter.network == value
            else -> false
        }
    }
    return false
}
