// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.ExperimentalTestApi
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.assertHeightIsAtLeast
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.hasContentDescription
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onAllNodesWithContentDescription
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performKeyInput
import androidx.compose.ui.test.pressKey
import androidx.compose.ui.test.runComposeUiTest
import androidx.compose.ui.test.withKeyDown
import androidx.compose.ui.unit.dp
import com.clambhook.ui.runtime.DefaultRuntimeClient
import kotlinx.coroutines.CompletableDeferred
import kotlin.math.max
import kotlin.math.min
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

@OptIn(ExperimentalTestApi::class)
class AppUiTest {
    private val longInterval = 60_000L

    @Test
    fun navigationIsResponsiveKeyboardAccessibleAndTouchSized() = runComposeUiTest {
        val backend = ScriptedBackend()
        setContent {
            Box(Modifier.size(1180.dp, 760.dp)) {
                ClambhookApp(DefaultRuntimeClient(backend), FakePlatformServices(), refreshIntervalMillis = longInterval)
            }
        }
        waitUntil(timeoutMillis = 5_000) { onAllNodesWithContentDescription("Open Dashboard").fetchSemanticsNodes().isNotEmpty() }
        Page.entries.forEach { onNodeWithContentDescription("Open ${it.title}").assertIsDisplayed() }
        onNodeWithContentDescription("ClambHook network controller").assertExists()
        waitUntil(timeoutMillis = 5_000) { onAllNodesWithText("Ready to connect").fetchSemanticsNodes().isNotEmpty() }
        onNodeWithText("Active profile · default").assertExists()
        onNodeWithContentDescription("Refresh all ClambHook data").assertHeightIsAtLeast(48.dp)
        onNodeWithContentDescription("Open Settings").assertHeightIsAtLeast(48.dp)

        onNodeWithContentDescription("ClambHook network controller").performKeyInput {
            withKeyDown(Key.CtrlLeft) { pressKey(Key.Nine) }
        }
        waitForIdle()
        onNode(hasText("Settings") and SemanticsMatcher.keyIsDefined(SemanticsProperties.Heading)).assertExists()
        onNodeWithContentDescription("Creem payment provider (opens in browser)").assertExists()
        onNodeWithContentDescription("NOWPayments payment provider (opens in browser)").assertExists()
    }

    @Test
    fun compactWidthSwapsSideNavigationForASectionPicker() = runComposeUiTest {
        setContent {
            Box(Modifier.size(600.dp, 760.dp)) {
                ClambhookApp(DefaultRuntimeClient(ScriptedBackend()), FakePlatformServices(), refreshIntervalMillis = longInterval)
            }
        }
        waitForIdle()
        onNodeWithContentDescription("Current section").assertExists()
        assertTrue(onAllNodesWithContentDescription("Open Dashboard").fetchSemanticsNodes().isEmpty())
    }

    @Test
    fun missingProfileDisablesConnectAndOffersASetupRoute() = runComposeUiTest {
        val backend = ScriptedBackend(activeProfile = "")
        setContent {
            ClambhookApp(DefaultRuntimeClient(backend), FakePlatformServices(), refreshIntervalMillis = longInterval)
        }
        waitUntil(timeoutMillis = 5_000) { onAllNodesWithText("Add a profile").fetchSemanticsNodes().isNotEmpty() }
        onNode(hasText("Connect") and hasContentDescription("Add and select a profile before connecting", substring = true))
            .assertIsNotEnabled()
        onNodeWithContentDescription("Open Profiles to add a profile").performClick()
        waitForIdle()
        onNode(hasText("Profiles") and SemanticsMatcher.keyIsDefined(SemanticsProperties.Heading)).assertExists()
    }

    @Test
    fun refreshCannotReenableConnectWhileConnectionActionIsPending() = runComposeUiTest {
        val platform = FakePlatformServices().apply { pendingStart = CompletableDeferred() }
        setContent {
            ClambhookApp(DefaultRuntimeClient(ScriptedBackend()), platform, refreshIntervalMillis = longInterval)
        }
        waitUntil(timeoutMillis = 5_000) { onAllNodesWithText("Ready to connect").fetchSemanticsNodes().isNotEmpty() }
        onNodeWithText("Connect").assertIsEnabled().performClick()
        waitForIdle()
        onNodeWithText("Connecting…").assertIsNotEnabled()
        onNodeWithContentDescription("Refresh all ClambHook data").performClick()
        waitForIdle()
        onNodeWithText("Connecting…").assertIsNotEnabled()
        platform.pendingStart!!.complete(Unit)
        waitUntil(timeoutMillis = 5_000) { onAllNodesWithText("Connect").fetchSemanticsNodes().isNotEmpty() }
        onNodeWithText("Connect").assertIsEnabled()
    }

    @Test
    fun failedRefreshIsAnnouncedAndAUserRetryClearsIt() = runComposeUiTest {
        val backend = ScriptedBackend(failStatus = true)
        setContent {
            ClambhookApp(DefaultRuntimeClient(backend), FakePlatformServices(), refreshIntervalMillis = longInterval)
        }
        waitUntil(timeoutMillis = 5_000) {
            onAllNodesWithContentDescription("Error: Cannot reach the daemon").fetchSemanticsNodes().isNotEmpty()
        }
        backend.failStatus = false
        onNodeWithContentDescription("Retry loading ClambHook data").assertHeightIsAtLeast(48.dp).performClick()
        waitUntil(timeoutMillis = 5_000) {
            onAllNodesWithContentDescription("Retry loading ClambHook data").fetchSemanticsNodes().isEmpty()
        }
        assertTrue(backend.statusCalls.get() >= 2)
    }

    @Test
    fun externalOutlineLinksAreOnlyRoutedIntoReview() = runComposeUiTest {
        val backend = ScriptedBackend()
        setContent {
            ClambhookApp(
                DefaultRuntimeClient(backend),
                FakePlatformServices(),
                initialOutlineKey = "ss://example-key",
                refreshIntervalMillis = longInterval,
            )
        }
        waitForIdle()
        onNode(hasText("Profiles") and SemanticsMatcher.keyIsDefined(SemanticsProperties.Heading)).assertExists()
        onNodeWithText("ss://example-key").assertExists()
        assertFalse(synchronized(backend.calls) { backend.calls.any { it.contains("/outline/import") } })
    }

    @Test
    fun paymentAndDonationDestinationsAreExact() {
        assertEquals(
            mapOf(
                "Ko-fi" to "https://ko-fi.com/jpfchang",
                "Liberapay" to "https://en.liberapay.com/jpfchang/",
                "IssueHunt" to "https://oss.issuehunt.io/u/johnthre",
                "Donate crypto" to "https://nowpayments.io/donation?api_key=4f798f1e-c93e-456e-8067-b03b200790cd",
            ),
            AppController.DONATION_URLS,
        )
        assertEquals(
            mapOf("Creem" to "https://creem.io/", "NOWPayments" to "https://nowpayments.io/"),
            AppController.PAYMENT_PROVIDER_URLS,
        )
        assertFalse("PayPal" in AppController.PAYMENT_PROVIDER_URLS)
        assertEquals("Card via Creem · Cryptocurrency via NOWPayments", AppController.PAYMENT_PROVIDER_TRUST_SUMMARY)
    }

    @Test
    fun corePaletteMeetsWcagContrastThresholds() {
        ClambhookPalette.contrastPairs.forEach { (foreground, background, minimum) ->
            assertTrue(contrast(foreground, background) >= minimum, "$foreground on $background")
        }
    }

    private fun contrast(first: Color, second: Color): Double {
        val a = first.luminance().toDouble()
        val b = second.luminance().toDouble()
        return (max(a, b) + 0.05) / (min(a, b) + 0.05)
    }
}
