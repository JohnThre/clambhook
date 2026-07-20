package com.clambhook.android

import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Covers the Kotlin-side license contracts that gate update installs and drive
 * the offline-grace UX. The date math and server calls live in Go; these are
 * the transport + decision semantics the app renders and enforces.
 */
class LicenseDecisionTest {
    private val json = Json { ignoreUnknownKeys = true }

    @Test
    fun lockedDecisionBlocksAppAndFeatures() {
        val decision = LicenseDecision(reason = "locked", unlockedFeatureIDs = listOf("pro"))
        assertFalse(decision.canUseApp)
        assertFalse(decision.isTrialActive)
        assertFalse(decision.isOfflineGraceActive)
        // A locked license unlocks nothing even if feature IDs linger.
        assertFalse(decision.canUseFeature("pro"))
    }

    @Test
    fun trialDecisionAllowsAppUse() {
        val decision = LicenseDecision(reason = "trial", trialDaysRemaining = 5)
        assertTrue(decision.canUseApp)
        assertTrue(decision.isTrialActive)
        assertFalse(decision.isOfflineGraceActive)
    }

    @Test
    fun offlineGraceKeepsAppUsable() {
        val decision = LicenseDecision(
            reason = "offlineGrace",
            offlineGraceEndsAt = "2026-01-01T00:00:00Z",
            unlockedFeatureIDs = listOf("pro"),
        )
        // Offline grace is the failure-tolerant state: the app stays usable and
        // previously unlocked features remain available while verification is down.
        assertTrue(decision.canUseApp)
        assertTrue(decision.isOfflineGraceActive)
        assertTrue(decision.canUseFeature("pro"))
        assertFalse(decision.canUseFeature("enterprise"))
    }

    @Test
    fun licensedDecisionCarriesUpdateCutoffForGating() {
        // The update-install gate keys off updateCutoffDate; it must round-trip
        // across the gomobile JSON boundary intact.
        val decision = json.decodeFromString(
            LicenseDecision.serializer(),
            """{"reason":"licensed","updateCutoffDate":"2027-06-01T00:00:00Z","unlockedFeatureIDs":["pro"]}""",
        )
        assertTrue(decision.canUseApp)
        assertEquals("2027-06-01T00:00:00Z", decision.updateCutoffDate)
        assertTrue(decision.canUseFeature("pro"))
    }

    @Test
    fun deviceStateComputesRemainingSeats() {
        val state = LicenseDeviceState(
            currentDeviceId = "dev-1",
            maxActiveDevices = 3,
            devices = listOf(
                LicenseDevice(deviceId = "dev-1", displayName = "Pixel", activatedAt = "t"),
                LicenseDevice(deviceId = "dev-2", displayName = "Tablet", activatedAt = "t", deactivatedAt = "t2"),
            ),
        )
        assertEquals(1, state.activeDeviceCount)
        assertEquals(2, state.remainingActivations)
        assertEquals("dev-1", state.currentDevice?.deviceId)
        assertTrue(state.isCurrentDeviceActive)
        assertTrue(state.canTransferCurrentDevice)
    }

    @Test
    fun deviceStateAllowsReactivationWhenSeatsFree() {
        val state = LicenseDeviceState(
            currentDeviceId = "dev-1",
            maxActiveDevices = 2,
            devices = listOf(
                LicenseDevice(deviceId = "dev-1", displayName = "Pixel", activatedAt = "t", deactivatedAt = "t2"),
            ),
        )
        assertFalse(state.isCurrentDeviceActive)
        assertTrue(state.canReactivateCurrentDevice)
        assertFalse(state.canTransferCurrentDevice)
    }
}
