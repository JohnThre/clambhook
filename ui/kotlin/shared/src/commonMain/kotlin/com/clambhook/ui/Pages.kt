// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.widthIn
import androidx.compose.material3.Checkbox
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.clambhook.ui.format.Formatters
import com.clambhook.ui.json.Json
import com.clambhook.ui.model.DashboardData
import com.clambhook.ui.platform.PlatformServices.Capability
import com.clambhook.ui.runtime.Document
import com.clambhook.ui.runtime.TrafficQuery
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch

private const val MAX_IMPORT_BYTES = 4 * 1024 * 1024

@Composable
internal fun DashboardPage(controller: AppController) {
    val data = controller.data
    ScrollPage {
        Card {
            Text(
                if (data.running) "Your traffic is protected" else "Ready to connect",
                style = MaterialTheme.typography.headlineSmall,
                fontWeight = FontWeight.Bold,
                modifier = Modifier.semantics { contentDescription = "Connection summary" },
            )
            Text(
                if (data.activeProfile.isBlank()) "Choose or import a profile to get started"
                else "Active profile · ${data.activeProfile}",
                color = ClambhookPalette.muted,
                modifier = Modifier.semantics { contentDescription = "Active profile summary" },
            )
            if (controller.hasLoaded && controller.missingProfile) {
                PrimaryAction(
                    "Add a profile",
                    onClick = { controller.showPage(Page.PROFILES) },
                    description = "Open Profiles to add a profile",
                )
            }
        }
        ActionRow {
            Metric("Active connections", data.traffic.activeConnections.toString())
            Metric("Download", Formatters.rate(data.traffic.downloadBytesPerSecond))
            Metric("Upload", Formatters.rate(data.traffic.uploadBytesPerSecond))
            Metric("Transferred", Formatters.bytes(data.traffic.receivedBytes + data.traffic.transmittedBytes))
        }
        Card {
            SectionTitle("Listeners")
            SecondaryText(
                if (data.listeners.isEmpty()) "No active listeners"
                else data.listeners.joinToString("\n") {
                    "${it.protocol.uppercase()} ${it.address} · " +
                        Formatters.count(it.activeConnections, "connection", "connections")
                },
            )
        }
        Card {
            SectionTitle("Encrypted DNS")
            SecondaryText(
                (if (data.dns.enabled) "Enabled" else "Disabled") + " · ${data.dns.strategy}\n" +
                    data.dns.upstreams.ifEmpty { listOf("No encrypted upstreams") }.joinToString("\n"),
            )
        }
        SecondaryText(
            controller.supporterBadge,
            modifier = Modifier.semantics { contentDescription = "ClambHook supporter status" },
        )
        SecondaryText(
            controller.lastUpdated,
            modifier = Modifier.semantics { contentDescription = "Dashboard refresh status" },
        )
    }
}

@Composable
private fun Metric(title: String, value: String) {
    Card(Modifier.widthIn(min = 180.dp, max = 260.dp)) {
        Text(title, color = ClambhookPalette.muted, style = MaterialTheme.typography.labelLarge)
        Text(value, style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold)
    }
}

@Composable
internal fun ProfilesPage(controller: AppController) {
    val runtime = controller.runtime
    val platform = controller.platform
    val files = platform.supports(Capability.FILES)
    val clipboard = platform.supports(Capability.CLIPBOARD)
    val scope = rememberCoroutineScope()
    var selectedProfile by remember(controller.data.activeProfile) {
        mutableStateOf(controller.data.activeProfile.ifBlank { null })
    }

    var outlineKey by rememberSaveable { mutableStateOf("") }
    var outlineName by rememberSaveable { mutableStateOf("Outline") }
    var outlineReview by rememberSaveable {
        mutableStateOf("Review an access key before importing. Keys are never auto-connected.")
    }
    var reviewed by remember { mutableStateOf<Pair<String, String>?>(null) }
    LaunchedEffect(controller.outlineKeyRequest) {
        controller.outlineKeyRequest?.let {
            outlineKey = it
            reviewed = null
            controller.outlineKeyRequestHandled()
        }
    }

    var converterFormat by rememberSaveable { mutableStateOf("auto") }
    var converterName by rememberSaveable { mutableStateOf("Imported Profile") }
    var converterActivate by rememberSaveable { mutableStateOf(false) }
    var converterReview by remember { mutableStateOf<Document?>(null) }
    var converterDetails by rememberSaveable {
        mutableStateOf("Conversion is offline. Review every omitted or changed item before merging.")
    }
    var converterFile by rememberSaveable { mutableStateOf("") }
    var configFile by rememberSaveable { mutableStateOf("") }

    fun launchInto(onValue: (String) -> Unit, operation: suspend () -> String) {
        scope.launch {
            try {
                onValue(operation().trim())
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                controller.showError(error)
            }
        }
    }

    ScrollPage {
        Card {
            SectionTitle("Profiles")
            SelectableList("Configured profiles", controller.data.profiles, selectedProfile) { selectedProfile = it }
            PrimaryAction("Activate selected profile", onClick = {
                val selected = selectedProfile
                if (selected.isNullOrBlank()) {
                    controller.showError(IllegalArgumentException("Select a profile first"))
                } else {
                    controller.mutate { runtime.setActiveProfile(selected) }
                }
            })
        }

        Card {
            SectionTitle("Import Outline access key")
            SecondaryText("Paste, scan, or open a standard ss:// or basic dynamic ssconf:// key.")
            LabeledField(
                outlineKey,
                { outlineKey = it; reviewed = null },
                "Outline access key",
                modifier = Modifier.fillMaxWidth(),
                singleLine = false,
                minLines = 3,
            )
            LabeledField(outlineName, { outlineName = it; reviewed = null }, "Outline profile name", Modifier.fillMaxWidth())
            SecondaryText(outlineReview)
            ActionRow {
                SecondaryAction("Paste key", enabled = clipboard, onClick = {
                    launchInto({ outlineKey = it; reviewed = null }) { platform.clipboardRead() }
                })
                SecondaryAction("Scan key QR", enabled = platform.supports(Capability.QR_SCAN), onClick = {
                    launchInto({ outlineKey = it; reviewed = null }) { platform.scanQrCode() }
                })
                SecondaryAction("Review access key", onClick = {
                    val requestedKey = outlineKey
                    scope.launch {
                        try {
                            val preview = runtime.reviewOutlineAccessKey(requestedKey)
                            if (requestedKey != outlineKey) {
                                reviewed = null
                                return@launch
                            }
                            val suggested = preview.root["suggested_name"].text()
                            if (suggested.isNotBlank() && (outlineName.isBlank() || outlineName == "Outline")) {
                                outlineName = suggested
                            }
                            outlineReview = "Compatibility preview (credentials hidden):\n${preview.rawJson}\n" +
                                "Will create profile: $outlineName"
                            reviewed = requestedKey to outlineName
                            controller.clearError()
                        } catch (error: CancellationException) {
                            throw error
                        } catch (error: Throwable) {
                            reviewed = null
                            controller.showError(error)
                        }
                    }
                })
                PrimaryAction("Import Outline profile", enabled = reviewed != null, onClick = {
                    if (reviewed != (outlineKey to outlineName)) {
                        reviewed = null
                        controller.showError(IllegalStateException("Review this access key and profile name again"))
                    } else {
                        val key = outlineKey
                        val name = outlineName
                        controller.mutate { runtime.importOutlineAccessKey(key, name, false) }
                    }
                })
                SecondaryAction("Refresh selected dynamic profile", onClick = {
                    val selected = selectedProfile
                    if (selected.isNullOrBlank()) {
                        controller.showError(IllegalArgumentException("Select a profile first"))
                    } else {
                        controller.mutate { runtime.refreshOutlineProfile(selected) }
                    }
                })
            }
        }

        Card {
            SectionTitle("Convert Mihomo or Surge profile")
            SecondaryText("No providers, includes, or sidecars are fetched during conversion.")
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                Picker(
                    "Source profile format",
                    listOf("auto", "mihomo", "surge"),
                    converterFormat,
                    { converterFormat = it; converterReview = null },
                    Modifier.widthIn(max = 180.dp),
                )
                LabeledField(
                    converterName,
                    { converterName = it; converterReview = null },
                    "Converted profile name",
                    Modifier.weight(1f),
                )
            }
            Row(verticalAlignment = Alignment.CenterVertically) {
                Checkbox(
                    checked = converterActivate,
                    onCheckedChange = { converterActivate = it },
                    modifier = Modifier.semantics { contentDescription = "Activate converted profile after import" },
                )
                Text("Activate after import")
            }
            DocumentArea(
                controller.document(Doc.CONVERTER_SOURCE),
                { controller.setDocument(Doc.CONVERTER_SOURCE, it); converterReview = null },
                "Mihomo or Surge source profile",
                minLines = 7,
            )
            SecondaryText(converterDetails)
            ActionRow {
                SecondaryAction("Review conversion", onClick = {
                    val source = controller.document(Doc.CONVERTER_SOURCE)
                    scope.launch {
                        try {
                            val preview = runtime.reviewProfileConversion(source, converterFormat, converterName)
                            converterReview = preview
                            converterDetails = conversionSummary(preview)
                            controller.clearError()
                        } catch (error: CancellationException) {
                            throw error
                        } catch (error: Throwable) {
                            converterReview = null
                            controller.showError(error)
                        }
                    }
                })
                PrimaryAction("Merge profile", enabled = converterReview != null, onClick = {
                    val preview = converterReview ?: return@PrimaryAction
                    val source = controller.document(Doc.CONVERTER_SOURCE)
                    val format = converterFormat
                    val name = converterName
                    val activate = converterActivate
                    controller.mutate {
                        runtime.importProfileConversion(source, format, name, preview.root["sha256"].text(), activate)
                    }
                })
                SecondaryAction("Copy TOML to editor", enabled = converterReview != null, onClick = {
                    converterReview?.let { controller.setDocument(Doc.CONFIG_TRANSFER, it.root["toml"].text()) }
                })
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                LabeledField(converterFile, { converterFile = it }, "Converter source file path", Modifier.weight(1f))
                SecondaryAction("Read source file", enabled = files, onClick = {
                    val path = converterFile
                    controller.loadText(Doc.CONVERTER_SOURCE) { platform.readTextFile(path, MAX_IMPORT_BYTES) }
                })
            }
        }

        Card {
            SectionTitle("Import, export, and QR")
            SecondaryText(
                "TOML is validated transactionally. Exports can contain passwords and dynamic Outline URLs; " +
                    "handle them as secrets.",
            )
            DocumentArea(
                controller.document(Doc.CONFIG_TRANSFER),
                { controller.setDocument(Doc.CONFIG_TRANSFER, it) },
                "TOML configuration",
            )
            ActionRow {
                SecondaryAction("Load current config", onClick = {
                    controller.loadText(Doc.CONFIG_TRANSFER) { runtime.exportConfig() }
                })
                PrimaryAction("Import and apply", onClick = {
                    val toml = controller.document(Doc.CONFIG_TRANSFER)
                    controller.mutate { runtime.importConfig(toml) }
                })
                SecondaryAction("Paste", enabled = clipboard, onClick = {
                    controller.loadText(Doc.CONFIG_TRANSFER) { platform.clipboardRead() }
                })
                SecondaryAction("Copy", enabled = clipboard, onClick = {
                    val value = controller.document(Doc.CONFIG_TRANSFER)
                    controller.mutate(afterSuccess = null) { platform.clipboardWrite(value) }
                })
                SecondaryAction("Scan QR", enabled = platform.supports(Capability.QR_SCAN), onClick = {
                    controller.loadText(Doc.CONFIG_TRANSFER) { platform.scanQrCode() }
                })
                SecondaryAction("Share QR", enabled = platform.supports(Capability.QR_SHARE), onClick = {
                    val value = controller.document(Doc.CONFIG_TRANSFER)
                    controller.mutate(afterSuccess = null) { platform.shareQrCode(value) }
                })
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                LabeledField(configFile, { configFile = it }, "Configuration file path", Modifier.weight(1f))
                SecondaryAction("Read file", enabled = files, onClick = {
                    val path = configFile
                    controller.loadText(Doc.CONFIG_TRANSFER) { platform.readTextFile(path, MAX_IMPORT_BYTES) }
                })
                SecondaryAction("Write file", enabled = files, onClick = {
                    val path = configFile
                    val value = controller.document(Doc.CONFIG_TRANSFER)
                    controller.mutate(afterSuccess = null) { platform.writeTextFile(path, value) }
                })
            }
        }
    }
}

internal fun conversionSummary(preview: Document): String {
    val summary = preview.root["profiles"].elements()
    val warnings = preview.root["warnings"].elements()
    val details = if (summary.isEmpty()) {
        "No converted profile"
    } else {
        val first = summary[0]
        "${preview.root["format"].text()} · ${first["chain_count"].long(0)} chains · " +
            "${first["group_count"].long(0)} groups · ${first["rule_count"].long(0)} rules"
    }
    if (warnings.isEmpty()) return details
    return details + "\nWarnings:" + warnings.joinToString("") {
        "\n• ${it["path"].text()}: ${it["message"].text()}"
    }
}

@Composable
internal fun ActivityPage(controller: AppController) {
    val runtime = controller.runtime
    var selected by remember { mutableStateOf<DashboardData.Connection?>(null) }
    var ruleName by rememberSaveable { mutableStateOf("temporary-rule") }
    var action by rememberSaveable { mutableStateOf("direct") }
    FillPage {
        ActionRow {
            LabeledField(ruleName, { ruleName = it }, "New rule name", Modifier.widthIn(min = 200.dp, max = 280.dp))
            Picker("New rule action", listOf("direct", "block", "reject"), action, { action = it }, Modifier.widthIn(max = 180.dp))
            SecondaryAction("Create temporary rule", onClick = {
                controller.createConnectionRule(selected?.id, ruleName, action, persistent = false)
            })
            SecondaryAction("Create persistent rule", onClick = {
                controller.createConnectionRule(selected?.id, ruleName, action, persistent = true)
            })
        }
        Tabs(
            "Connections" to {
                TablePage("Live and retained traffic with application attribution") {
                    DataTable(
                        description = "Network connections",
                        columns = listOf(
                            TableColumn("Target", 2.6f) { it.target },
                            TableColumn("Network", 0.9f) { it.network.uppercase() },
                            TableColumn("Route", 1.4f) { it.route },
                            TableColumn("State", 1f) { it.state },
                            TableColumn("Application", 1.5f) { it.application },
                            TableColumn("Traffic", 1.6f) {
                                "${Formatters.rate(it.downloadBytesPerSecond)} ↓  ${Formatters.rate(it.uploadBytesPerSecond)} ↑"
                            },
                        ),
                        rows = controller.data.connections,
                        key = { it.id.ifBlank { it.hashCode() } },
                        selected = selected?.let { current -> controller.data.connections.firstOrNull { it.id == current.id } },
                        onSelect = { selected = it },
                    )
                }
            },
            "Decisions" to {
                DocumentEditor(controller, "Decision history", Doc.DECISIONS, "Traffic decisions JSON",
                    loader = { runtime.decisions(TrafficQuery.recent(500)) })
            },
            "Temporary rules" to {
                DocumentEditor(controller, "Temporary rules", Doc.TEMPORARY_RULES, "Temporary rules JSON",
                    loader = { runtime.temporaryRules() })
            },
        )
    }
}

@Composable
private fun TablePage(description: String, table: @Composable ColumnScope.() -> Unit) {
    FillPage {
        SecondaryText(description)
        table()
    }
}

@Composable
internal fun RoutingPage(controller: AppController) {
    val runtime = controller.runtime
    Tabs(
        "Servers and chains" to {
            TablePage("Ordered protocol hops, capabilities, and resolved locations") {
                DataTable(
                    description = "Servers and chains",
                    columns = listOf(
                        TableColumn<DashboardData.Server>("Chain", 1.5f) { it.chain },
                        TableColumn("Server", 1.7f) { it.name },
                        TableColumn("Protocol", 1.2f) { it.protocol },
                        TableColumn("Address", 2.3f) { it.address },
                        TableColumn("Location", 1.8f) { it.location },
                    ),
                    rows = controller.data.servers,
                )
            }
        },
        "Policy groups" to {
            DocumentEditor(
                controller, "Policy groups and selected chains", Doc.POLICY_GROUPS, "Policy groups JSON",
                loader = { runtime.policyGroups() },
                saver = { runtime.replacePolicyGroups(it) },
            ) {
                SecondaryAction("Run policy health tests", onClick = {
                    controller.mutate(afterSuccess = {
                        controller.loadDocument(Doc.POLICY_GROUPS) { runtime.policyGroups() }
                    }) { runtime.testPolicyGroups("{}") }
                })
            }
        },
    )
}

@Composable
internal fun RulesPage(controller: AppController) {
    val runtime = controller.runtime
    Tabs(
        "Resolved rules" to {
            TablePage("Rules are evaluated in profile order") {
                DataTable(
                    description = "Routing rules",
                    columns = listOf(
                        TableColumn<DashboardData.Rule>("Name", 1.8f) { it.name },
                        TableColumn("Action", 1.2f) { it.action },
                        TableColumn("Matches", 4.8f) { it.matchSummary },
                    ),
                    rows = controller.data.rules,
                )
            }
        },
        "Rule editor" to {
            DocumentEditor(controller, "Persisted profile rules", Doc.RULES, "Rules JSON",
                loader = { runtime.rules() }, saver = { runtime.replaceRules(it) })
        },
        "Rule sets" to {
            DocumentEditor(controller, "Remote and local rule sets", Doc.RULE_SETS, "Rule sets JSON",
                loader = { runtime.ruleSets() }, saver = { runtime.replaceRuleSets(it) }) {
                SecondaryAction("Refresh selected/all rule sets", onClick = {
                    controller.mutate(afterSuccess = {
                        controller.loadDocument(Doc.RULE_SETS) { runtime.ruleSets() }
                    }) { runtime.refreshRuleSets("{}") }
                })
            }
        },
        "Subscriptions" to {
            DocumentEditor(controller, "Rule subscriptions", Doc.SUBSCRIPTIONS, "Rule subscriptions JSON",
                loader = { runtime.ruleSubscriptions() }, saver = { runtime.replaceRuleSubscriptions(it) }) {
                SecondaryAction("Refresh selected/all subscriptions", onClick = {
                    controller.mutate(afterSuccess = {
                        controller.loadDocument(Doc.SUBSCRIPTIONS) { runtime.ruleSubscriptions() }
                    }) { runtime.refreshRuleSubscriptions("{}") }
                })
            }
        },
    )
}

@Composable
internal fun NetworkPage(controller: AppController) {
    val runtime = controller.runtime
    Tabs(
        "DNS and firewall" to {
            DocumentEditor(controller, "Encrypted DNS and leak-prevention settings", Doc.DNS, "DNS settings JSON",
                loader = { runtime.dns() }, saver = { runtime.updateDns(it) })
        },
        "Capture and runtime" to {
            DocumentEditor(controller, "Firewall, capture, route, and runtime settings", Doc.SETTINGS,
                "Runtime settings JSON", loader = { runtime.settings() }, saver = { runtime.updateSettings(it) })
        },
        "Conditioner" to {
            DocumentEditor(controller, "Latency, jitter, bandwidth, and loss simulation", Doc.CONDITIONER,
                "Conditioner settings JSON", loader = { runtime.conditioner() },
                saver = { runtime.updateConditioner(it) })
        },
    )
}

@Composable
internal fun PromptsPage(controller: AppController) {
    val runtime = controller.runtime
    var identifier by rememberSaveable { mutableStateOf("") }
    FillPage {
        ActionRow {
            LabeledField(identifier, { identifier = it }, "Prompt or decision identifier", Modifier.widthIn(min = 220.dp, max = 320.dp))
            SecondaryAction("Allow once", onClick = { controller.resolvePrompt(identifier, allow = true) })
            SecondaryAction("Block once", onClick = { controller.resolvePrompt(identifier, allow = false) })
            SecondaryAction("Promote forever", onClick = { controller.promotePrompt(identifier) })
        }
        Tabs(
            "Pending" to {
                DocumentEditor(controller, "Pending connection prompts", Doc.PENDING_PROMPTS, "Pending prompts JSON",
                    loader = { runtime.pendingPrompts() })
            },
            "Silent decisions" to {
                DocumentEditor(controller, "Decisions eligible for persistent promotion", Doc.PROMPT_DECISIONS,
                    "Prompt decisions JSON", loader = { runtime.promptDecisions() })
            },
        )
    }
}

@Composable
internal fun DeveloperPage(controller: AppController) {
    val runtime = controller.runtime
    val platform = controller.platform
    var breakpointId by rememberSaveable { mutableStateOf("") }
    val developer = controller.data.developer
    Tabs(
        "Captured traffic" to {
            FillPage {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    SecondaryText(
                        (if (developer.enabled) "Capture enabled" else "Capture disabled") + " · " +
                            Formatters.count(developer.captureCount, "entry", "entries"),
                        Modifier.weight(1f),
                    )
                    SecondaryAction("Clear entries", onClick = { controller.mutate { runtime.clearDeveloperEntries() } })
                    PrimaryAction(if (developer.enabled) "Disable capture" else "Enable capture", onClick = controller::toggleCapture)
                }
                DataTable(
                    description = "Developer traffic captures",
                    columns = listOf(
                        TableColumn<DashboardData.Capture>("Method", 0.9f) { it.method },
                        TableColumn("URL", 4.3f) { it.url },
                        TableColumn("Status", 0.9f) { it.status.toString() },
                        TableColumn("Error", 2.2f) { it.error },
                    ),
                    rows = developer.captures,
                )
            }
        },
        "Settings" to {
            DocumentEditor(controller, "Capture, redaction, TLS decryption, and cache settings", Doc.DEVELOPER_SETTINGS,
                "Developer settings JSON", loader = { runtime.developerSettings() },
                saver = { runtime.updateDeveloperSettings(it) })
        },
        "Map" to {
            DocumentEditor(controller, "Map Local and Map Remote rules are evaluated in order", Doc.DEVELOPER_MAP_RULES,
                "Developer map rules JSON", loader = { runtime.developerMapRules() },
                saver = { runtime.replaceDeveloperMapRules(it) })
        },
        "Breakpoints" to {
            DocumentEditor(controller, "Interactive request and response breakpoint rules",
                Doc.DEVELOPER_BREAKPOINT_RULES, "Developer breakpoint rules JSON",
                loader = { runtime.developerBreakpointRules() },
                saver = { runtime.replaceDeveloperBreakpointRules(it) })
        },
        "Pending" to {
            DocumentEditor(controller, "Paused requests continue automatically after 30 seconds",
                Doc.DEVELOPER_PENDING_BREAKPOINTS, "Pending developer breakpoints JSON",
                loader = { runtime.developerPendingBreakpoints() }) {
                LabeledField(breakpointId, { breakpointId = it }, "Pending developer breakpoint identifier",
                    Modifier.widthIn(min = 220.dp, max = 320.dp))
                SecondaryAction("Continue", onClick = { controller.resolveDeveloperBreakpoint(breakpointId, "continue") })
                SecondaryAction("Drop", onClick = { controller.resolveDeveloperBreakpoint(breakpointId, "drop") })
            }
        },
        "Rewrites" to {
            DocumentEditor(controller, "Ordered request and response header, body, URL, and status rewrites",
                Doc.DEVELOPER_REWRITE_RULES, "Developer rewrite rules JSON",
                loader = { runtime.developerRewriteRules() },
                saver = { runtime.replaceDeveloperRewriteRules(it) })
        },
        "CA" to {
            ScrollPage {
                SecondaryText(
                    "Install this CA only on devices you control. Regeneration invalidates the previous certificate.",
                )
                ActionRow {
                    SecondaryAction("Load CA PEM", onClick = {
                        controller.loadText(Doc.DEVELOPER_CA) { runtime.developerCaPem() }
                    })
                    SecondaryAction("Copy CA PEM", enabled = platform.supports(Capability.CLIPBOARD), onClick = {
                        val pem = controller.document(Doc.DEVELOPER_CA)
                        controller.mutate(afterSuccess = null) { platform.clipboardWrite(pem) }
                    })
                    SecondaryAction("Regenerate CA", onClick = {
                        controller.mutate(afterSuccess = {
                            controller.loadText(Doc.DEVELOPER_CA) { runtime.developerCaPem() }
                        }) { runtime.regenerateDeveloperCa() }
                    })
                }
                DocumentArea(controller.document(Doc.DEVELOPER_CA), {}, "Developer certificate authority PEM", readOnly = true)
            }
        },
        "Composer" to {
            ScrollPage {
                SecondaryText("Compose or import an HTTP request. Private targets remain blocked.")
                DocumentArea(
                    controller.document(Doc.DEVELOPER_COMPOSER),
                    { controller.setDocument(Doc.DEVELOPER_COMPOSER, it) },
                    "Developer request JSON",
                )
                ActionRow {
                    SecondaryAction("Import cURL JSON", onClick = {
                        val curl = controller.document(Doc.DEVELOPER_COMPOSER)
                        controller.loadDocument(Doc.DEVELOPER_RESULT) { runtime.importCurl(Json.obj("curl" to curl)) }
                    })
                    PrimaryAction("Send request", onClick = {
                        val request = controller.document(Doc.DEVELOPER_COMPOSER)
                        controller.loadDocument(Doc.DEVELOPER_RESULT) { runtime.sendDeveloperRequest(request) }
                    })
                }
                DocumentArea(controller.document(Doc.DEVELOPER_RESULT), {}, "Developer response JSON", readOnly = true)
            }
        },
    )
}

@Composable
internal fun SettingsPage(controller: AppController) {
    val runtime = controller.runtime
    val platform = controller.platform
    val licensing = platform.supports(Capability.LICENSING)
    val updates = platform.supports(Capability.UPDATES)
    val browser = platform.supports(Capability.BROWSER)
    ScrollPage {
        SectionTitle("Runtime adapter")
        DetailRow("Platform", runtime.displayName)
        DetailRow("UI toolkit", "Kotlin and Compose Multiplatform")
        DetailRow("Supported systems", "Android 12 / API 31 and newer · Ubuntu · Fedora")

        runtime.endpointSettings?.let { endpoint ->
            var baseUrl by rememberSaveable { mutableStateOf(endpoint.baseUrl) }
            var token by remember { mutableStateOf("") }
            HorizontalDivider()
            SectionTitle("GNU/Linux daemon")
            LabeledField(baseUrl, { baseUrl = it }, "ClambHook API URL", Modifier.fillMaxWidth())
            LabeledField(token, { token = it }, "ClambHook API bearer token", Modifier.fillMaxWidth(), secret = true)
            PrimaryAction("Apply connection settings", onClick = {
                try {
                    runtime.configureEndpoint(com.clambhook.ui.runtime.EndpointSettings(baseUrl, token))
                    controller.clearError()
                    controller.restartLiveEvents()
                    controller.refresh()
                } catch (error: Throwable) {
                    controller.showError(error)
                }
            })
        }

        HorizontalDivider()
        PerAppRoutingCard(controller)
        HorizontalDivider()

        var email by rememberSaveable { mutableStateOf("") }
        var licenseKey by remember { mutableStateOf("") }
        Card {
            SectionTitle("Licensing")
            SecondaryText(controller.supporterBadge, Modifier.semantics { contentDescription = "ClambHook supporter status" })
            if (controller.supporterThanks.isNotBlank()) {
                Text(controller.supporterThanks, Modifier.semantics { contentDescription = "Supporter thank-you message" })
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                LabeledField(email, { email = it }, "License email", Modifier.weight(1f))
                LabeledField(licenseKey, { licenseKey = it }, "License key", Modifier.weight(1f), secret = true)
            }
            ActionRow {
                SecondaryAction("Refresh license", enabled = licensing, onClick = {
                    controller.loadLicenseStatus(Doc.LICENSE) { platform.licensing("status", "{}") }
                })
                PrimaryAction("Activate", enabled = licensing, onClick = {
                    val request = Json.obj("email" to email, "license_key" to licenseKey)
                    controller.loadLicenseStatus(Doc.LICENSE) { platform.licensing("activate", request) }
                })
                SecondaryAction("Deactivate this device", enabled = licensing, onClick = {
                    controller.loadLicenseStatus(Doc.LICENSE) { platform.licensing("deactivate", "{}") }
                })
            }
            ActionRow {
                SecondaryAction("Buy Subscription", enabled = browser, description = "Buy Subscription (opens in browser)",
                    onClick = { controller.openBrowser(AppController.CLAMBHOOK_BUY_URL) })
                SecondaryAction("Manage Subscription", enabled = browser, description = "Manage Subscription (opens in browser)",
                    onClick = { controller.openBrowser(AppController.CLAMBHOOK_PORTAL_URL) })
            }
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.semantics { contentDescription = AppController.PAYMENT_PROVIDER_TRUST_SUMMARY },
            ) {
                Text("Card via")
                LinkAction("Creem", "Creem payment provider (opens in browser)", browser) {
                    controller.openBrowser(AppController.PAYMENT_PROVIDER_URLS.getValue("Creem"))
                }
                Text("· Cryptocurrency via")
                LinkAction("NOWPayments", "NOWPayments payment provider (opens in browser)", browser) {
                    controller.openBrowser(AppController.PAYMENT_PROVIDER_URLS.getValue("NOWPayments"))
                }
            }
            SecondaryText("Checkout opens in your browser at Swipht Store.")
            SectionTitle("Support independent ClambHook development")
            SecondaryText(
                "Donations never create licenses, extend subscriptions, change badges, or affect support priority.",
            )
            ActionRow {
                AppController.DONATION_URLS.forEach { (label, url) ->
                    SecondaryAction(label, enabled = browser, description = "$label (opens in browser)",
                        onClick = { controller.openBrowser(url) })
                }
            }
            DocumentArea(controller.document(Doc.LICENSE), {}, "License status JSON", readOnly = true, minLines = 6)
        }

        Card {
            SectionTitle("Updates")
            SecondaryText(
                "Updates are verified with the developer@jpfchang.org signing key: signed APKs on Android, " +
                    "the signed apt (Ubuntu) or dnf (Fedora) repository on GNU/Linux.",
            )
            ActionRow {
                SecondaryAction("Check for updates", enabled = updates, onClick = {
                    controller.loadPlatformResult(Doc.UPDATE) { platform.updates("check", "{}") }
                })
                PrimaryAction("Install verified update", enabled = updates, onClick = {
                    controller.loadPlatformResult(Doc.UPDATE) { platform.updates("install", "{}") }
                })
            }
            DocumentArea(controller.document(Doc.UPDATE), {}, "Update status JSON", readOnly = true, minLines = 6)
        }
        DetailRow("Notices", "Compose Multiplatform, native dependencies, and license texts are packaged with the application.")
    }
}

@Composable
private fun PerAppRoutingCard(controller: AppController) {
    val platform = controller.platform
    val supported = platform.supports(Capability.PER_APP_ROUTING)
    val scope = rememberCoroutineScope()
    var mode by rememberSaveable { mutableStateOf("all") }
    var packages by rememberSaveable { mutableStateOf("") }
    Card {
        SectionTitle("Per-application routing")
        SecondaryText(
            if (supported) "Android VPN inclusion and exclusion lists are owned by the Kotlin service layer."
            else "Per-application routing is only available on Android.",
        )
        Picker("Per-application routing mode", listOf("all", "include", "exclude"), mode, { mode = it },
            Modifier.widthIn(max = 220.dp))
        DocumentArea(packages, { packages = it }, "One Android package name per line", minLines = 7)
        ActionRow {
            SecondaryAction("Load routing settings", enabled = supported, onClick = {
                scope.launch {
                    try {
                        val settings = platform.appRoutingSettings()
                        mode = settings.mode
                        packages = settings.packageNames.joinToString("\n")
                        controller.clearError()
                    } catch (error: CancellationException) {
                        throw error
                    } catch (error: Throwable) {
                        controller.showError(error)
                    }
                }
            })
            SecondaryAction("List installed apps", enabled = supported, onClick = {
                scope.launch {
                    try {
                        packages = platform.installedApplications()
                            .joinToString("\n") { "${it.packageName} # ${it.label}" }
                        controller.clearError()
                    } catch (error: CancellationException) {
                        throw error
                    } catch (error: Throwable) {
                        controller.showError(error)
                    }
                }
            })
            PrimaryAction("Apply per-app routing", enabled = supported, onClick = {
                val selected = parsePackageList(packages)
                val selectedMode = mode
                controller.mutate { platform.updateAppRoutingSettings(selectedMode, selected) }
            })
        }
    }
}

/** One package name per line; `# label` comments and blank lines are ignored. */
internal fun parsePackageList(text: String): Set<String> =
    text.lines().map { it.replace(Regex("\\s+#.*$"), "").trim() }.filter { it.isNotBlank() }.toCollection(LinkedHashSet())

@Composable
private fun DocumentEditor(
    controller: AppController,
    description: String,
    key: Doc,
    label: String,
    loader: suspend () -> Document,
    saver: (suspend (String) -> Any?)? = null,
    additionalActions: @Composable () -> Unit = {},
) {
    ScrollPage {
        SecondaryText(description)
        ActionRow {
            SecondaryAction("Reload", onClick = { controller.loadDocument(key, loader) })
            if (saver != null) {
                PrimaryAction("Validate and apply", onClick = { controller.applyDocument(key, saver, loader) })
            }
            additionalActions()
        }
        DocumentArea(
            controller.document(key),
            { controller.setDocument(key, it) },
            label,
            readOnly = saver == null,
            minLines = 14,
        )
    }
}
