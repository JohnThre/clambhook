// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.safeDrawingPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEvent
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.isCtrlPressed
import androidx.compose.ui.input.key.isMetaPressed
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onPreviewKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.clambhook.ui.platform.PlatformServices
import com.clambhook.ui.runtime.RuntimeClient

/** Below this width the side navigation collapses into a bottom section picker. */
val CompactWidth = 760.dp

/**
 * The one responsive view hierarchy shared by Android and GNU/Linux.
 *
 * @param initialOutlineKey an `ss://`/`ssconf://` link passed on the command
 *   line or by the OS; it is only routed into review, never imported.
 */
@Composable
fun ClambhookApp(
    runtime: RuntimeClient,
    platformServices: PlatformServices,
    initialOutlineKey: String? = null,
    refreshIntervalMillis: Long = 3_000,
) {
    val scope = rememberCoroutineScope()
    val controller = remember(runtime, platformServices) {
        AppController(runtime, platformServices, scope, refreshIntervalMillis)
    }
    DisposableEffect(controller) {
        controller.start()
        initialOutlineKey?.takeIf { it.isNotBlank() }?.let(controller::showOutlineAccessKey)
        onDispose { controller.close() }
    }
    ClambhookTheme {
        AppShell(controller)
    }
}

@Composable
internal fun AppShell(controller: AppController) {
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { runCatching { focus.requestFocus() } }
    Surface(
        color = ClambhookPalette.background,
        modifier = Modifier.fillMaxSize()
            .onPreviewKeyEvent { handleShortcut(controller, it) }
            .focusRequester(focus)
            .focusable()
            .semantics { contentDescription = "ClambHook network controller" },
    ) {
        BoxWithConstraints(Modifier.fillMaxSize().safeDrawingPadding()) {
            val compact = maxWidth < CompactWidth
            Column(Modifier.fillMaxSize()) {
                ErrorBar(controller)
                Header(controller, compact)
                Row(Modifier.weight(1f).fillMaxWidth()) {
                    if (!compact) SideNavigation(controller)
                    Box(Modifier.weight(1f).fillMaxHeight()) { PageContent(controller) }
                }
                if (compact) BottomNavigation(controller)
            }
        }
    }
}

/** Ctrl/⌘+R and F5 refresh, Esc dismisses errors, Ctrl/⌘+Enter connects, Ctrl/⌘+1–9 open pages. */
internal fun handleShortcut(controller: AppController, event: KeyEvent): Boolean {
    if (event.type != KeyEventType.KeyDown) return false
    val shortcut = event.isCtrlPressed || event.isMetaPressed
    when {
        event.key == Key.F5 || (shortcut && event.key == Key.R) -> controller.refresh()
        event.key == Key.Escape && controller.errorMessage != null -> controller.clearError()
        shortcut && (event.key == Key.Enter || event.key == Key.NumPadEnter) -> controller.changeConnectionState()
        shortcut && pageForKey(event.key) != null -> controller.showPage(pageForKey(event.key)!!)
        else -> return false
    }
    return true
}

private fun pageForKey(key: Key): Page? {
    val keys = listOf(Key.One, Key.Two, Key.Three, Key.Four, Key.Five, Key.Six, Key.Seven, Key.Eight, Key.Nine)
    val index = keys.indexOf(key)
    return if (index in Page.entries.indices) Page.entries[index] else null
}

@Composable
private fun ErrorBar(controller: AppController) {
    val message = controller.errorMessage ?: return
    Row(
        modifier = Modifier.fillMaxWidth().background(ClambhookPalette.errorSurface)
            .padding(horizontal = 18.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        Text(
            message,
            color = ClambhookPalette.onErrorSurface,
            modifier = Modifier.weight(1f).semantics {
                contentDescription = "Error: $message"
                liveRegion = LiveRegionMode.Assertive
            },
        )
        TextButton(
            onClick = controller::refresh,
            modifier = Modifier.heightIn(min = MinTarget)
                .semantics { contentDescription = "Retry loading ClambHook data" },
        ) { Text("Retry", color = ClambhookPalette.onErrorSurface) }
    }
}

@Composable
private fun Header(controller: AppController, compact: Boolean) {
    Row(
        modifier = Modifier.fillMaxWidth().background(ClambhookPalette.background)
            .padding(horizontal = 18.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Text(
            controller.page.title,
            style = MaterialTheme.typography.headlineSmall,
            fontWeight = FontWeight.Bold,
            modifier = Modifier.semantics { heading() },
        )
        StatusPill(controller)
        Spacer(Modifier.weight(1f))
        if (!compact) {
            Picker(
                label = "Active profile",
                options = controller.data.profiles,
                selected = controller.data.activeProfile.ifBlank { null },
                onSelected = controller::setActiveProfile,
                modifier = Modifier.width(200.dp),
            )
        }
        SecondaryAction(
            text = when {
                controller.refreshing -> "Refreshing…"
                compact -> "↻"
                else -> "Refresh"
            },
            onClick = controller::refresh,
            enabled = !controller.refreshing,
            description = "Refresh all ClambHook data",
        )
        PrimaryAction(
            text = controller.connectLabel,
            onClick = controller::changeConnectionState,
            enabled = controller.connectEnabled,
            description = if (controller.connectionPending) controller.connectLabel
            else "${controller.connectLabel} (Control or Command plus Enter). ${controller.connectHint}",
        )
    }
}

@Composable
private fun StatusPill(controller: AppController) {
    val (background, foreground) = when (controller.statusKind) {
        AppController.StatusKind.CONNECTED -> ClambhookPalette.accentDark to ClambhookPalette.connectedText
        AppController.StatusKind.ERROR -> ClambhookPalette.errorSurface to ClambhookPalette.onErrorSurface
        else -> ClambhookPalette.neutralPill to ClambhookPalette.onNeutralPill
    }
    Text(
        controller.statusText,
        color = foreground,
        fontWeight = FontWeight.Bold,
        style = MaterialTheme.typography.labelMedium,
        modifier = Modifier.background(background, RoundedCornerShape(99.dp))
            .padding(horizontal = 10.dp, vertical = 5.dp)
            .semantics {
                stateDescription = controller.statusText
                liveRegion = LiveRegionMode.Polite
            },
    )
}

@Composable
private fun SideNavigation(controller: AppController) {
    Column(
        Modifier.width(196.dp).fillMaxHeight().background(ClambhookPalette.surface)
            .verticalScroll(rememberScrollState())
            .padding(horizontal = 10.dp, vertical = 16.dp),
        verticalArrangement = Arrangement.spacedBy(6.dp),
    ) {
        Page.entries.forEach { page ->
            val selected = page == controller.page
            TextButton(
                onClick = { controller.showPage(page) },
                modifier = Modifier.fillMaxWidth().heightIn(min = MinTarget)
                    .background(
                        if (selected) ClambhookPalette.accentDark else ClambhookPalette.surface,
                        RoundedCornerShape(10.dp),
                    )
                    .semantics {
                        contentDescription = "Open ${page.title}"
                        if (selected) stateDescription = "Current section"
                    },
            ) {
                Text(
                    page.title,
                    modifier = Modifier.fillMaxWidth(),
                    color = if (selected) ClambhookPalette.connectedText else ClambhookPalette.text,
                )
            }
        }
    }
}

@Composable
private fun BottomNavigation(controller: AppController) {
    Box(Modifier.fillMaxWidth().background(ClambhookPalette.surface).padding(horizontal = 12.dp, vertical = 8.dp)) {
        Picker(
            label = "Current section",
            options = Page.entries.map { it.name },
            selected = controller.page.name,
            onSelected = { controller.showPage(Page.valueOf(it)) },
            display = { Page.valueOf(it).title },
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun PageContent(controller: AppController) {
    when (controller.page) {
        Page.DASHBOARD -> DashboardPage(controller)
        Page.PROFILES -> ProfilesPage(controller)
        Page.ACTIVITY -> ActivityPage(controller)
        Page.ROUTING -> RoutingPage(controller)
        Page.RULES -> RulesPage(controller)
        Page.NETWORK -> NetworkPage(controller)
        Page.PROMPTS -> PromptsPage(controller)
        Page.DEVELOPER -> DeveloperPage(controller)
        Page.SETTINGS -> SettingsPage(controller)
    }
}
