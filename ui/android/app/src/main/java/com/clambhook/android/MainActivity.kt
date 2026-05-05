package com.clambhook.android

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.lifecycle.viewmodel.compose.viewModel

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val settingsStore = DataStoreSettingsStore(this)
        val tokenStore = EncryptedTokenStore(this)

        setContent {
            val settings by settingsStore.settings.collectAsState(initial = AppSettings())
            val token by tokenStore.token.collectAsState(initial = tokenStore.currentToken())
            val apiClient = ClambhookApiClient(
                baseUrl = settings.normalizedBaseUrl,
                tokenProvider = { token }
            )
            val viewModel: DashboardViewModel = viewModel(
                key = "${settings.normalizedBaseUrl}:${token.hashCode()}",
                factory = DashboardViewModelFactory(apiClient)
            )

            LaunchedEffect(viewModel, settings.normalizedRefreshIntervalSeconds) {
                viewModel.startPolling(settings.normalizedRefreshIntervalSeconds)
            }
            LaunchedEffect(viewModel, settings.eventStreamEnabled) {
                viewModel.startEventStream(settings.eventStreamEnabled)
            }

            ClambhookApp(
                viewModel = viewModel,
                settings = settings,
                token = token,
                onSaveSettings = { nextSettings, nextToken ->
                    settingsStore.save(nextSettings)
                    tokenStore.saveToken(nextToken)
                }
            )
        }
    }
}
