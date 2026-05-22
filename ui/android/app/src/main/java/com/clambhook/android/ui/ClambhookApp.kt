package com.clambhook.android

import android.os.Build
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.dynamicDarkColorScheme
import androidx.compose.material3.dynamicLightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.runtime.collectAsState
import com.clambhook.android.AppSettings
import com.clambhook.android.DashboardViewModel

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
    onSaveSettings: suspend (AppSettings, String, String) -> Unit
) {
    val context = LocalContext.current
    val colorScheme = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
        if (isSystemInDarkTheme()) dynamicDarkColorScheme(context) else dynamicLightColorScheme(context)
    } else {
        if (isSystemInDarkTheme()) androidx.compose.material3.darkColorScheme() else androidx.compose.material3.lightColorScheme()
    }
    var selectedTab by rememberSaveable { mutableStateOf(AppTab.Dashboard) }
    val state by viewModel.state.collectAsState()

    MaterialTheme(colorScheme = colorScheme) {
        Surface {
            Scaffold(
                topBar = { TopAppBar(title = { Text("clambhook") }) },
                bottomBar = {
                    NavigationBar {
                        NavigationBarItem(
                            selected = selectedTab == AppTab.Dashboard,
                            onClick = { selectedTab = AppTab.Dashboard },
                            icon = {},
                            label = { Text("Dashboard") }
                        )
                        NavigationBarItem(
                            selected = selectedTab == AppTab.Settings,
                            onClick = { selectedTab = AppTab.Settings },
                            icon = {},
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
                        modifier = Modifier.padding(padding)
                    )

                    AppTab.Settings -> SettingsScreen(
                        settings = settings,
                        token = token,
                        configToml = configToml,
                        onSave = onSaveSettings,
                        modifier = Modifier.padding(padding)
                    )
                }
            }
        }
    }
}
