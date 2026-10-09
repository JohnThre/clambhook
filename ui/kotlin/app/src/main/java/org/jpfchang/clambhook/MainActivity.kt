// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package org.jpfchang.clambhook

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import com.clambhook.ui.ClambhookApp
import com.clambhook.ui.platform.AndroidPlatformServices
import com.clambhook.ui.runtime.AndroidBackend
import com.clambhook.ui.runtime.DefaultRuntimeClient

/**
 * Hosts the shared Compose UI. The VPN service owns the single C runtime, so
 * closing or recreating this activity only detaches the UI from it.
 */
class MainActivity : ComponentActivity() {
    private val runtime by lazy { DefaultRuntimeClient(AndroidBackend()) }
    private val platformServices by lazy { AndroidPlatformServices() }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            ClambhookApp(runtime = runtime, platformServices = platformServices)
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        // ClambhookPlatformInitializer captures ss:// and ssconf:// links from
        // the resumed activity's intent; the UI consumes them on refresh.
        setIntent(intent)
    }

    override fun onDestroy() {
        if (isFinishing) {
            platformServices.close()
            runtime.close()
        }
        super.onDestroy()
    }
}
