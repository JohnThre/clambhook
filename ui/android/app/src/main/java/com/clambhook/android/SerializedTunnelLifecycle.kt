package com.clambhook.android

import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/**
 * Owns exactly one tunnel resource and serializes replacement/stop transitions.
 *
 * A stop waits for an in-flight start to either publish its resource or fail,
 * then closes that resource before returning. Replacing an active tunnel closes
 * the old resource first, which makes repeated starts (including profile
 * re-import/restart) safe rather than overwriting and leaking the old handle.
 */
class SerializedTunnelLifecycle<T>(
    private val close: suspend (T) -> Unit,
) {
    private val transitionLock = Mutex()
    private var active: T? = null

    /** Replaces the active resource. [activate] runs while transitions are locked. */
    suspend fun replace(
        create: suspend () -> T,
        activate: suspend (T) -> Unit = {},
    ): T = transitionLock.withLock {
        closeActive()
        val candidate = create()
        try {
            activate(candidate)
            active = candidate
            candidate
        } catch (error: Throwable) {
            close(candidate)
            throw error
        }
    }

    /** Closes the active resource, waiting for an in-flight [replace]. */
    suspend fun stop() = transitionLock.withLock { closeActive() }

    private suspend fun closeActive() {
        val resource = active ?: return
        active = null
        close(resource)
    }
}
