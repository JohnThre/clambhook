package com.clambhook.android

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.FileDownload
import androidx.compose.material.icons.rounded.History
import androidx.compose.material.icons.rounded.NetworkCheck
import androidx.compose.material.icons.rounded.Settings
import androidx.compose.material.icons.rounded.Tune
import androidx.compose.material.icons.rounded.VpnKey
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Shapes
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.runtime.collectAsState
import com.clambhook.android.AppSettings
import com.clambhook.android.DashboardViewModel
import kotlinx.coroutines.launch

private enum class AppTab {
    Imports,
    Status,
    Profiles,
    Activity,
    Settings,
    License
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ClambhookApp(
    viewModel: DashboardViewModel,
    settings: AppSettings,
    configToml: String,
    supportPurchaseState: SupportPurchaseState,
    licenseState: LicenseUiState,
    onSaveSettings: suspend (AppSettings, String) -> Unit,
    onValidateConfig: suspend (String) -> Unit,
    onPurchaseSupport: (String) -> Unit,
    onClearSupportPurchaseMessage: () -> Unit,
    onActivateLicense: (String, String) -> Unit,
    onDeactivateLicense: () -> Unit,
    onReactivateLicense: () -> Unit,
    onTransferLicense: () -> Unit,
    onClearLicenseMessage: () -> Unit,
    onOpenUrl: (String) -> Unit,
    licenseBuyUrl: String,
    licensePortalUrl: String,
    updateState: UpdateUiState,
    onCheckUpdates: () -> Unit,
    onInstallUpdate: () -> Unit,
    onProfilesImported: () -> Unit
) {
    val context = LocalContext.current
    val colorScheme = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
        if (isSystemInDarkTheme()) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
    } else {
        if (isSystemInDarkTheme()) androidx.compose.material3.darkColorScheme() else androidx.compose.material3.lightColorScheme()
    }
    var selectedTab by rememberSaveable { mutableStateOf(AppTab.Status) }
    val state by viewModel.state.collectAsState()
    val snackbarHostState = remember { SnackbarHostState() }
    val snackbarScope = rememberCoroutineScope()
    val showMessage: (String) -> Unit = { message ->
        snackbarScope.launch { snackbarHostState.showSnackbar(message) }
    }
    val notificationPermissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { granted ->
        if (!granted) {
            showMessage("Daemon will run without notification alerts until notifications are allowed")
        }
    }

    androidx.compose.runtime.LaunchedEffect(Unit) {
        if (
            Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            context.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            notificationPermissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
        }
    }

    androidx.compose.runtime.LaunchedEffect(supportPurchaseState.statusMessage) {
        supportPurchaseState.statusMessage?.let { message ->
            showMessage(message)
            onClearSupportPurchaseMessage()
        }
    }

    androidx.compose.runtime.LaunchedEffect(licenseState.message) {
        if (licenseState.message.isNotBlank()) {
            showMessage(licenseState.message)
            onClearLicenseMessage()
        }
    }

    MaterialTheme(
        colorScheme = colorScheme,
        shapes = Shapes(
            extraSmall = RoundedCornerShape(4.dp),
            small = RoundedCornerShape(8.dp),
            medium = RoundedCornerShape(8.dp),
            large = RoundedCornerShape(8.dp),
            extraLarge = RoundedCornerShape(8.dp)
        )
    ) {
        Surface {
            Scaffold(
                topBar = {
                    TopAppBar(
                        title = {
                            Column {
                                Text("clambhook")
                                Text(
                                    if (state.status.running) "Running" else "Stopped",
                                    style = MaterialTheme.typography.labelMedium,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant
                                )
                            }
                        },
                        actions = {
                            IconButton(onClick = { selectedTab = AppTab.License }) {
                                Icon(Icons.Rounded.VpnKey, contentDescription = "License")
                            }
                            IconButton(onClick = { selectedTab = AppTab.Settings }) {
                                Icon(Icons.Rounded.Settings, contentDescription = "Settings")
                            }
                        }
                    )
                },
                snackbarHost = { SnackbarHost(snackbarHostState) },
                bottomBar = {
                    NavigationBar {
                        NavigationBarItem(
                            selected = selectedTab == AppTab.Imports,
                            onClick = { selectedTab = AppTab.Imports },
                            icon = { Icon(Icons.Rounded.FileDownload, contentDescription = "Imports") },
                            label = { Text("Imports") }
                        )
                        NavigationBarItem(
                            selected = selectedTab == AppTab.Status,
                            onClick = { selectedTab = AppTab.Status },
                            icon = { Icon(Icons.Rounded.NetworkCheck, contentDescription = "Status") },
                            label = { Text("Status") }
                        )
                        NavigationBarItem(
                            selected = selectedTab == AppTab.Profiles,
                            onClick = { selectedTab = AppTab.Profiles },
                            icon = { Icon(Icons.Rounded.Tune, contentDescription = "Profiles") },
                            label = { Text("Profiles") }
                        )
                        NavigationBarItem(
                            selected = selectedTab == AppTab.Activity,
                            onClick = { selectedTab = AppTab.Activity },
                            icon = { Icon(Icons.Rounded.History, contentDescription = "Activity") },
                            label = { Text("Activity") }
                        )
                        NavigationBarItem(
                            selected = selectedTab == AppTab.Settings,
                            onClick = { selectedTab = AppTab.Settings },
                            icon = { Icon(Icons.Rounded.Settings, contentDescription = "Settings") },
                            label = { Text("Settings") }
                        )
                    }
                }
            ) { padding ->
                Column(modifier = Modifier.padding(padding).fillMaxSize()) {
                    LicenseBanners(
                        state = licenseState,
                        onManageLicense = { selectedTab = AppTab.License },
                        onOpenUrl = onOpenUrl,
                        buyUrl = licenseBuyUrl,
                        portalUrl = licensePortalUrl
                    )
                    when (selectedTab) {
                        AppTab.Imports -> DashboardScreen(
                            destination = DashboardDestination.Imports,
                            state = state,
                            onRefresh = viewModel::refresh,
                            onConnect = viewModel::connect,
                            onDisconnect = viewModel::disconnect,
                            onProfileSelected = viewModel::setActiveProfile,
                            onPolicyGroupSelected = viewModel::selectPolicyGroup,
                            onOpenSettings = { selectedTab = AppTab.Settings },
                            onCreateRule = viewModel::createRule,
                            onCreateRuleFromConnection = viewModel::createRuleFromConnection,
                            onCreateTemporaryRuleFromConnection = viewModel::createTemporaryRuleFromConnection,
                            onCleanupRule = viewModel::cleanupRule,
                            onProfilesImported = onProfilesImported,
                            modifier = Modifier.weight(1f)
                        )

                        AppTab.Status -> DashboardScreen(
                            destination = DashboardDestination.Status,
                            state = state,
                            onRefresh = viewModel::refresh,
                            onConnect = viewModel::connect,
                            onDisconnect = viewModel::disconnect,
                            onProfileSelected = viewModel::setActiveProfile,
                            onPolicyGroupSelected = viewModel::selectPolicyGroup,
                            onOpenSettings = { selectedTab = AppTab.Settings },
                            onCreateRule = viewModel::createRule,
                            onCreateRuleFromConnection = viewModel::createRuleFromConnection,
                            onCreateTemporaryRuleFromConnection = viewModel::createTemporaryRuleFromConnection,
                            onCleanupRule = viewModel::cleanupRule,
                            onProfilesImported = onProfilesImported,
                            modifier = Modifier.weight(1f)
                        )

                        AppTab.Profiles -> DashboardScreen(
                            destination = DashboardDestination.Profiles,
                            state = state,
                            onRefresh = viewModel::refresh,
                            onConnect = viewModel::connect,
                            onDisconnect = viewModel::disconnect,
                            onProfileSelected = viewModel::setActiveProfile,
                            onPolicyGroupSelected = viewModel::selectPolicyGroup,
                            onOpenSettings = { selectedTab = AppTab.Settings },
                            onCreateRule = viewModel::createRule,
                            onCreateRuleFromConnection = viewModel::createRuleFromConnection,
                            onCreateTemporaryRuleFromConnection = viewModel::createTemporaryRuleFromConnection,
                            onCleanupRule = viewModel::cleanupRule,
                            onProfilesImported = onProfilesImported,
                            modifier = Modifier.weight(1f)
                        )

                        AppTab.Activity -> DashboardScreen(
                            destination = DashboardDestination.Activity,
                            state = state,
                            onRefresh = viewModel::refresh,
                            onConnect = viewModel::connect,
                            onDisconnect = viewModel::disconnect,
                            onProfileSelected = viewModel::setActiveProfile,
                            onPolicyGroupSelected = viewModel::selectPolicyGroup,
                            onOpenSettings = { selectedTab = AppTab.Settings },
                            onCreateRule = viewModel::createRule,
                            onCreateRuleFromConnection = viewModel::createRuleFromConnection,
                            onCreateTemporaryRuleFromConnection = viewModel::createTemporaryRuleFromConnection,
                            onCleanupRule = viewModel::cleanupRule,
                            onProfilesImported = onProfilesImported,
                            onClearDeveloperEntries = viewModel::clearDeveloperEntries,
                            developerHar = viewModel::developerHar,
                            modifier = Modifier.weight(1f)
                        )

                        AppTab.Settings -> SettingsScreen(
                            settings = settings,
                            configToml = configToml,
                            supportPurchaseState = supportPurchaseState,
                            onSave = onSaveSettings,
                            onValidateConfig = onValidateConfig,
                            onPurchaseSupport = onPurchaseSupport,
                            onShowMessage = showMessage,
                            modifier = Modifier.weight(1f)
                        )

                        AppTab.License -> LicenseScreen(
                            state = licenseState,
                            updateState = updateState,
                            onActivate = onActivateLicense,
                            onDeactivate = onDeactivateLicense,
                            onReactivate = onReactivateLicense,
                            onTransfer = onTransferLicense,
                            onCheckUpdates = onCheckUpdates,
                            onInstallUpdate = onInstallUpdate,
                            onOpenUrl = onOpenUrl,
                            buyUrl = licenseBuyUrl,
                            portalUrl = licensePortalUrl,
                            modifier = Modifier.weight(1f)
                        )
                    }
                }
            }
        }
    }
}
