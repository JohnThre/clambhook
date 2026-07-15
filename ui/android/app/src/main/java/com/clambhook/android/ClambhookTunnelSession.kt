package com.clambhook.android

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * Process-global handle to the packet-tunnel runtime owned by
 * [ClambhookVpnService]. The service runs in the app's main process, so UI code
 * reads the live runtime directly instead of round-tripping through a bound
 * service or the retired local HTTP API.
 */
object ClambhookTunnelSession {
    private val _runtime = MutableStateFlow<ClambhookTunnelRuntime?>(null)
    val runtime: StateFlow<ClambhookTunnelRuntime?> = _runtime.asStateFlow()

    @Volatile
    var configPath: String = ""
        private set

    fun publish(runtime: ClambhookTunnelRuntime, configPath: String) {
        this.configPath = configPath
        _runtime.value = runtime
    }

    fun clear() {
        _runtime.value = null
    }
}
