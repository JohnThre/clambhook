// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui

import com.clambhook.ui.runtime.DefaultRuntimeClient
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class AppControllerTest {
    /** Blocks the first status request until [gate] completes. */
    private class GatedBackend : ScriptedBackend() {
        val gate = CompletableDeferred<Unit>()

        override suspend fun request(method: String, path: String, body: String): String {
            if (path.startsWith("/api/v1/status") && statusCalls.get() == 0) {
                statusCalls.incrementAndGet()
                gate.await()
                throw com.clambhook.ui.runtime.BackendException(0, "Cannot reach the daemon")
            }
            return super.request(method, path, body)
        }
    }

    @Test
    fun aRefreshRequestedWhileOneIsInFlightIsQueuedNotDropped() = runTest {
        val backend = GatedBackend()
        val controller = AppController(DefaultRuntimeClient(backend), FakePlatformServices(), this)
        controller.refresh()
        advanceUntilIdle()
        // A user Retry arrives while the failing refresh is still running.
        controller.refresh()
        backend.gate.complete(Unit)
        advanceUntilIdle()
        assertEquals(2, backend.statusCalls.get())
        assertNull(controller.errorMessage, "the queued retry must clear the error")
    }
}
