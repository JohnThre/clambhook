package com.clambhook.android

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Dns
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.Settings
import androidx.compose.material.icons.rounded.Stop
import androidx.compose.material3.AssistChip
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ElevatedAssistChip
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextField
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlin.math.max

enum class DashboardDestination {
    Now,
    Activity,
    Library
}

@Composable
fun DashboardScreen(
    destination: DashboardDestination,
    state: DashboardState,
    onRefresh: () -> Unit,
    onConnect: () -> Unit,
    onDisconnect: () -> Unit,
    onProfileSelected: (String) -> Unit,
    onOpenSettings: () -> Unit,
    onCreateRule: (RulePayload) -> Unit,
    modifier: Modifier = Modifier
) {
    LazyColumn(
        modifier = modifier
            .fillMaxSize()
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        when (destination) {
            DashboardDestination.Now -> {
                item { StatusCard(state, onRefresh, onConnect, onDisconnect) }
                item { NowActivityCard(state) }
            }

            DashboardDestination.Activity -> {
                item { TrafficCard(state, onCreateRule) }
                item { LogsCard(state) }
            }

            DashboardDestination.Library -> {
                item { ProfilesCard(state, onProfileSelected) }
                item { ListenersCard(state.status.listeners) }
                item { ServersCard(state.servers, onOpenSettings) }
            }
        }
    }
}

@Composable
private fun StatusCard(
    state: DashboardState,
    onRefresh: () -> Unit,
    onConnect: () -> Unit,
    onDisconnect: () -> Unit
) {
    Card(
        shape = RoundedCornerShape(8.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer)
    ) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            Row(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.Top
            ) {
                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Box(
                            modifier = Modifier
                                .size(10.dp)
                                .background(
                                    if (state.status.running) {
                                        MaterialTheme.colorScheme.primary
                                    } else {
                                        MaterialTheme.colorScheme.outline
                                    },
                                    CircleShape
                                )
                        )
                        Spacer(Modifier.width(8.dp))
                        Text(
                            if (state.status.running) "Running" else "Stopped",
                            style = MaterialTheme.typography.headlineSmall
                        )
                    }
                    Text(
                        if (state.apiOnline) "API online" else "API offline",
                        color = if (state.apiOnline) {
                            MaterialTheme.colorScheme.primary
                        } else {
                            MaterialTheme.colorScheme.error
                        },
                        style = MaterialTheme.typography.bodyMedium
                    )
                }
                Row(verticalAlignment = Alignment.CenterVertically) {
                    StatusPill(state.activeProfile.ifBlank { "No profile" })
                    IconButton(onClick = onRefresh, enabled = !state.isBusy) {
                        if (state.isRefreshing) {
                            CircularProgressIndicator(modifier = Modifier.size(18.dp), strokeWidth = 2.dp)
                        } else {
                            Icon(Icons.Rounded.Refresh, contentDescription = "Refresh")
                        }
                    }
                }
            }

            if (state.errorText.isNotBlank()) {
                Text(
                    state.errorText,
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodyMedium
                )
            }

            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                Button(
                    onClick = if (state.status.running) onDisconnect else onConnect,
                    enabled = !state.isBusy && state.apiOnline
                ) {
                    ButtonProgressOrIcon(
                        showProgress = state.actionInProgress == DashboardAction.Connect ||
                            state.actionInProgress == DashboardAction.Disconnect,
                        icon = {
                            Icon(
                                if (state.status.running) Icons.Rounded.Stop else Icons.Rounded.PlayArrow,
                                contentDescription = null
                            )
                        }
                    )
                    Text(
                        when {
                            state.actionInProgress == DashboardAction.Connect -> "Connecting"
                            state.actionInProgress == DashboardAction.Disconnect -> "Disconnecting"
                            state.status.running -> "Disconnect"
                            else -> "Connect"
                        }
                    )
                }
            }

            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                MetricPill("Active", state.activeConnections.toString())
                MetricPill("Down", formatRate(state.currentBandwidth.rxBps))
                MetricPill("Up", formatRate(state.currentBandwidth.txBps))
                MetricPill("Updated", formatUpdatedAt(state.lastUpdatedEpochMillis))
            }
        }
    }
}

@Composable
private fun NowActivityCard(state: DashboardState) {
    Card(shape = RoundedCornerShape(8.dp)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.Top
            ) {
                Column {
                    Text("Activity", style = MaterialTheme.typography.titleMedium)
                    Text(
                        "${state.traffic.summary.activeConnections} active connections",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
                Column(horizontalAlignment = Alignment.End) {
                    Text("${formatRate(state.traffic.summary.rxBps)} down", fontWeight = FontWeight.SemiBold)
                    Text("${formatRate(state.traffic.summary.txBps)} up", style = MaterialTheme.typography.bodySmall)
                }
            }
            BandwidthSparkline(
                samples = state.bandwidthSamples,
                modifier = Modifier
                    .fillMaxWidth()
                    .height(56.dp)
            )
            val latest = state.traffic.connections.firstOrNull()
            if (latest == null) {
                EmptyState("No activity yet", "Recent connection decisions will appear here.")
            } else {
                LatestConnectionRow(latest)
            }
        }
    }
}

@Composable
private fun LatestConnectionRow(connection: TrafficConnectionPayload) {
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(3.dp)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
            Text(
                connection.target.ifBlank { "Untitled connection" },
                fontWeight = FontWeight.SemiBold,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f)
            )
            Spacer(Modifier.width(12.dp))
            StatusPill(connection.actionFamily().uppercase())
        }
        Text(
            listOf(connection.application, connection.network, connection.chainName, connection.ruleName)
                .filter { it.isNotBlank() }
                .joinToString(" · ")
                .ifBlank { connection.listener.protocol },
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis
        )
    }
}

@Composable
private fun TrafficCard(state: DashboardState, onCreateRule: (RulePayload) -> Unit) {
    val traffic = state.traffic
    var filter by remember { mutableStateOf("all") }
    var search by remember { mutableStateOf("") }
    var draftRule by remember { mutableStateOf<RulePayload?>(null) }
    val counts = traffic.actionCounts()
    val visibleConnections = traffic.connections.filter { connection ->
        (filter == "all" || connection.actionFamily() == filter) &&
            (search.isBlank() || listOf(
                connection.target,
                connection.monitorHost(),
                connection.ruleName,
                connection.ruleAction,
                connection.chainName,
                connection.application,
                connection.network
            ).any { it.contains(search, ignoreCase = true) })
    }
    Card(shape = RoundedCornerShape(8.dp)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.Top
            ) {
                Column {
                    Text("Traffic", style = MaterialTheme.typography.titleMedium)
                    Text(
                        "${traffic.summary.activeConnections} tracked connections",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant
                    )
                }
                Column(horizontalAlignment = Alignment.End) {
                    Text("${formatRate(traffic.summary.rxBps)} down", fontWeight = FontWeight.SemiBold)
                    Text("${formatRate(traffic.summary.txBps)} up", style = MaterialTheme.typography.bodySmall)
                }
            }

            BandwidthSparkline(
                samples = state.bandwidthSamples,
                modifier = Modifier
                    .fillMaxWidth()
                    .height(56.dp)
            )

            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Text("Total down ${formatBytes(traffic.summary.rxTotal)}", style = MaterialTheme.typography.bodySmall)
                Text("Total up ${formatBytes(traffic.summary.txTotal)}", style = MaterialTheme.typography.bodySmall)
            }
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                listOf("all" to "All ${traffic.connections.size}", "proxy" to "Proxy ${counts["proxy"] ?: 0}", "direct" to "Direct ${counts["direct"] ?: 0}", "block" to "Block ${counts["block"] ?: 0}").forEach { (value, label) ->
                    FilterChip(selected = filter == value, onClick = { filter = value }, label = { Text(label) })
                }
            }
            TextField(
                value = search,
                onValueChange = { search = it },
                label = { Text("Search hosts, rules, chains") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth()
            )
            val hits = traffic.ruleHitSummaries()
            if (hits.isNotEmpty()) {
                Text(
                    "Rule hits " + hits.take(3).joinToString("  ") { "${it.ruleName}: ${it.count}" },
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant
                )
            }
            if (traffic.summary.persistError.isNotBlank()) {
                Text(traffic.summary.persistError, color = MaterialTheme.colorScheme.error)
            }
            if (visibleConnections.isEmpty()) {
                EmptyState("No matching activity", "Connection decisions appear here when traffic passes through clambhook.")
            } else {
                visibleConnections.take(8).forEach { connection ->
                    ConnectionRow(connection, onCreateRule = { draftRule = connection.ruleDraft() })
                }
            }
        }
    }
    draftRule?.let { rule ->
        RuleCreateDialog(
            initialRule = rule,
            chains = state.servers.chains.map { it.name },
            onDismiss = { draftRule = null },
            onSave = {
                onCreateRule(it)
                draftRule = null
            }
        )
    }
}

@Composable
private fun ConnectionRow(connection: TrafficConnectionPayload, onCreateRule: () -> Unit) {
    Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(3.dp)) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
            Text(
                connection.target.ifBlank { "--" },
                fontWeight = FontWeight.SemiBold,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f)
            )
            Spacer(Modifier.width(12.dp))
            StatusPill(connection.actionFamily().uppercase())
        }
        Text(
            listOf(connection.application, connection.network, connection.chainName, connection.ruleName)
                .filter { it.isNotBlank() }
                .joinToString(" · ")
                .ifBlank { connection.listener.protocol },
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis
        )
        Text(
            "${formatBytes(connection.rxTotal)} down · ${formatBytes(connection.txTotal)} up · ${formatDurationNs(connection.durationNs)}",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )
        OutlinedButton(onClick = onCreateRule, enabled = connection.ruleDraft() != null) {
            Text("Create rule")
        }
    }
}

@Composable
private fun RuleCreateDialog(
    initialRule: RulePayload,
    chains: List<String>,
    onDismiss: () -> Unit,
    onSave: (RulePayload) -> Unit
) {
    var name by remember(initialRule) { mutableStateOf(initialRule.name) }
    var action by remember(initialRule) { mutableStateOf(initialRule.action) }
    val match = initialRule.domains.firstOrNull() ?: initialRule.cidrs.firstOrNull() ?: "--"
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Create rule") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                TextField(value = name, onValueChange = { name = it }, label = { Text("Name") }, singleLine = true)
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    FilterChip(selected = action == "block", onClick = { action = "block" }, label = { Text("Block") })
                    FilterChip(selected = action == "direct", onClick = { action = "direct" }, label = { Text("Direct") })
                    chains.forEach { chain ->
                        FilterChip(selected = action == "chain:$chain", onClick = { action = "chain:$chain" }, label = { Text("Proxy: $chain") })
                    }
                }
                Text("Match $match", style = MaterialTheme.typography.bodySmall)
            }
        },
        confirmButton = {
            TextButton(
                onClick = { onSave(initialRule.copy(name = name.trim(), action = action)) },
                enabled = name.isNotBlank()
            ) { Text("Save") }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } }
    )
}

@Composable
private fun ProfilesCard(state: DashboardState, onProfileSelected: (String) -> Unit) {
    Card(shape = RoundedCornerShape(8.dp)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text("Profiles", style = MaterialTheme.typography.titleMedium)
            if (state.profiles.profiles.isEmpty()) {
                EmptyState("No profiles yet", "Add or import a profile in Settings.")
            } else {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    state.profiles.profiles.forEach { profile ->
                        FilterChip(
                            selected = profile == state.activeProfile,
                            onClick = { onProfileSelected(profile) },
                            enabled = !state.isBusy,
                            label = {
                                if (state.actionInProgress == DashboardAction.SwitchProfile && state.pendingProfile == profile) {
                                    Row(verticalAlignment = Alignment.CenterVertically) {
                                        CircularProgressIndicator(
                                            modifier = Modifier.size(14.dp),
                                            strokeWidth = 2.dp
                                        )
                                        Spacer(Modifier.width(8.dp))
                                        Text(profile)
                                    }
                                } else {
                                    Text(profile)
                                }
                            }
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun ListenersCard(listeners: List<ListenerStatusPayload>) {
    Card(shape = RoundedCornerShape(8.dp)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text("Listeners", style = MaterialTheme.typography.titleMedium)
            if (listeners.isEmpty()) {
                EmptyState("No listeners active", "Connect to start the configured listeners.")
            } else {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    listeners.forEach { listener ->
                        AssistChip(
                            onClick = {},
                            label = { Text("${listener.protocol} ${listener.addr} (${listener.activeConns})") },
                            leadingIcon = { Icon(Icons.Rounded.Dns, contentDescription = null) }
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun ServersCard(servers: ServersPayload, onOpenSettings: () -> Unit) {
    Card(shape = RoundedCornerShape(8.dp)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween, verticalAlignment = Alignment.CenterVertically) {
                Text("Servers", style = MaterialTheme.typography.titleMedium)
                OutlinedButton(onClick = onOpenSettings) {
                    Icon(Icons.Rounded.Settings, contentDescription = null)
                    Spacer(Modifier.width(8.dp))
                    Text("Settings")
                }
            }
            if (servers.chains.isEmpty()) {
                EmptyState("No servers in this profile", "Add a chain and server in Settings.")
            } else {
                servers.chains.forEach { chain ->
                    Text(chain.name, fontWeight = FontWeight.SemiBold)
                    chain.servers.forEach { server ->
                        Column(Modifier.fillMaxWidth().padding(start = 8.dp, bottom = 6.dp)) {
                            Text(
                                "${server.name} · ${server.protocol}",
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis
                            )
                            Text(
                                serverLocation(server),
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

@Composable
private fun LogsCard(state: DashboardState) {
    Card(shape = RoundedCornerShape(8.dp)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Row(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically
            ) {
                Text("Recent logs", style = MaterialTheme.typography.titleMedium)
                StatusPill(state.eventStreamStatus)
            }
            if (state.eventStreamError.isNotBlank()) {
                Text(
                    state.eventStreamError,
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall,
                    maxLines = 2,
                    overflow = TextOverflow.Ellipsis
                )
            }
            if (state.logs.isEmpty()) {
                EmptyState("No logs yet", "Connection and daemon events will appear here.")
            } else {
                state.logs.takeLast(12).forEach { line ->
                    Text(
                        line,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis
                    )
                }
            }
        }
    }
}

@Composable
private fun BandwidthSparkline(samples: List<BandwidthSample>, modifier: Modifier = Modifier) {
    val lineColor = MaterialTheme.colorScheme.primary
    val baselineColor = MaterialTheme.colorScheme.outlineVariant
    val backgroundColor = MaterialTheme.colorScheme.surfaceContainerHighest
    val values = samples.map { max(it.rxBps, it.txBps) }

    Canvas(
        modifier = modifier
            .clip(RoundedCornerShape(8.dp))
            .background(backgroundColor)
            .semantics { contentDescription = "Recent bandwidth graph" }
    ) {
        val midY = size.height / 2f
        drawLine(
            color = baselineColor,
            start = Offset(0f, midY),
            end = Offset(size.width, midY),
            strokeWidth = 1.dp.toPx()
        )
        if (values.size < 2) {
            return@Canvas
        }
        val maxValue = (values.maxOrNull() ?: 0.0).coerceAtLeast(1.0)
        val stepX = size.width / (values.lastIndex).coerceAtLeast(1)
        var previous: Offset? = null
        values.forEachIndexed { index, value ->
            val x = stepX * index
            val y = size.height - ((value / maxValue).toFloat() * size.height).coerceIn(0f, size.height)
            val current = Offset(x, y)
            previous?.let { last ->
                drawLine(
                    color = lineColor,
                    start = last,
                    end = current,
                    strokeWidth = 3.dp.toPx(),
                    cap = StrokeCap.Round
                )
            }
            previous = current
        }
    }
}

@Composable
private fun ButtonProgressOrIcon(showProgress: Boolean, icon: @Composable () -> Unit) {
    if (showProgress) {
        CircularProgressIndicator(modifier = Modifier.size(18.dp), strokeWidth = 2.dp)
    } else {
        icon()
    }
    Spacer(Modifier.width(8.dp))
}

@Composable
private fun MetricPill(label: String, value: String) {
    ElevatedAssistChip(
        onClick = {},
        label = {
            Column {
                Text(label, style = MaterialTheme.typography.labelSmall)
                Text(value, style = MaterialTheme.typography.bodySmall, fontWeight = FontWeight.SemiBold)
            }
        }
    )
}

@Composable
private fun StatusPill(text: String, modifier: Modifier = Modifier) {
    Surface(
        modifier = modifier.widthIn(max = 180.dp),
        shape = RoundedCornerShape(999.dp),
        color = MaterialTheme.colorScheme.secondaryContainer,
        contentColor = MaterialTheme.colorScheme.onSecondaryContainer
    ) {
        Text(
            text,
            modifier = Modifier.padding(horizontal = 10.dp, vertical = 5.dp),
            style = MaterialTheme.typography.labelMedium,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis
        )
    }
}

@Composable
private fun EmptyState(title: String, detail: String) {
    Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(title, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.SemiBold)
        Text(
            detail,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant
        )
    }
}
