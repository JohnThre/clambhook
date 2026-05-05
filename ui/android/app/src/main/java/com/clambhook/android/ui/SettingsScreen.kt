package com.clambhook.android

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.launch

@Composable
fun SettingsScreen(
    settings: AppSettings,
    token: String,
    onSave: suspend (AppSettings, String) -> Unit,
    modifier: Modifier = Modifier
) {
    val scope = rememberCoroutineScope()
    var apiBaseUrl by remember { mutableStateOf(settings.apiBaseUrl) }
    var apiToken by remember { mutableStateOf(token) }
    var refreshSeconds by remember { mutableStateOf(settings.refreshIntervalSeconds.toString()) }
    var eventsEnabled by remember { mutableStateOf(settings.eventStreamEnabled) }

    LaunchedEffect(settings, token) {
        apiBaseUrl = settings.apiBaseUrl
        apiToken = token
        refreshSeconds = settings.refreshIntervalSeconds.toString()
        eventsEnabled = settings.eventStreamEnabled
    }

    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp)
    ) {
        Card {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                Text("Daemon API", style = MaterialTheme.typography.titleMedium)
                OutlinedTextField(
                    value = apiBaseUrl,
                    onValueChange = { apiBaseUrl = it },
                    label = { Text("Base URL") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri)
                )
                OutlinedTextField(
                    value = apiToken,
                    onValueChange = { apiToken = it },
                    label = { Text("Bearer token") },
                    singleLine = true,
                    visualTransformation = PasswordVisualTransformation(),
                    modifier = Modifier.fillMaxWidth()
                )
                OutlinedTextField(
                    value = refreshSeconds,
                    onValueChange = { refreshSeconds = it.filter(Char::isDigit) },
                    label = { Text("Refresh seconds") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number)
                )
                androidx.compose.foundation.layout.Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween
                ) {
                    Text("Event stream")
                    Switch(checked = eventsEnabled, onCheckedChange = { eventsEnabled = it })
                }
                Button(
                    onClick = {
                        scope.launch {
                            onSave(
                                AppSettings(
                                    apiBaseUrl = apiBaseUrl,
                                    refreshIntervalSeconds = refreshSeconds.toIntOrNull() ?: 5,
                                    eventStreamEnabled = eventsEnabled
                                ),
                                apiToken
                            )
                        }
                    },
                    modifier = Modifier.fillMaxWidth()
                ) {
                    Text("Save")
                }
            }
        }
    }
}
