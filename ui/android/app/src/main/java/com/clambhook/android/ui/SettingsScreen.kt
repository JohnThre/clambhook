package com.clambhook.android

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Restore
import androidx.compose.material.icons.rounded.Save
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.Switch
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Checkbox
import androidx.compose.material3.FilterChip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow

@Composable
fun SettingsScreen(
    settings: AppSettings,
    configToml: String,
    onSave: suspend (AppSettings, String) -> Unit,
    onValidateConfig: suspend (String) -> Unit,
    onShowMessage: (String) -> Unit,
    modifier: Modifier = Modifier,
    conditioner: ConditionerPayload? = null,
    conditionerEditable: Boolean = true,
    conditionerLoading: Boolean = false,
    conditionerError: String = "",
    onLoadConditioner: () -> Unit = {},
    onUpdateConditioner: (ConditionerUpdateRequest) -> Unit = {}
) {
    val scope = rememberCoroutineScope()
    var refreshSeconds by remember { mutableStateOf(settings.refreshIntervalSeconds.toString()) }
    var configText by remember { mutableStateOf(configToml) }
    var saving by remember { mutableStateOf(false) }
    var confirmRestore by remember { mutableStateOf(false) }
    var showDashboardSettings by remember { mutableStateOf(true) }
    var showConfigEditor by remember { mutableStateOf(false) }
    var showConditioner by remember { mutableStateOf(false) }
    var showAppRouting by remember { mutableStateOf(false) }
    var splitMode by remember { mutableStateOf(settings.normalizedSplitTunnelMode) }
    var selectedPackages by remember { mutableStateOf(settings.normalizedSplitTunnelPackages) }
    var includeSystemApps by remember { mutableStateOf(false) }
    var appQuery by remember { mutableStateOf("") }
    var installedApps by remember { mutableStateOf<List<InstalledApp>>(emptyList()) }
    var appsLoading by remember { mutableStateOf(false) }
    val context = LocalContext.current

    LaunchedEffect(settings, configToml) {
        refreshSeconds = settings.refreshIntervalSeconds.toString()
        configText = configToml
        splitMode = settings.normalizedSplitTunnelMode
        selectedPackages = settings.normalizedSplitTunnelPackages
    }

    LaunchedEffect(showAppRouting) {
        if (showAppRouting && installedApps.isEmpty()) {
            appsLoading = true
            installedApps = runCatching { InstalledAppInventory.load(context) }.getOrDefault(emptyList())
            appsLoading = false
        }
    }

    LaunchedEffect(showConditioner) {
        if (showConditioner && conditioner == null) {
            onLoadConditioner()
        }
    }

    val validation = validateSettingsInput(
        refreshSeconds = refreshSeconds,
        configToml = configText
    )
    val hasChanges = refreshSeconds != settings.refreshIntervalSeconds.toString() ||
        splitMode != settings.normalizedSplitTunnelMode ||
        selectedPackages != settings.normalizedSplitTunnelPackages ||
        configText != configToml

    if (confirmRestore) {
        AlertDialog(
            onDismissRequest = { confirmRestore = false },
            title = { Text("Restore default config?") },
            text = { Text("This replaces the editor contents with the default local proxy config.") },
            confirmButton = {
                TextButton(
                    onClick = {
                        configText = defaultAndroidConfigToml
                        confirmRestore = false
                    }
                ) {
                    Text("Restore")
                }
            },
            dismissButton = {
                TextButton(onClick = { confirmRestore = false }) {
                    Text("Cancel")
                }
            }
        )
    }

    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        SettingsDisclosureHeader(
            title = "Dashboard refresh",
            expanded = showDashboardSettings,
            onToggle = { showDashboardSettings = !showDashboardSettings }
        )
        if (showDashboardSettings) {
            Card {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
                Text("Dashboard refresh", style = MaterialTheme.typography.titleMedium)
                OutlinedTextField(
                    value = refreshSeconds,
                    onValueChange = { refreshSeconds = it.filter(Char::isDigit) },
                    label = { Text("Refresh seconds") },
                    singleLine = true,
                    isError = validation.refreshSeconds != null,
                    supportingText = validation.refreshSeconds?.let { { Text(it) } },
                    modifier = Modifier.fillMaxWidth(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number)
                )
            }
        }
        }

        SettingsDisclosureHeader(
            title = "Config TOML",
            expanded = showConfigEditor,
            onToggle = { showConfigEditor = !showConfigEditor }
        )
        if (showConfigEditor) {
            Card {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically
                ) {
                    Text("Config TOML", style = MaterialTheme.typography.titleMedium)
                    OutlinedButton(onClick = { confirmRestore = true }, enabled = !saving) {
                        Icon(Icons.Rounded.Restore, contentDescription = null)
                        Spacer(Modifier.width(8.dp))
                        Text("Restore")
                    }
                }
                OutlinedTextField(
                    value = configText,
                    onValueChange = { configText = it },
                    label = { Text("Config TOML") },
                    minLines = 12,
                    isError = validation.configToml != null,
                    supportingText = validation.configToml?.let { { Text(it) } },
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(300.dp)
                )
            }
        }
        }

        SettingsDisclosureHeader(
            title = "App routing",
            expanded = showAppRouting,
            onToggle = { showAppRouting = !showAppRouting }
        )
        if (showAppRouting) {
            Card {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Text("Per-app routing", style = MaterialTheme.typography.titleMedium)
                    Text(
                        "Choose which apps route through the tunnel. Applies the next time the tunnel starts.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        FilterChip(
                            selected = splitMode == SplitTunnelMode.All,
                            onClick = { splitMode = SplitTunnelMode.All },
                            label = { Text("All apps") }
                        )
                        FilterChip(
                            selected = splitMode == SplitTunnelMode.Include,
                            onClick = { splitMode = SplitTunnelMode.Include },
                            label = { Text("Only selected") }
                        )
                        FilterChip(
                            selected = splitMode == SplitTunnelMode.Exclude,
                            onClick = { splitMode = SplitTunnelMode.Exclude },
                            label = { Text("Except selected") }
                        )
                    }
                    if (splitMode != SplitTunnelMode.All) {
                        Text(
                            "${selectedPackages.size} app(s) selected",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant
                        )
                        OutlinedTextField(
                            value = appQuery,
                            onValueChange = { appQuery = it },
                            label = { Text("Search apps") },
                            singleLine = true,
                            modifier = Modifier.fillMaxWidth()
                        )
                        SettingSwitchRow(
                            label = "Include system apps",
                            checked = includeSystemApps,
                            onCheckedChange = { includeSystemApps = it }
                        )
                        if (appsLoading) {
                            CircularProgressIndicator(
                                modifier = Modifier.height(20.dp).width(20.dp),
                                strokeWidth = 2.dp
                            )
                        } else {
                            val visibleApps = installedApps.filter { app ->
                                (includeSystemApps || !app.isSystem) &&
                                    (appQuery.isBlank() ||
                                        app.label.contains(appQuery, ignoreCase = true) ||
                                        app.packageName.contains(appQuery, ignoreCase = true))
                            }
                            LazyColumn(
                                modifier = Modifier.fillMaxWidth().heightIn(max = 320.dp),
                                verticalArrangement = Arrangement.spacedBy(4.dp)
                            ) {
                                items(visibleApps, key = { it.packageName }) { app ->
                                    Row(
                                        modifier = Modifier.fillMaxWidth(),
                                        verticalAlignment = Alignment.CenterVertically
                                    ) {
                                        Checkbox(
                                            checked = selectedPackages.contains(app.packageName),
                                            onCheckedChange = { checked ->
                                                selectedPackages = if (checked) {
                                                    selectedPackages + app.packageName
                                                } else {
                                                    selectedPackages - app.packageName
                                                }
                                            }
                                        )
                                        Spacer(Modifier.width(8.dp))
                                        Column(Modifier.weight(1f)) {
                                            Text(app.label, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                            Text(
                                                app.packageName,
                                                style = MaterialTheme.typography.bodySmall,
                                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                                maxLines = 1,
                                                overflow = TextOverflow.Ellipsis
                                            )
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }

        SettingsDisclosureHeader(
            title = "Network Conditioner",
            expanded = showConditioner,
            onToggle = { showConditioner = !showConditioner }
        )
        if (showConditioner) {
            Card {
                ConditionerSection(
                    conditioner = conditioner,
                    editable = conditionerEditable,
                    loading = conditionerLoading,
                    error = conditionerError,
                    onReload = onLoadConditioner,
                    onUpdate = onUpdateConditioner,
                    onShowMessage = onShowMessage
                )
            }
        }

        Button(
            onClick = {
                if (!validation.isValid) {
                    onShowMessage(validation.firstMessage ?: "Fix settings before saving")
                    return@Button
                }
                scope.launch {
                    saving = true
                    try {
                        onValidateConfig(configText)
                        onSave(
                            AppSettings(
                                refreshIntervalSeconds = refreshSeconds.toIntOrNull() ?: 5,
                                splitTunnelMode = splitMode,
                                splitTunnelPackages = selectedPackages
                            ),
                            configText
                        )
                        onShowMessage("Settings saved")
                    } catch (error: Throwable) {
                        onShowMessage("Save failed: ${error.message ?: error}")
                    } finally {
                        saving = false
                    }
                }
            },
            enabled = hasChanges && !saving,
            modifier = Modifier.fillMaxWidth()
        ) {
            if (saving) {
                CircularProgressIndicator(modifier = Modifier.height(18.dp).width(18.dp), strokeWidth = 2.dp)
                Spacer(Modifier.width(8.dp))
                Text("Saving")
            } else {
                Icon(Icons.Rounded.Save, contentDescription = null)
                Spacer(Modifier.width(8.dp))
                Text("Save")
            }
        }
    }
}

@Composable
private fun SettingsDisclosureHeader(
    title: String,
    expanded: Boolean,
    onToggle: () -> Unit
) {
    TextButton(onClick = onToggle, modifier = Modifier.fillMaxWidth()) {
        Text(
            if (expanded) "$title: Hide" else "$title: Show",
            modifier = Modifier.weight(1f)
        )
    }
}


@Composable
private fun SettingSwitchRow(
    label: String,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit
) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically
    ) {
        Text(label)
        Switch(checked = checked, onCheckedChange = onCheckedChange)
    }
}

@Composable
private fun ConditionerSection(
    conditioner: ConditionerPayload?,
    editable: Boolean,
    loading: Boolean,
    error: String,
    onReload: () -> Unit,
    onUpdate: (ConditionerUpdateRequest) -> Unit,
    onShowMessage: (String) -> Unit
) {
    // Editable form fields, seeded from the latest snapshot.
    var enabled by remember(conditioner) { mutableStateOf(conditioner?.enabled ?: false) }
    var downloadKbps by remember(conditioner) {
        mutableStateOf(conditioner?.downloadKbps?.takeIf { it > 0 }?.toString() ?: "")
    }
    var uploadKbps by remember(conditioner) {
        mutableStateOf(conditioner?.uploadKbps?.takeIf { it > 0 }?.toString() ?: "")
    }
    var latency by remember(conditioner) { mutableStateOf(conditioner?.latency ?: "") }
    var jitter by remember(conditioner) { mutableStateOf(conditioner?.jitter ?: "") }
    var lossPercent by remember(conditioner) {
        mutableStateOf(conditioner?.lossPercent?.takeIf { it > 0.0 }?.toString() ?: "")
    }

    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Text("Network Conditioner", style = MaterialTheme.typography.titleMedium)
        Text(
            "Shape bandwidth, latency, jitter and loss for the active profile.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )

        when {
            // Loading state.
            loading && conditioner == null -> {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(
                        modifier = Modifier.height(20.dp).width(20.dp),
                        strokeWidth = 2.dp
                    )
                    Spacer(Modifier.width(8.dp))
                    Text("Loading conditioner…", style = MaterialTheme.typography.bodySmall)
                }
            }
            // Error state.
            error.isNotBlank() && conditioner == null -> {
                Text(
                    error,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error
                )
                OutlinedButton(onClick = onReload) { Text("Retry") }
            }
            // Empty state (never loaded).
            conditioner == null -> {
                Text(
                    "No conditioner data yet.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
                OutlinedButton(onClick = onReload) { Text("Load") }
            }
            // Content state.
            else -> {
                if (!editable) {
                    Text(
                        "Editing requires the daemon HTTP API and is unavailable on-device; showing the current snapshot read-only.",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.tertiary
                    )
                }
                if (conditioner.profile.isNotBlank()) {
                    Text(
                        "Profile: ${conditioner.profile}",
                        style = MaterialTheme.typography.labelMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
                SettingSwitchRow(
                    label = "Enabled",
                    checked = enabled,
                    onCheckedChange = { if (editable) enabled = it }
                )
                OutlinedTextField(
                    value = downloadKbps,
                    onValueChange = { downloadKbps = it.filter(Char::isDigit) },
                    label = { Text("Download (kbps)") },
                    singleLine = true,
                    enabled = editable,
                    modifier = Modifier.fillMaxWidth(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number)
                )
                OutlinedTextField(
                    value = uploadKbps,
                    onValueChange = { uploadKbps = it.filter(Char::isDigit) },
                    label = { Text("Upload (kbps)") },
                    singleLine = true,
                    enabled = editable,
                    modifier = Modifier.fillMaxWidth(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number)
                )
                OutlinedTextField(
                    value = latency,
                    onValueChange = { latency = it },
                    label = { Text("Latency (e.g. 40ms)") },
                    singleLine = true,
                    enabled = editable,
                    modifier = Modifier.fillMaxWidth()
                )
                OutlinedTextField(
                    value = jitter,
                    onValueChange = { jitter = it },
                    label = { Text("Jitter (e.g. 5ms)") },
                    singleLine = true,
                    enabled = editable,
                    modifier = Modifier.fillMaxWidth()
                )
                OutlinedTextField(
                    value = lossPercent,
                    onValueChange = { value ->
                        lossPercent = value.filter { it.isDigit() || it == '.' }
                    },
                    label = { Text("Loss (%)") },
                    singleLine = true,
                    enabled = editable,
                    modifier = Modifier.fillMaxWidth(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Decimal)
                )
                if (error.isNotBlank()) {
                    Text(
                        error,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error
                    )
                }
                Button(
                    onClick = {
                        onUpdate(
                            ConditionerUpdateRequest(
                                profile = conditioner.profile.ifBlank { null },
                                enabled = enabled,
                                downloadKbps = downloadKbps.toIntOrNull(),
                                uploadKbps = uploadKbps.toIntOrNull(),
                                latency = latency.ifBlank { null },
                                jitter = jitter.ifBlank { null },
                                lossPercent = lossPercent.toDoubleOrNull()
                            )
                        )
                        onShowMessage("Saving conditioner")
                    },
                    enabled = editable && !loading,
                    modifier = Modifier.fillMaxWidth()
                ) {
                    if (loading) {
                        CircularProgressIndicator(
                            modifier = Modifier.height(18.dp).width(18.dp),
                            strokeWidth = 2.dp
                        )
                        Spacer(Modifier.width(8.dp))
                        Text("Saving")
                    } else {
                        Icon(Icons.Rounded.Save, contentDescription = null)
                        Spacer(Modifier.width(8.dp))
                        Text("Save conditioner")
                    }
                }
            }
        }
    }
}
