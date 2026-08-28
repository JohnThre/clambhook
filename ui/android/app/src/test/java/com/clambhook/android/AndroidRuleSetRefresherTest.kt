// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

import java.net.InetAddress
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

class AndroidRuleSetRefresherTest {
    @Test
    fun publicAddressPolicyRejectsLocalAndPrivateRanges() {
        assertTrue(AndroidRuleSetRefresher.isPublicAddress(InetAddress.getByName("8.8.8.8")))
        assertFalse(AndroidRuleSetRefresher.isPublicAddress(InetAddress.getByName("127.0.0.1")))
        assertFalse(AndroidRuleSetRefresher.isPublicAddress(InetAddress.getByName("10.0.0.1")))
        assertFalse(AndroidRuleSetRefresher.isPublicAddress(InetAddress.getByName("100.64.0.1")))
        assertFalse(AndroidRuleSetRefresher.isPublicAddress(InetAddress.getByName("169.254.1.1")))
        assertFalse(AndroidRuleSetRefresher.isPublicAddress(InetAddress.getByName("fc00::1")))
    }

    @Test
    fun metadataAndLocalhostNamesAreRejected() {
        assertThrows(IllegalArgumentException::class.java) {
            AndroidRuleSetRefresher.validateHost("metadata.google.internal")
        }
        assertThrows(IllegalArgumentException::class.java) {
            AndroidRuleSetRefresher.validateHost("service.localhost")
        }
    }

    @Test
    fun redirectsMustRemainOnTheOriginalOrigin() {
        val original = "https://rules.example:8443/list".toHttpUrl()
        assertTrue(
            AndroidRuleSetRefresher.sameOrigin(
                original,
                "https://rules.example:8443/next".toHttpUrl(),
            ),
        )
        assertFalse(
            AndroidRuleSetRefresher.sameOrigin(
                original,
                "https://cdn.example:8443/next".toHttpUrl(),
            ),
        )
        assertFalse(
            AndroidRuleSetRefresher.sameOrigin(
                original,
                "http://rules.example:8443/next".toHttpUrl(),
            ),
        )
    }
}
