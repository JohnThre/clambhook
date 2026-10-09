// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.desktop

import androidx.compose.ui.unit.DpSize
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Window
import androidx.compose.ui.window.application
import androidx.compose.ui.window.rememberWindowState
import com.clambhook.ui.ClambhookApp
import com.clambhook.ui.platform.DesktopPlatformServices
import com.clambhook.ui.runtime.DesktopRuntime
import java.awt.Dimension

/** GNU/Linux (Ubuntu and Fedora) entry point, installed as `clambhook-ui`. */
fun main(args: Array<String>) {
    val outlineKey = args.firstOrNull {
        it.startsWith("ss://", ignoreCase = true) || it.startsWith("ssconf://", ignoreCase = true)
    }
    val runtime = DesktopRuntime.create()
    val platformServices = DesktopPlatformServices(runtime)
    try {
        application {
            Window(
                onCloseRequest = ::exitApplication,
                title = "ClambHook",
                state = rememberWindowState(size = DpSize(1180.dp, 760.dp)),
            ) {
                window.minimumSize = Dimension(390, 620)
                ClambhookApp(runtime, platformServices, initialOutlineKey = outlineKey)
            }
        }
    } finally {
        platformServices.close()
        runtime.close()
    }
}
