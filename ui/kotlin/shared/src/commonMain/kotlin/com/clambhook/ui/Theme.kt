// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

/** Core palette; [ClambhookPalette.contrastPairs] are verified against WCAG AA/AAA in tests. */
object ClambhookPalette {
    val background = Color(0xFF0B1018)
    val surface = Color(0xFF121A25)
    val surfaceRaised = Color(0xFF182333)
    val border = Color(0xFF2A394D)
    val text = Color(0xFFEDF4FF)
    val muted = Color(0xFFA9B8CA)
    val accent = Color(0xFF5EE4A7)
    val onAccent = Color(0xFF082016)
    val accentDark = Color(0xFF173E31)
    val connectedText = Color(0xFF8FF5C6)
    val danger = Color(0xFFFF8C8C)
    val errorSurface = Color(0xFF4A2429)
    val onErrorSurface = Color(0xFFFFD3D3)
    val warning = Color(0xFFFFD27D)
    val neutralPill = Color(0xFF2A3442)
    val onNeutralPill = Color(0xFFD5DEEB)

    /** (foreground, background, minimum contrast ratio). */
    val contrastPairs = listOf(
        Triple(text, background, 7.0),
        Triple(muted, background, 4.5),
        Triple(onAccent, accent, 4.5),
        Triple(onErrorSurface, errorSurface, 4.5),
        Triple(connectedText, accentDark, 4.5),
        Triple(onNeutralPill, neutralPill, 4.5),
    )
}

@Composable
fun ClambhookTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = darkColorScheme(
            primary = ClambhookPalette.accent,
            onPrimary = ClambhookPalette.onAccent,
            primaryContainer = ClambhookPalette.accentDark,
            onPrimaryContainer = ClambhookPalette.connectedText,
            secondary = ClambhookPalette.muted,
            background = ClambhookPalette.background,
            onBackground = ClambhookPalette.text,
            surface = ClambhookPalette.surface,
            onSurface = ClambhookPalette.text,
            surfaceVariant = ClambhookPalette.surfaceRaised,
            onSurfaceVariant = ClambhookPalette.muted,
            surfaceContainer = ClambhookPalette.surface,
            surfaceContainerHigh = ClambhookPalette.surfaceRaised,
            surfaceContainerHighest = ClambhookPalette.surfaceRaised,
            outline = ClambhookPalette.border,
            outlineVariant = ClambhookPalette.border,
            error = ClambhookPalette.danger,
            errorContainer = ClambhookPalette.errorSurface,
            onErrorContainer = ClambhookPalette.onErrorSurface,
        ),
        content = content,
    )
}
