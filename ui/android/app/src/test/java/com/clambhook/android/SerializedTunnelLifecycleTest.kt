package com.clambhook.android

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.joinAll
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.atomic.AtomicInteger

/**
 * Regression coverage for the tunnel start/stop lifecycle fix: an in-flight
 * start must never leak its resource (the ParcelFileDescriptor / runtime in
 * production) when a stop or a re-import-driven restart races it.
 */
class SerializedTunnelLifecycleTest {
    private class Resource(val id: Int)

    @Test
    fun stopClosesResourceOpenedByInFlightStart() = runBlocking {
        val opened = AtomicInteger()
        val closedIds = java.util.Collections.synchronizedSet(mutableSetOf<Int>())
        val lifecycle = SerializedTunnelLifecycle<Resource> { res -> closedIds.add(res.id) }
        val startEntered = CompletableDeferred<Unit>()

        val startJob = launch(Dispatchers.Default) {
            lifecycle.replace(create = {
                startEntered.complete(Unit)
                delay(50) // simulate slow establish()/rt.start()
                Resource(opened.incrementAndGet())
            })
        }

        // Request stop while the start is mid-establish.
        startEntered.await()
        lifecycle.stop()
        startJob.join()

        // The resource the start created must have been torn down, not leaked.
        assertEquals(1, opened.get())
        assertTrue(closedIds.contains(1))
    }

    @Test
    fun rapidConnectDisconnectNeverLeaks() = runBlocking {
        val opened = AtomicInteger()
        val closed = AtomicInteger()
        val lifecycle = SerializedTunnelLifecycle<Resource> { closed.incrementAndGet() }

        val jobs = (1..60).map { i ->
            launch(Dispatchers.Default) {
                if (i % 2 == 0) {
                    lifecycle.stop()
                } else {
                    lifecycle.replace(create = {
                        delay(1)
                        Resource(opened.incrementAndGet())
                    })
                }
            }
        }
        jobs.joinAll()
        lifecycle.stop()

        // Every resource opened was closed exactly once — no ParcelFileDescriptor
        // is left dangling after rapid connect/disconnect churn.
        assertEquals(opened.get(), closed.get())
    }

    @Test
    fun replaceClosesPreviousResourceBeforeInstallingNew() = runBlocking {
        val closedIds = mutableListOf<Int>()
        val lifecycle = SerializedTunnelLifecycle<Resource> { res -> closedIds.add(res.id) }

        lifecycle.replace(create = { Resource(1) })
        lifecycle.replace(create = { Resource(2) }) // profile re-import restart
        lifecycle.stop()

        assertEquals(listOf(1, 2), closedIds)
    }

    @Test
    fun activateFailureClosesResourceAndLeavesNothingActive() = runBlocking {
        val closed = AtomicInteger()
        val lifecycle = SerializedTunnelLifecycle<Resource> { closed.incrementAndGet() }

        runCatching {
            lifecycle.replace(
                create = { Resource(1) },
                activate = { error("publish failed") },
            )
        }
        // The failed start must not remain active: this stop would close a second
        // time if the candidate had been installed. It stays at one.
        lifecycle.stop()

        assertEquals(1, closed.get())
    }
}
