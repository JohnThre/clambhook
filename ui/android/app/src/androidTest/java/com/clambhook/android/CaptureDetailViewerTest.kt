// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollTo
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Drives the on-device HTTP capture detail viewer end to end: renders the
 * Activity dashboard with a fabricated capture entry, opens the detail dialog,
 * and switches through Headers / Body / Pretty / Cookies tabs asserting the
 * rendered content. Independent of the daemon capture pipeline.
 */
@RunWith(AndroidJUnit4::class)
class CaptureDetailViewerTest {
    @get:Rule
    val composeRule = createComposeRule()

    private val entry = DeveloperEntryPayload(
        id = "dev-1",
        method = "POST",
        url = "https://api.example.test/v1/items",
        scheme = "https",
        host = "api.example.test",
        status = 201,
        request = DeveloperMessagePayload(
            headers = listOf(DeveloperHeaderPayload(name = "content-type", value = "application/json")),
            cookies = listOf(
                DeveloperCookiePayload(
                    name = "session",
                    value = "[redacted]",
                    redacted = true,
                    httpOnly = true,
                    secure = true,
                    sameSite = "Lax"
                )
            ),
            body = DeveloperBodyPayload(
                size = 17,
                preview = "{\"ok\":true,\"id\":7}",
                previewBytes = 18,
                mimeType = "application/json"
            )
        ),
        response = DeveloperMessagePayload(
            body = DeveloperBodyPayload(size = 4, preview = "done", previewBytes = 4)
        )
    )

    private val decodedEntry = DeveloperEntryPayload(
        id = "dev-2",
        method = "GET",
        url = "wss://api.example.test/socket",
        scheme = "wss",
        host = "socket.example.test",
        status = 101,
        request = DeveloperMessagePayload(
            body = DeveloperBodyPayload(size = 0, preview = "", previewBytes = 0)
        ),
        response = DeveloperMessagePayload(
            body = DeveloperBodyPayload(size = 5, preview = "hello", previewBytes = 5)
        ),
        decoded = DeveloperDecodedPayload(
            kind = "websocket",
            frames = listOf(
                DeveloperDecodedFramePayload(
                    direction = "server",
                    opcode = "text",
                    preview = "hello",
                    truncated = false
                )
            )
        )
    )

    private fun renderActivity(entries: List<DeveloperEntryPayload> = listOf(entry)) {
        composeRule.setContent {
            DashboardScreen(
                destination = DashboardDestination.Activity,
                state = DashboardState(
                    developerStatus = DeveloperStatusPayload(enabled = true, captureCount = entries.size),
                    developerEntries = entries
                ),
                onRefresh = {},
                onConnect = {},
                onDisconnect = {},
                onProfileSelected = {},
                onPolicyGroupSelected = { _, _ -> },
                onOpenSettings = {},
                onCreateRule = {},
                onCreateRuleFromConnection = { _, _ -> },
                onCreateTemporaryRuleFromConnection = { _, _ -> },
                onCleanupRule = {},
                onProfilesImported = {}
            )
        }
    }

    @Test
    fun opensCaptureEntryAndInspectsHeadersBodyJsonCookies() {
        renderActivity()

        composeRule.onNodeWithText("HTTP Capture").assertIsDisplayed()

        // Open the detail dialog for the captured transaction.
        composeRule.onNodeWithText("POST api.example.test", substring = true).performScrollTo().performClick()

        // Request side is the default; header name renders in the Headers tab.
        composeRule.onNodeWithText("content-type").performScrollTo().assertIsDisplayed()
        composeRule.onNodeWithText("Repeat").assertIsDisplayed()

        // Body tab shows the request body preview.
        composeRule.onNodeWithText("Body").performScrollTo().performClick()
        composeRule.onNodeWithText("\"ok\":true", substring = true).performScrollTo().assertIsDisplayed()

        // Pretty tab formats the JSON request body preview.
        composeRule.onNodeWithText("Pretty").performScrollTo().performClick()
        composeRule.onNodeWithText("\"id\"", substring = true).performScrollTo().assertIsDisplayed()

        // Cookies tab renders the captured request cookie name and attributes.
        composeRule.onNodeWithText("Cookies").performScrollTo().performClick()
        composeRule.onNodeWithText("session").performScrollTo().assertIsDisplayed()
        composeRule.onNodeWithText("httponly", substring = true).performScrollTo().assertIsDisplayed()

        // Switching to the Response side shows the response body preview.
        composeRule.onNodeWithText("Response").performScrollTo().performClick()
        composeRule.onNodeWithText("Body").performScrollTo().performClick()
        composeRule.onNodeWithText("done", substring = true).performScrollTo().assertIsDisplayed()

        // Close returns to the card.
        composeRule.onNodeWithText("Close").performClick()
        composeRule.onNodeWithText("HTTP Capture").assertIsDisplayed()
    }

    @Test
    fun opensDecodedEntryAndInspectsDecodedFrames() {
        renderActivity(entries = listOf(decodedEntry))

        composeRule.onNodeWithText("HTTP Capture").assertIsDisplayed()

        // Open the detail dialog for the decoded (websocket) transaction.
        composeRule.onNodeWithText("GET socket.example.test", substring = true).performScrollTo().performClick()

        // The Decoded tab is present because the entry carries decoded frames.
        composeRule.onNodeWithText("Decoded").performScrollTo().performClick()

        // The decoded kind and the frame label + preview are rendered.
        composeRule.onNodeWithText("Decoded websocket", substring = true).performScrollTo().assertIsDisplayed()
        composeRule.onNodeWithText("server", substring = true).performScrollTo().assertIsDisplayed()
        composeRule.onNodeWithText("hello", substring = true).performScrollTo().assertIsDisplayed()

        composeRule.onNodeWithText("Close").performClick()
        composeRule.onNodeWithText("HTTP Capture").assertIsDisplayed()
    }

    @Test
    fun entryWithoutDecodedHasNoDecodedTab() {
        renderActivity()

        composeRule.onNodeWithText("POST api.example.test", substring = true).performScrollTo().performClick()

        // No Decoded tab is offered for a plain HTTP entry.
        composeRule.onNodeWithText("Decoded").assertDoesNotExist()
    }
}
