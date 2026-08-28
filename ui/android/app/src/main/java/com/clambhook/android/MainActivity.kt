// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

import android.app.Activity
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val settingsStore = DataStoreSettingsStore(this)
        val configStore = AndroidConfigStore(this)
        val configValidator = AndroidConfigValidator(this)
        val licenseManager = LicenseManager(applicationContext)
        val updateManager = UpdateManager(applicationContext) { millis ->
            licenseManager.canInstallUpdate(millis)
        }

        setContent {
            val settings by settingsStore.settings.collectAsStateWithLifecycle(initialValue = AppSettings())
            var configToml by remember { mutableStateOf(defaultAndroidConfigToml) }
            val licenseState by licenseManager.state.collectAsStateWithLifecycle()
            val licenseScope = rememberCoroutineScope()
            val updateState by updateManager.state.collectAsStateWithLifecycle()
            val appContext = applicationContext
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
                    ClambhookTunnelController.start(appContext)
                }
            }
            val startTunnel: () -> Unit = {
                if (licenseState.decision.canUseApp) {
                    val consent = ClambhookTunnelController.consentIntent(appContext)
                    if (consent != null) {
                        vpnConsentLauncher.launch(consent)
                    } else {
                        ClambhookTunnelController.start(appContext)
                    }
                }
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
                    ClambhookTunnelController.stop(appContext)
                }
            }

            val dashboardApi = remember { LocalTunnelApi(appContext) }
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
                onSaveSettings = { nextSettings, nextConfigToml ->
                    configStore.saveConfig(nextConfigToml)
                    configToml = nextConfigToml
                    settingsStore.save(nextSettings)
                    startTunnel()
                },
                onValidateConfig = configValidator::validate,
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
                            ClambhookTunnelController.stop(appContext)
                            startTunnel()
                        }
                        viewModel.refresh()
                    }
                }
            )
        }
    }
}
