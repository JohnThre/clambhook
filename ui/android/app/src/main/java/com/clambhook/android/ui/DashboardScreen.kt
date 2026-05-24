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
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ElevatedAssistChip
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
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

@Composable
fun DashboardScreen(
    state: DashboardState,
    onRefresh: () -> Unit,
    onConnect: () -> Unit,
    onDisconnect: () -> Unit,
    onProfileSelected: (String) -> Unit,
    onOpenSettings: () -> Unit,
    modifier: Modifier = Modifier
) {
    LazyColumn(
        modifier = modifier
            .fillMaxSize()
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        item {
            StatusCard(state, onRefresh, onConnect, onDisconnect)
        }
        item {
            TrafficCard(state)
        }
        item {
            ProfilesCard(state, onProfileSelected)
        }
        item {
            ListenersCard(state.status.listeners)
        }
        item {
            ServersCard(state.servers, onOpenSettings)
        }
        item {
            LogsCard(state)
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
                StatusPill(state.activeProfile.ifBlank { "No profile" })
            }

            if (state.errorText.isNotBlank()) {
                Text(
                    state.errorText,
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodyMedium
                )
            }

            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(
                    onClick = onConnect,
                    enabled = !state.isBusy && state.apiOnline && !state.status.running
                ) {
                    ButtonProgressOrIcon(
                        showProgress = state.actionInProgress == DashboardAction.Connect,
                        icon = { Icon(Icons.Rounded.PlayArrow, contentDescription = null) }
                    )
                    Text(if (state.actionInProgress == DashboardAction.Connect) "Connecting" else "Connect")
                }
                OutlinedButton(
                    onClick = onDisconnect,
                    enabled = !state.isBusy && state.apiOnline && state.status.running
                ) {
                    ButtonProgressOrIcon(
                        showProgress = state.actionInProgress == DashboardAction.Disconnect,
                        icon = { Icon(Icons.Rounded.Stop, contentDescription = null) }
                    )
                    Text(if (state.actionInProgress == DashboardAction.Disconnect) "Disconnecting" else "Disconnect")
                }
                OutlinedButton(onClick = onRefresh, enabled = !state.isBusy) {
                    ButtonProgressOrIcon(
                        showProgress = state.isRefreshing,
                        icon = { Icon(Icons.Rounded.Refresh, contentDescription = null) }
                    )
                    Text(if (state.isRefreshing) "Refreshing" else "Refresh")
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
private fun TrafficCard(state: DashboardState) {
    val traffic = state.traffic
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
            if (traffic.summary.persistError.isNotBlank()) {
                Text(traffic.summary.persistError, color = MaterialTheme.colorScheme.error)
            }
            if (traffic.connections.isEmpty()) {
                EmptyText("No traffic history yet")
            } else {
                traffic.connections.take(8).forEach { connection ->
                    ConnectionRow(connection)
                }
            }
        }
    }
}

@Composable
private fun ConnectionRow(connection: TrafficConnectionPayload) {
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
            Text(connection.state, style = MaterialTheme.typography.bodySmall)
        }
        Text(
            listOf(connection.application, connection.network, connection.chainName)
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
    }
}

@Composable
private fun ProfilesCard(state: DashboardState, onProfileSelected: (String) -> Unit) {
    Card(shape = RoundedCornerShape(8.dp)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text("Profiles", style = MaterialTheme.typography.titleMedium)
            if (state.profiles.profiles.isEmpty()) {
                EmptyText("No profiles found")
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
                EmptyText("No listeners are active")
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
                EmptyText("No servers in the active profile")
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
                EmptyText("No log events")
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
private fun EmptyText(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant
    )
}
