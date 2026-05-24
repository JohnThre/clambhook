package com.clambhook.android

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.lifecycle.viewmodel.compose.viewModel

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val settingsStore = DataStoreSettingsStore(this)
        val tokenStore = EncryptedTokenStore(this)
        val configStore = AndroidConfigStore(this)
        val configValidator = AndroidConfigValidator(this)

        setContent {
            val settings by settingsStore.settings.collectAsState(initial = AppSettings())
            val token by tokenStore.token.collectAsState(initial = tokenStore.currentToken())
            var configToml by remember { mutableStateOf(defaultAndroidConfigToml) }

            LaunchedEffect(Unit) {
                configToml = configStore.readConfig()
            }
            LaunchedEffect(settings.embeddedDaemonEnabled, token) {
                configStore.ensureConfig()
                if (settings.embeddedDaemonEnabled) {
                    LocalDaemonService.start(this@MainActivity)
                } else {
                    LocalDaemonService.stop(this@MainActivity)
                }
            }

            val effectiveSettings = if (settings.embeddedDaemonEnabled) {
                settings.copy(apiBaseUrl = defaultAndroidApiBaseUrl)
            } else {
                settings
            }
            val apiClient = ClambhookApiClient(
                baseUrl = effectiveSettings.normalizedBaseUrl,
                tokenProvider = { token }
            )
            val viewModel: DashboardViewModel = viewModel(
                key = "${effectiveSettings.normalizedBaseUrl}:${token.hashCode()}",
                factory = DashboardViewModelFactory(apiClient)
            )

            LaunchedEffect(viewModel, effectiveSettings.normalizedRefreshIntervalSeconds) {
                viewModel.startPolling(effectiveSettings.normalizedRefreshIntervalSeconds)
            }
            LaunchedEffect(viewModel, effectiveSettings.eventStreamEnabled) {
                viewModel.startEventStream(effectiveSettings.eventStreamEnabled)
            }

            ClambhookApp(
                viewModel = viewModel,
                settings = effectiveSettings,
                token = token,
                configToml = configToml,
                onSaveSettings = { nextSettings, nextToken, nextConfigToml ->
                    val normalizedSettings = if (nextSettings.embeddedDaemonEnabled) {
                        nextSettings.copy(apiBaseUrl = defaultAndroidApiBaseUrl)
                    } else {
                        nextSettings
                    }
                    configStore.saveConfig(nextConfigToml)
                    configToml = nextConfigToml
                    settingsStore.save(normalizedSettings)
                    tokenStore.saveToken(nextToken)
                    if (normalizedSettings.embeddedDaemonEnabled) {
                        LocalDaemonService.restart(this@MainActivity)
                    } else {
                        LocalDaemonService.stop(this@MainActivity)
                    }
                },
                onValidateConfig = configValidator::validate
            )
        }
    }
}
