package com.clambhook.android

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Dashboard
import androidx.compose.material.icons.rounded.Settings
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
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
    Dashboard,
    Settings
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ClambhookApp(
    viewModel: DashboardViewModel,
    settings: AppSettings,
    token: String,
    configToml: String,
    supportPurchaseState: SupportPurchaseState,
    onSaveSettings: suspend (AppSettings, String, String) -> Unit,
    onValidateConfig: suspend (String) -> Unit,
    onPurchaseSupport: (String) -> Unit,
    onClearSupportPurchaseMessage: () -> Unit
) {
    val context = LocalContext.current
    val colorScheme = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
        if (isSystemInDarkTheme()) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
    } else {
        if (isSystemInDarkTheme()) androidx.compose.material3.darkColorScheme() else androidx.compose.material3.lightColorScheme()
    }
    var selectedTab by rememberSaveable { mutableStateOf(AppTab.Dashboard) }
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

    androidx.compose.runtime.LaunchedEffect(settings.embeddedDaemonEnabled) {
        if (
            settings.embeddedDaemonEnabled &&
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
                        }
                    )
                },
                snackbarHost = { SnackbarHost(snackbarHostState) },
                bottomBar = {
                    NavigationBar {
                        NavigationBarItem(
                            selected = selectedTab == AppTab.Dashboard,
                            onClick = { selectedTab = AppTab.Dashboard },
                            icon = { Icon(Icons.Rounded.Dashboard, contentDescription = "Dashboard") },
                            label = { Text("Dashboard") }
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
                when (selectedTab) {
                    AppTab.Dashboard -> DashboardScreen(
                        state = state,
                        onRefresh = viewModel::refresh,
                        onConnect = viewModel::connect,
                        onDisconnect = viewModel::disconnect,
                        onProfileSelected = viewModel::setActiveProfile,
                        onOpenSettings = { selectedTab = AppTab.Settings },
                        modifier = Modifier.padding(padding)
                    )

                    AppTab.Settings -> SettingsScreen(
                        settings = settings,
                        token = token,
                        configToml = configToml,
                        supportPurchaseState = supportPurchaseState,
                        onSave = onSaveSettings,
                        onValidateConfig = onValidateConfig,
                        onPurchaseSupport = onPurchaseSupport,
                        onShowMessage = showMessage,
                        modifier = Modifier.padding(padding)
                    )
                }
            }
        }
    }
}
