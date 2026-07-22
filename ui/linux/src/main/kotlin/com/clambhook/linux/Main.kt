package com.clambhook.linux

import androidx.compose.ui.window.application
import com.clambhook.linux.license.*
import com.clambhook.linux.settings.FileSettingsStore
import com.clambhook.linux.settings.SecretTokenVault
import com.clambhook.linux.settings.normalized
import com.clambhook.linux.daemon.DaemonSupervisor
import com.clambhook.linux.store.DashboardStore
import com.clambhook.linux.api.ClambhookApiClient
import com.clambhook.linux.ui.MainWindow
import com.clambhook.linux.ui.MainViewModel

fun main() {
    val settingsStore = FileSettingsStore()
    val tokenVault = SecretTokenVault()
    val daemon = DaemonSupervisor()
    val licenseStateStore = FileLicenseStateStore()
    val licenseKeyVault = SecretLicenseKeyVault()
    val appBaseDir = DaemonSupervisor.defaultAppBaseDir()
    val helperPath = LicenseHelperClient.resolveHelperPath(appBaseDir) ?: ""
    val helper = LicenseHelperClient(helperPath)
    val license = LicenseManager(licenseStateStore, licenseKeyVault, helper)

    var apiToken = ""
    val settings = settingsStore.load().normalized()
    val client = ClambhookApiClient(settings.apiEndpoint) { apiToken }
    val store = DashboardStore(client, settings.logRetention)

    val viewModel = MainViewModel(
        store = store,
        client = client,
        settingsStore = settingsStore,
        tokenVault = tokenVault,
        daemon = daemon,
        license = license,
        initialSettings = settings,
        onTokenLoaded = { apiToken = it }
    )

    application {
        MainWindow(viewModel)
    }
}