// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.runtime

import com.clambhook.android.AndroidRuntimeFacade
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** Routes control requests to the in-process C runtime owned by the VPN service. */
class AndroidBackend : Backend {
    // One request at a time, matching the single-threaded native bridge.
    @OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
    private val dispatcher = Dispatchers.IO.limitedParallelism(1)

    override suspend fun request(method: String, path: String, body: String): String =
        withContext(dispatcher) {
            try {
                AndroidRuntimeFacade.request(method.ifBlank { "GET" }, path.ifBlank { "/" }, body)
            } catch (error: CancellationException) {
                throw error
            } catch (error: Throwable) {
                throw BackendException(0, error.message ?: "Android runtime request failed", error)
            }
        }

    override val displayName: String = "Android on-device runtime"

    override val supportsConnectionControl: Boolean = true

    override fun close() = Unit
}
