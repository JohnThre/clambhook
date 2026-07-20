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
import android.content.Intent
import android.net.Uri
import androidx.compose.runtime.rememberCoroutineScope
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val settingsStore = DataStoreSettingsStore(this)
        val configStore = AndroidConfigStore(this)
        val configValidator = AndroidConfigValidator(this)

        setContent {
            val supportPurchaseManager = remember { SupportPurchaseManager(this@MainActivity) }
            val supportPurchaseState by supportPurchaseManager.state.collectAsState()
            val settings by settingsStore.settings.collectAsState(initial = AppSettings())
            var configToml by remember { mutableStateOf(defaultAndroidConfigToml) }
            val licenseManager = remember { LicenseManager(this@MainActivity) }
            val licenseState by licenseManager.state.collectAsState()
            val licenseScope = rememberCoroutineScope()
            val updateManager = remember {
                UpdateManager(this@MainActivity) { millis -> licenseManager.canInstallUpdate(millis) }
            }
            val updateState by updateManager.state.collectAsState()
            val openUrl: (String) -> Unit = { url ->
                runCatching {
                    startActivity(
                        Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                    )
                }
            }

            val vpnConsentLauncher = rememberLauncherForActivityResult(
                ActivityResultContracts.StartActivityForResult()
            ) { result ->
                if (result.resultCode == Activity.RESULT_OK) {
                    ClambhookTunnelController.start(this@MainActivity)
                }
            }
            val startTunnel: () -> Unit = {
                if (licenseState.decision.canUseApp) {
                    val consent = ClambhookTunnelController.consentIntent(this@MainActivity)
                    if (consent != null) {
                        vpnConsentLauncher.launch(consent)
                    } else {
                        ClambhookTunnelController.start(this@MainActivity)
                    }
                }
            }

            DisposableEffect(supportPurchaseManager) {
                supportPurchaseManager.start()
                onDispose { supportPurchaseManager.close() }
            }
            LaunchedEffect(Unit) { licenseManager.start() }
            LaunchedEffect(Unit) {
                configToml = configStore.readConfig()
            }
            LaunchedEffect(licenseState.initialized, licenseState.decision.canUseApp) {
                configStore.ensureConfig()
                if (licenseState.decision.canUseApp) {
                    startTunnel()
                } else {
                    ClambhookTunnelController.stop(this@MainActivity)
                }
            }

            val dashboardApi = remember { LocalTunnelApi(applicationContext) }
            val viewModel: DashboardViewModel = viewModel(
                key = "local-tunnel",
                factory = DashboardViewModelFactory(dashboardApi, null)
            )

            LaunchedEffect(viewModel, settings.normalizedRefreshIntervalSeconds) {
                viewModel.startPolling(settings.normalizedRefreshIntervalSeconds)
            }

            ClambhookApp(
                viewModel = viewModel,
                settings = settings,
                configToml = configToml,
                supportPurchaseState = supportPurchaseState,
                onSaveSettings = { nextSettings, nextConfigToml ->
                    configStore.saveConfig(nextConfigToml)
                    configToml = nextConfigToml
                    settingsStore.save(nextSettings)
                    startTunnel()
                },
                onValidateConfig = configValidator::validate,
                onPurchaseSupport = { productId ->
                    supportPurchaseManager.purchase(this@MainActivity, productId)
                },
                onClearSupportPurchaseMessage = supportPurchaseManager::clearMessage,
                licenseState = licenseState,
                onActivateLicense = { key, email -> licenseScope.launch { licenseManager.activate(key, email) } },
                onDeactivateLicense = { licenseScope.launch { licenseManager.deactivateCurrentDevice() } },
                onReactivateLicense = { licenseScope.launch { licenseManager.reactivateCurrentDevice() } },
                onTransferLicense = { licenseScope.launch { licenseManager.transferCurrentDevice() } },
                onClearLicenseMessage = licenseManager::clearMessage,
                onOpenUrl = openUrl,
                licenseBuyUrl = licenseManager.buyUrl,
                licensePortalUrl = licenseManager.portalUrl,
                updateState = updateState,
                onCheckUpdates = { licenseScope.launch { updateManager.check() } },
                onInstallUpdate = { licenseScope.launch { updateManager.downloadAndInstall() } },
                onProfilesImported = {
                    licenseScope.launch {
                        configToml = configStore.readConfig()
                        if (licenseState.decision.canUseApp) {
                            ClambhookTunnelController.stop(this@MainActivity)
                            startTunnel()
                        }
                        viewModel.refresh()
                    }
                }
            )
        }
    }
}
