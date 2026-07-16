package com.clambhook.android

import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Drives the on-device HTTP capture detail viewer end to end: renders the
 * Activity dashboard with a fabricated capture entry, opens the detail dialog,
 * and switches through Headers / Body / JSON / Cookies tabs asserting the
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

    private fun renderActivity() {
        composeRule.setContent {
            DashboardScreen(
                destination = DashboardDestination.Activity,
                state = DashboardState(
                    developerStatus = DeveloperStatusPayload(enabled = true, captureCount = 1),
                    developerEntries = listOf(entry)
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
        composeRule.onNodeWithText("POST api.example.test", substring = true).performClick()

        // Request side is the default; header name renders in the Headers tab.
        composeRule.onNodeWithText("content-type").assertIsDisplayed()

        // Body tab shows the request body preview.
        composeRule.onNodeWithText("Body").performClick()
        composeRule.onNodeWithText("\"ok\":true", substring = true).assertIsDisplayed()

        // JSON tab pretty-prints the request body preview.
        composeRule.onNodeWithText("JSON").performClick()
        composeRule.onNodeWithText("\"id\"", substring = true).assertIsDisplayed()

        // Cookies tab renders the captured request cookie name and attributes.
        composeRule.onNodeWithText("Cookies").performClick()
        composeRule.onNodeWithText("session").assertIsDisplayed()
        composeRule.onNodeWithText("httponly", substring = true).assertIsDisplayed()

        // Switching to the Response side shows the response body preview.
        composeRule.onNodeWithText("Response").performClick()
        composeRule.onNodeWithText("Body").performClick()
        composeRule.onNodeWithText("done", substring = true).assertIsDisplayed()

        // Close returns to the card.
        composeRule.onNodeWithText("Close").performClick()
        composeRule.onNodeWithText("HTTP Capture").assertIsDisplayed()
    }
}
