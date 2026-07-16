package com.clambhook.android

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.OpenInNew
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

/**
 * License management surface: current entitlement, purchase/renew links, key
 * activation, and device-seat management. All decisions come from the shared Go
 * license domain via [LicenseManager]; this screen only renders and dispatches.
 */
@Composable
fun LicenseScreen(
    state: LicenseUiState,
    updateState: UpdateUiState,
    onActivate: (String, String) -> Unit,
    onDeactivate: () -> Unit,
    onReactivate: () -> Unit,
    onTransfer: () -> Unit,
    onCheckUpdates: () -> Unit,
    onInstallUpdate: () -> Unit,
    onOpenUrl: (String) -> Unit,
    buyUrl: String,
    portalUrl: String,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        LicenseStatusCard(state)
        PurchaseCard(state, onOpenUrl, buyUrl)
        UpdatesCard(updateState, onCheckUpdates, onInstallUpdate, onOpenUrl, buyUrl)
        ActivationCard(state, onActivate)
        DeviceSeatsCard(state, onDeactivate, onReactivate, onTransfer, onOpenUrl, portalUrl)
    }
}

private fun licenseHeadline(decision: LicenseDecision): String = when (decision.reason) {
    "trial" -> "Trial — ${decision.trialDaysRemaining} day${if (decision.trialDaysRemaining == 1) "" else "s"} left"
    "lifetime" -> "Licensed"
    "offlineGrace" -> "Licensed (offline grace)"
    else -> "Trial ended"
}

@Composable
private fun LicenseStatusCard(state: LicenseUiState) {
    Card(
        shape = RoundedCornerShape(8.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
    ) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text("License", style = MaterialTheme.typography.titleMedium)
            Text(licenseHeadline(state.decision), style = MaterialTheme.typography.headlineSmall)
            if (!state.decision.canUseApp) {
                Text(
                    "Buy or activate a ClambHook license to keep routing traffic.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            state.status.productStates.forEach { row ->
                Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    Text(
                        row.title,
                        fontWeight = FontWeight.SemiBold,
                        maxLines = 2,
                        overflow = TextOverflow.Ellipsis,
                    )
                    Text(
                        row.detail,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
    }
}

@Composable
private fun PurchaseCard(state: LicenseUiState, onOpenUrl: (String) -> Unit, buyUrl: String) {
    Card(shape = RoundedCornerShape(8.dp)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text("Buy or renew", style = MaterialTheme.typography.titleMedium)
            Text(
                "One-time USD 99.99 license includes 1 year of updates for up to 10 devices. " +
                    "Each USD 9.99 renewal adds another update year. Checkout: Creem or NOWPayments only.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Button(onClick = { onOpenUrl(buyUrl) }, modifier = Modifier.fillMaxWidth()) {
                Icon(Icons.AutoMirrored.Rounded.OpenInNew, contentDescription = null)
                Spacer(Modifier.width(8.dp))
                Text(if (state.decision.hasLifetimeUnlock) "Renew updates" else "Buy license")
            }
        }
    }
}

@Composable
private fun UpdatesCard(
    state: UpdateUiState,
    onCheck: () -> Unit,
    onInstall: () -> Unit,
    onOpenUrl: (String) -> Unit,
    buyUrl: String,
) {
    Card(shape = RoundedCornerShape(8.dp)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            Text("App updates", style = MaterialTheme.typography.titleMedium)
            val available = state.available
            when {
                available != null -> {
                    Text(
                        "Version ${available.manifest.versionName} is available.",
                        fontWeight = FontWeight.SemiBold,
                    )
                    if (available.manifest.notes.isNotBlank()) {
                        Text(
                            available.manifest.notes,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                    if (available.installable) {
                        Button(
                            onClick = onInstall,
                            enabled = !state.downloading,
                            modifier = Modifier.fillMaxWidth(),
                        ) {
                            if (state.downloading) {
                                CircularProgressIndicator(modifier = Modifier.size(18.dp), strokeWidth = 2.dp)
                                Spacer(Modifier.width(8.dp))
                                Text("Downloading")
                            } else {
                                Text("Download & install")
                            }
                        }
                    } else {
                        Text(
                            "This release is outside your update window. Renew updates to install it.",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.error,
                        )
                        Button(onClick = { onOpenUrl(buyUrl) }, modifier = Modifier.fillMaxWidth()) {
                            Text("Renew updates")
                        }
                    }
                }
                state.upToDate -> Text(
                    "ClambHook is up to date.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                else -> Text(
                    "Check clambercloud.com for a newer signed APK.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (state.message.isNotBlank()) {
                Text(
                    state.message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            OutlinedButton(
                onClick = onCheck,
                enabled = !state.checking && !state.downloading,
                modifier = Modifier.fillMaxWidth(),
            ) {
                if (state.checking) {
                    CircularProgressIndicator(modifier = Modifier.size(18.dp), strokeWidth = 2.dp)
                    Spacer(Modifier.width(8.dp))
                    Text("Checking")
                } else {
                    Text("Check for updates")
                }
            }
        }
    }
}

@Composable
private fun ActivationCard(state: LicenseUiState, onActivate: (String, String) -> Unit) {
    var key by remember { mutableStateOf("") }
    var email by remember { mutableStateOf(state.email) }
    Card(shape = RoundedCornerShape(8.dp)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Text("Activate this device", style = MaterialTheme.typography.titleMedium)
            Text(
                if (state.hasLicenseKey) {
                    "This device has a saved license key. Re-activate to refresh its seat."
                } else {
                    "Enter the license key emailed after checkout to activate this device."
                },
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            OutlinedTextField(
                value = key,
                onValueChange = { key = it },
                label = { Text("License key") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            OutlinedTextField(
                value = email,
                onValueChange = { email = it },
                label = { Text("Email (optional)") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            Button(
                onClick = { onActivate(key, email) },
                enabled = !state.loading && key.isNotBlank(),
                modifier = Modifier.fillMaxWidth(),
            ) {
                if (state.loading) {
                    CircularProgressIndicator(modifier = Modifier.size(18.dp), strokeWidth = 2.dp)
                    Spacer(Modifier.width(8.dp))
                    Text("Working")
                } else {
                    Text("Activate")
                }
            }
        }
    }
}

@Composable
private fun DeviceSeatsCard(
    state: LicenseUiState,
    onDeactivate: () -> Unit,
    onReactivate: () -> Unit,
    onTransfer: () -> Unit,
    onOpenUrl: (String) -> Unit,
    portalUrl: String,
) {
    val deviceState = state.deviceState
    Card(shape = RoundedCornerShape(8.dp)) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(
                Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text("Devices", style = MaterialTheme.typography.titleMedium)
                Text(
                    "${deviceState.activeDeviceCount} of ${deviceState.maxActiveDevices} active",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (deviceState.devices.isEmpty()) {
                Text(
                    "No registered devices yet. Activate this device to claim a seat.",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            } else {
                deviceState.devices.forEach { device ->
                    val isCurrent = device.deviceId == deviceState.currentDevice?.deviceId
                    Column(verticalArrangement = Arrangement.spacedBy(2.dp)) {
                        Text(
                            device.displayName.ifBlank { device.deviceId }.plus(if (isCurrent) " (this device)" else ""),
                            fontWeight = FontWeight.SemiBold,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                        Text(
                            listOf(
                                if (device.isActive) "active" else "deactivated",
                                device.platform,
                                device.architecture,
                            ).filter { it.isNotBlank() }.joinToString(" · "),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (deviceState.isCurrentDeviceActive) {
                    OutlinedButton(onClick = onDeactivate, enabled = !state.loading) { Text("Deactivate") }
                    OutlinedButton(onClick = onTransfer, enabled = !state.loading) { Text("Transfer") }
                } else if (deviceState.canReactivateCurrentDevice) {
                    OutlinedButton(onClick = onReactivate, enabled = !state.loading) { Text("Reactivate") }
                }
            }
            TextButton(onClick = { onOpenUrl(portalUrl) }) {
                Icon(Icons.AutoMirrored.Rounded.OpenInNew, contentDescription = null)
                Spacer(Modifier.width(8.dp))
                Text("Manage seats in portal")
            }
        }
    }
}

/** Recovery banners shown above tab content when the license needs attention. */
@Composable
fun LicenseBanners(
    state: LicenseUiState,
    onManageLicense: () -> Unit,
    onOpenUrl: (String) -> Unit,
    buyUrl: String,
    portalUrl: String,
) {
    val banners = listOfNotNull(state.status.expiredTrial, state.status.licenseExpiredForUpdates)
    if (banners.isEmpty()) return
    Column(
        Modifier
            .fillMaxWidth()
            .padding(horizontal = 16.dp)
            .padding(top = 12.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        banners.forEach { banner ->
            LicenseBanner(banner, onManageLicense, onOpenUrl, buyUrl, portalUrl)
        }
    }
}

@Composable
private fun LicenseBanner(
    banner: LicenseRecoveryState,
    onManageLicense: () -> Unit,
    onOpenUrl: (String) -> Unit,
    buyUrl: String,
    portalUrl: String,
) {
    Card(
        shape = RoundedCornerShape(8.dp),
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.errorContainer),
    ) {
        Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(
                banner.title,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.onErrorContainer,
            )
            Text(
                banner.message,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onErrorContainer,
            )
            Button(onClick = { dispatchAction(banner.primaryAction, onManageLicense, onOpenUrl, buyUrl, portalUrl) }) {
                Text(licenseActionLabel(banner.primaryAction))
            }
        }
    }
}

private fun licenseActionLabel(action: String): String = when (action) {
    "buy_license" -> "Buy license"
    "renew_updates" -> "Renew updates"
    "activate_license" -> "Activate"
    "open_license_portal" -> "Open portal"
    else -> "Manage license"
}

private fun dispatchAction(
    action: String,
    onManageLicense: () -> Unit,
    onOpenUrl: (String) -> Unit,
    buyUrl: String,
    portalUrl: String,
) {
    when (action) {
        "buy_license", "renew_updates" -> onOpenUrl(buyUrl)
        "open_license_portal" -> onOpenUrl(portalUrl)
        else -> onManageLicense()
    }
}
