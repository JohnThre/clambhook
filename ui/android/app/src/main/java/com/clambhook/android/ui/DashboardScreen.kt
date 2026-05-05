package com.clambhook.android

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.FilterChip
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp

@Composable
fun DashboardScreen(
    state: DashboardState,
    onRefresh: () -> Unit,
    onConnect: () -> Unit,
    onDisconnect: () -> Unit,
    onProfileSelected: (String) -> Unit,
    modifier: Modifier = Modifier
) {
    LazyColumn(
        modifier = modifier
            .fillMaxSize()
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        item {
            StatusCard(state, onRefresh, onConnect, onDisconnect)
        }
        item {
            ProfilesCard(state, onProfileSelected)
        }
        item {
            ListenersCard(state.status.listeners)
        }
        item {
            ServersCard(state.servers)
        }
        item {
            LogsCard(state.logs)
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
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Column {
                    Text(if (state.status.running) "Running" else "Stopped", style = MaterialTheme.typography.headlineSmall)
                    Text(
                        if (state.apiOnline) "API online" else "API offline",
                        color = if (state.apiOnline) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.error
                    )
                }
                Text(state.activeProfile.ifBlank { "No profile" }, fontWeight = FontWeight.SemiBold)
            }
            if (state.errorText.isNotBlank()) {
                Text(state.errorText, color = MaterialTheme.colorScheme.error)
            }
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = onConnect) { Text("Connect") }
                OutlinedButton(onClick = onDisconnect) { Text("Disconnect") }
                OutlinedButton(onClick = onRefresh) { Text("Refresh") }
            }
            Text("Active connections ${state.activeConnections}")
            Text("RX ${formatRate(state.currentBandwidth.rxBps)}  TX ${formatRate(state.currentBandwidth.txBps)}")
        }
    }
}

@Composable
private fun ProfilesCard(state: DashboardState, onProfileSelected: (String) -> Unit) {
    Card {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("Profiles", style = MaterialTheme.typography.titleMedium)
            if (state.profiles.profiles.isEmpty()) {
                Text("No profiles")
            } else {
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    state.profiles.profiles.forEach { profile ->
                        FilterChip(
                            selected = profile == state.activeProfile,
                            onClick = { onProfileSelected(profile) },
                            label = { Text(profile) }
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun ListenersCard(listeners: List<ListenerStatusPayload>) {
    Card {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("Listeners", style = MaterialTheme.typography.titleMedium)
            if (listeners.isEmpty()) {
                Text("No listeners")
            } else {
                listeners.forEach { listener ->
                    AssistChip(
                        onClick = {},
                        label = { Text("${listener.protocol} ${listener.addr} (${listener.activeConns})") }
                    )
                }
            }
        }
    }
}

@Composable
private fun ServersCard(servers: ServersPayload) {
    Card {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("Servers", style = MaterialTheme.typography.titleMedium)
            if (servers.chains.isEmpty()) {
                Text("No servers in active profile")
            } else {
                servers.chains.forEach { chain ->
                    Text(chain.name, fontWeight = FontWeight.SemiBold)
                    chain.servers.forEach { server ->
                        Column(Modifier.fillMaxWidth().padding(start = 8.dp, bottom = 6.dp)) {
                            Text("${server.name} · ${server.protocol}")
                            Text(serverLocation(server), style = MaterialTheme.typography.bodySmall)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun LogsCard(logs: List<String>) {
    Card {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text("Recent logs", style = MaterialTheme.typography.titleMedium)
            if (logs.isEmpty()) {
                Text("No log events")
            } else {
                logs.takeLast(12).forEach { line ->
                    Text(line, style = MaterialTheme.typography.bodySmall)
                }
            }
        }
    }
}
