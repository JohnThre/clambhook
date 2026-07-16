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
import androidx.compose.material.icons.rounded.Favorite
import androidx.compose.material.icons.rounded.Restore
import androidx.compose.material.icons.rounded.Save
import androidx.compose.material.icons.rounded.Visibility
import androidx.compose.material.icons.rounded.VisibilityOff
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
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
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
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
    token: String,
    configToml: String,
    supportPurchaseState: SupportPurchaseState,
    onSave: suspend (AppSettings, String, String) -> Unit,
    onValidateConfig: suspend (String) -> Unit,
    onPurchaseSupport: (String) -> Unit,
    onShowMessage: (String) -> Unit,
    modifier: Modifier = Modifier
) {
    val scope = rememberCoroutineScope()
    var apiBaseUrl by remember { mutableStateOf(settings.apiBaseUrl) }
    var apiToken by remember { mutableStateOf(token) }
    var refreshSeconds by remember { mutableStateOf(settings.refreshIntervalSeconds.toString()) }
    var eventsEnabled by remember { mutableStateOf(settings.eventStreamEnabled) }
    var embeddedDaemonEnabled by remember { mutableStateOf(settings.embeddedDaemonEnabled) }
    var configText by remember { mutableStateOf(configToml) }
    var tokenVisible by remember { mutableStateOf(false) }
    var saving by remember { mutableStateOf(false) }
    var confirmRestore by remember { mutableStateOf(false) }
    var showApiSettings by remember { mutableStateOf(true) }
    var showConfigEditor by remember { mutableStateOf(false) }
    var showSupportOptions by remember { mutableStateOf(false) }
    var showAppRouting by remember { mutableStateOf(false) }
    var splitMode by remember { mutableStateOf(settings.normalizedSplitTunnelMode) }
    var selectedPackages by remember { mutableStateOf(settings.normalizedSplitTunnelPackages) }
    var includeSystemApps by remember { mutableStateOf(false) }
    var appQuery by remember { mutableStateOf("") }
    var installedApps by remember { mutableStateOf<List<InstalledApp>>(emptyList()) }
    var appsLoading by remember { mutableStateOf(false) }
    val context = LocalContext.current

    LaunchedEffect(settings, token, configToml) {
        apiBaseUrl = settings.apiBaseUrl
        apiToken = token
        refreshSeconds = settings.refreshIntervalSeconds.toString()
        eventsEnabled = settings.eventStreamEnabled
        embeddedDaemonEnabled = settings.embeddedDaemonEnabled
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

    val validation = validateSettingsInput(
        apiBaseUrl = apiBaseUrl,
        apiToken = apiToken,
        refreshSeconds = refreshSeconds,
        embeddedDaemonEnabled = embeddedDaemonEnabled,
        configToml = configText
    )
    val hasChanges = apiBaseUrl != settings.apiBaseUrl ||
        apiToken != token ||
        refreshSeconds != settings.refreshIntervalSeconds.toString() ||
        eventsEnabled != settings.eventStreamEnabled ||
        embeddedDaemonEnabled != settings.embeddedDaemonEnabled ||
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
            title = "Daemon API",
            expanded = showApiSettings,
            onToggle = { showApiSettings = !showApiSettings }
        )
        if (showApiSettings) {
            Card {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
                Text("Daemon API", style = MaterialTheme.typography.titleMedium)
                SettingSwitchRow(
                    label = "Embedded daemon",
                    checked = embeddedDaemonEnabled,
                    onCheckedChange = { embeddedDaemonEnabled = it }
                )
                OutlinedTextField(
                    value = apiBaseUrl,
                    onValueChange = { apiBaseUrl = it },
                    label = { Text("Base URL") },
                    singleLine = true,
                    enabled = !embeddedDaemonEnabled,
                    isError = validation.apiBaseUrl != null,
                    supportingText = validation.apiBaseUrl?.let { { Text(it) } },
                    modifier = Modifier.fillMaxWidth(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri)
                )
                OutlinedTextField(
                    value = apiToken,
                    onValueChange = { apiToken = it },
                    label = { Text("Bearer token") },
                    singleLine = true,
                    isError = validation.apiToken != null,
                    supportingText = validation.apiToken?.let { { Text(it) } },
                    visualTransformation = if (tokenVisible) VisualTransformation.None else PasswordVisualTransformation(),
                    trailingIcon = {
                        IconButton(onClick = { tokenVisible = !tokenVisible }) {
                            Icon(
                                if (tokenVisible) Icons.Rounded.VisibilityOff else Icons.Rounded.Visibility,
                                contentDescription = if (tokenVisible) "Hide token" else "Show token"
                            )
                        }
                    },
                    modifier = Modifier.fillMaxWidth()
                )
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
                SettingSwitchRow(
                    label = "Event stream",
                    checked = eventsEnabled,
                    onCheckedChange = { eventsEnabled = it }
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

        if (supportPurchaseState.visible) {
            SettingsDisclosureHeader(
                title = "Support",
                expanded = showSupportOptions,
                onToggle = { showSupportOptions = !showSupportOptions }
            )
        }

        if (supportPurchaseState.visible && showSupportOptions) {
            Card {
                Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Text("Support", style = MaterialTheme.typography.titleMedium)
                        if (supportPurchaseState.loading) {
                            CircularProgressIndicator(
                                modifier = Modifier.height(18.dp).width(18.dp),
                                strokeWidth = 2.dp
                            )
                        }
                    }

                    if (supportPurchaseState.products.isEmpty()) {
                        Text("Support options unavailable", color = MaterialTheme.colorScheme.onSurfaceVariant)
                    } else {
                        supportPurchaseState.products.forEach { product ->
                            OutlinedButton(
                                onClick = { onPurchaseSupport(product.id) },
                                enabled = !supportPurchaseState.purchasing,
                                modifier = Modifier.fillMaxWidth()
                            ) {
                                Icon(Icons.Rounded.Favorite, contentDescription = null)
                                Spacer(Modifier.width(8.dp))
                                Text(product.name, modifier = Modifier.weight(1f))
                                Text(product.price)
                            }
                        }
                    }
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
                                apiBaseUrl = apiBaseUrl,
                                refreshIntervalSeconds = refreshSeconds.toIntOrNull() ?: 5,
                                eventStreamEnabled = eventsEnabled,
                                embeddedDaemonEnabled = embeddedDaemonEnabled,
                                splitTunnelMode = splitMode,
                                splitTunnelPackages = selectedPackages
                            ),
                            apiToken,
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
