package com.clambhook.android

import android.os.Bundle
import android.app.Activity
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.DisposableEffect
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
            val supportPurchaseManager = remember { SupportPurchaseManager(this@MainActivity) }
            val supportPurchaseState by supportPurchaseManager.state.collectAsState()
            val settings by settingsStore.settings.collectAsState(initial = AppSettings())
            val token by tokenStore.token.collectAsState(initial = tokenStore.currentToken())
            var configToml by remember { mutableStateOf(defaultAndroidConfigToml) }

            val vpnConsentLauncher = rememberLauncherForActivityResult(
                ActivityResultContracts.StartActivityForResult()
            ) { result ->
                if (result.resultCode == Activity.RESULT_OK) {
                    ClambhookTunnelController.start(this@MainActivity)
                }
            }
            val startTunnel: () -> Unit = {
                val consent = ClambhookTunnelController.consentIntent(this@MainActivity)
                if (consent != null) {
                    vpnConsentLauncher.launch(consent)
                } else {
                    ClambhookTunnelController.start(this@MainActivity)
                }
            }

            DisposableEffect(supportPurchaseManager) {
                supportPurchaseManager.start()
                onDispose { supportPurchaseManager.close() }
            }
            LaunchedEffect(Unit) {
                configToml = configStore.readConfig()
            }
            LaunchedEffect(settings.embeddedDaemonEnabled, token) {
                configStore.ensureConfig()
                if (settings.embeddedDaemonEnabled) {
                    startTunnel()
                } else {
                    ClambhookTunnelController.stop(this@MainActivity)
                }
            }

            val effectiveSettings = if (settings.embeddedDaemonEnabled) {
                settings.copy(apiBaseUrl = defaultAndroidApiBaseUrl)
            } else {
                settings
            }
            val useLocalTunnel = effectiveSettings.embeddedDaemonEnabled
            val dashboardApi: ClambhookApi
            val eventStream: ClambhookEventStream?
            if (useLocalTunnel) {
                dashboardApi = LocalTunnelApi(applicationContext)
                eventStream = null
            } else {
                val httpClient = ClambhookApiClient(
                    baseUrl = effectiveSettings.normalizedBaseUrl,
                    tokenProvider = { token }
                )
                dashboardApi = httpClient
                eventStream = httpClient
            }
            val viewModel: DashboardViewModel = viewModel(
                key = "${effectiveSettings.normalizedBaseUrl}:${token.hashCode()}:$useLocalTunnel",
                factory = DashboardViewModelFactory(dashboardApi, eventStream)
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
                supportPurchaseState = supportPurchaseState,
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
                        startTunnel()
                    } else {
                        ClambhookTunnelController.stop(this@MainActivity)
                    }
                },
                onValidateConfig = configValidator::validate,
                onPurchaseSupport = { productId ->
                    supportPurchaseManager.purchase(this@MainActivity, productId)
                },
                onClearSupportPurchaseMessage = supportPurchaseManager::clearMessage
            )
        }
    }
}
