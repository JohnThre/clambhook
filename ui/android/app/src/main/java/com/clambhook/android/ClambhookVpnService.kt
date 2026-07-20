package com.clambhook.android

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.graphics.drawable.Icon
import android.net.IpPrefix
import android.net.VpnService
import android.os.Build
import android.os.ParcelFileDescriptor
import android.util.Log
import com.clambhook.mobile.PacketWriter
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.isActive
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.io.FileInputStream
import java.io.FileOutputStream
import java.net.InetAddress

/**
 * ClambhookVpnService bridges the Android system TUN interface to the embedded
 * clambhook packet-tunnel runtime (`pkg/mobile.TunnelRuntime`).
 *
 * Inbound device packets are read from the TUN fd and injected into the Go
 * userspace packet stack; the runtime writes routed packets back through the
 * [PacketWriter] onto the same fd. The app's own package is excluded from the
 * tunnel via [VpnService.Builder.addDisallowedApplication] so the runtime's
 * outbound proxy sockets bypass the VPN instead of looping back into it.
 */
class ClambhookVpnService : VpnService() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val json = Json { ignoreUnknownKeys = true; coerceInputValues = true }
    private val writeLock = Any()
    private val lifecycle = SerializedTunnelLifecycle<TunnelResources>(::closeTunnelResources)
    private var transitionJob: Job? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            enqueueTransition {
                stopTunnel()
                stopForeground(STOP_FOREGROUND_REMOVE)
                stopSelf(startId)
            }
            return START_NOT_STICKY
        }
        startForegroundNotification(getString(R.string.vpn_notification_connecting))
        enqueueTransition { startTunnel() }
        return START_STICKY
    }

    override fun onRevoke() {
        enqueueTransition {
            stopTunnel()
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
        }
        super.onRevoke()
    }

    override fun onDestroy() {
        runBlocking {
            transitionJob?.join()
            stopTunnel()
        }
        scope.cancel()
        super.onDestroy()
    }

    private fun enqueueTransition(block: suspend () -> Unit) {
        val previous = transitionJob
        transitionJob = scope.launch {
            previous?.join()
            block()
        }
    }

    private suspend fun startTunnel() {
        try {
            lifecycle.replace(
                create = ::createTunnelResources,
                activate = { resources ->
                    ClambhookTunnelSession.publish(resources.runtime, resources.configPath)
                    resources.readJob = startReadLoop(resources.pfd, resources.runtime)
                    updateNotification(
                        getString(
                            if (resources.compatRouting) R.string.vpn_notification_connected_compat
                            else R.string.vpn_notification_connected
                        )
                    )
                },
            )
        } catch (error: Throwable) {
            Log.e(logTag, "failed to start clambhook packet tunnel", error)
            updateNotification(getString(R.string.vpn_notification_error, notificationError(error)))
            lifecycle.stop()
        }
    }

    private suspend fun createTunnelResources(): TunnelResources {
        var pfd: ParcelFileDescriptor? = null
        var out: FileOutputStream? = null
        var rt: ClambhookTunnelRuntime? = null
        try {
            val configPath = AndroidConfigStore(this).ensureConfig()
            val settings = json.decodeFromString<TunnelNetworkSettings>(
                GomobileClambhookTunnelRuntimeFactory.networkSettingsJson(configPath)
            )
            val appSettings = DataStoreSettingsStore(this).settings.first()
            pfd = establishInterface(settings, appSettings)
                ?: error("system rejected VPN interface establishment")
            out = FileOutputStream(pfd.fileDescriptor)
            rt = GomobileClambhookTunnelRuntimeFactory.create(PacketWriterImpl(out, writeLock))
            rt.start(configPath)
            val compatRouting = Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU &&
                settings.excludedRoutes.isNotEmpty()
            return TunnelResources(pfd, out, rt, configPath, compatRouting)
        } catch (error: Throwable) {
            runCatching { rt?.stop() }
            synchronized(writeLock) { runCatching { out?.close() } }
            runCatching { pfd?.close() }
            throw error
        }
    }

    private fun establishInterface(settings: TunnelNetworkSettings, appSettings: AppSettings): ParcelFileDescriptor? {
        val builder = Builder()
            .setSession(getString(R.string.app_name))
            .setMtu(if (settings.mtu > 0) settings.mtu else DEFAULT_MTU)
            .setBlocking(true)

        for (address in settings.ipv4 + settings.ipv6) {
            builder.addAddress(address.address, address.prefixLen)
        }
        val routes = settings.includedRoutes.ifEmpty { DEFAULT_ROUTES }
        val effectiveRoutes = if (
            Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU && settings.excludedRoutes.isNotEmpty()
        ) {
            inverseRoutes(routes, settings.excludedRoutes)
        } else {
            routes.mapNotNull(::parseRoutePrefix)
        }
        for (route in effectiveRoutes) {
            runCatching { builder.addRoute(route.address, route.prefixLength) }
                .onFailure { Log.w(logTag, "skip included route $route", it) }
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            for (route in settings.excludedRoutes) {
                parseRoutePrefix(route)?.let { parsed ->
                    runCatching {
                        builder.excludeRoute(
                            IpPrefix(InetAddress.getByName(parsed.address), parsed.prefixLength)
                        )
                    }.onFailure { Log.w(logTag, "skip excluded route $route", it) }
                }
            }
        }
        for (dns in settings.dnsServers) {
            builder.addDnsServer(dns)
        }

        applyPerAppRouting(builder, appSettings)

        builder.setConfigureIntent(configureIntent())
        return builder.establish()
    }

    private fun applyPerAppRouting(builder: Builder, appSettings: AppSettings) {
        when (
            val plan = resolveSplitTunnel(
                appSettings.normalizedSplitTunnelMode,
                appSettings.normalizedSplitTunnelPackages,
                packageName,
            )
        ) {
            is SplitTunnelPlan.AllowOnly -> plan.packages.forEach { pkg ->
                runCatching { builder.addAllowedApplication(pkg) }
                    .onFailure { Log.w(logTag, "skip allowed app $pkg", it) }
            }
            is SplitTunnelPlan.DisallowOwnAnd -> {
                disallowOwnPackage(builder)
                plan.packages.forEach { pkg ->
                    runCatching { builder.addDisallowedApplication(pkg) }
                        .onFailure { Log.w(logTag, "skip excluded app $pkg", it) }
                }
            }
            SplitTunnelPlan.DisallowOwnOnly -> {
                if (
                    appSettings.normalizedSplitTunnelMode == SplitTunnelMode.Include &&
                    appSettings.normalizedSplitTunnelPackages.isEmpty()
                ) {
                    Log.w(logTag, "include-only app routing selected with no apps; falling back to all apps")
                }
                disallowOwnPackage(builder)
            }
        }
    }

    private fun disallowOwnPackage(builder: Builder) {
        // Keep the app's own proxy egress sockets outside the tunnel.
        runCatching { builder.addDisallowedApplication(packageName) }
            .onFailure { Log.w(logTag, "could not exclude own package from tunnel", it) }
    }

    private fun startReadLoop(pfd: ParcelFileDescriptor, rt: ClambhookTunnelRuntime): Job =
        scope.launch {
            val input = FileInputStream(pfd.fileDescriptor)
            val buffer = ByteArray(MAX_PACKET_SIZE)
            try {
                while (isActive) {
                    val n = input.read(buffer)
                    if (n < 0) break
                    if (n == 0) continue
                    runCatching { rt.injectPacket(buffer.copyOf(n)) }
                        .onFailure { Log.w(logTag, "inject packet failed", it) }
                }
            } catch (error: Throwable) {
                if (isActive) Log.w(logTag, "tun read loop ended", error)
            }
        }

    private suspend fun stopTunnel() {
        lifecycle.stop()
    }

    private fun closeTunnelResources(resources: TunnelResources) {
        ClambhookTunnelSession.clear()
        resources.readJob?.cancel()
        resources.readJob = null
        runCatching { resources.runtime.stop() }
            .onFailure { Log.w(logTag, "stop runtime failed", it) }
        synchronized(writeLock) { runCatching { resources.out.close() } }
        runCatching { resources.pfd.close() }
    }

    private fun startForegroundNotification(contentText: String) {
        val notification = notification(contentText)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(notificationId, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        } else {
            startForeground(notificationId, notification)
        }
    }

    private fun updateNotification(contentText: String) {
        getSystemService(NotificationManager::class.java)
            .notify(notificationId, notification(contentText))
    }

    private fun configureIntent(): PendingIntent = PendingIntent.getActivity(
        this,
        0,
        Intent(this, MainActivity::class.java),
        PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
    )

    private fun notification(contentText: String): Notification {
        ensureNotificationChannel()
        val stopIntent = PendingIntent.getService(
            this,
            1,
            Intent(this, ClambhookVpnService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        return Notification.Builder(this, notificationChannelId)
            .setContentTitle(getString(R.string.vpn_notification_title))
            .setContentText(contentText)
            .setSmallIcon(R.drawable.ic_stat_clambhook)
            .setContentIntent(configureIntent())
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .setShowWhen(false)
            .setLocalOnly(true)
            .setCategory(Notification.CATEGORY_SERVICE)
            .addAction(
                Notification.Action.Builder(
                    Icon.createWithResource(this, R.drawable.ic_stat_clambhook),
                    getString(R.string.vpn_notification_stop),
                    stopIntent
                ).build()
            )
            .build()
    }

    private fun ensureNotificationChannel() {
        val manager = getSystemService(NotificationManager::class.java)
        if (manager.getNotificationChannel(notificationChannelId) != null) return
        manager.createNotificationChannel(
            NotificationChannel(
                notificationChannelId,
                getString(R.string.vpn_notification_channel),
                NotificationManager.IMPORTANCE_LOW
            )
        )
    }

    private fun notificationError(error: Throwable): String =
        (error.message ?: error.toString()).lineSequence().firstOrNull().orEmpty().take(96)


    private class TunnelResources(
        val pfd: ParcelFileDescriptor,
        val out: FileOutputStream,
        val runtime: ClambhookTunnelRuntime,
        val configPath: String,
        val compatRouting: Boolean,
        var readJob: Job? = null,
    )

    private class PacketWriterImpl(
        private val out: FileOutputStream,
        private val lock: Any,
    ) : PacketWriter {
        override fun writePacket(packet: ByteArray) {
            synchronized(lock) { out.write(packet) }
        }
    }

    companion object {
        const val ACTION_STOP = "com.clambhook.android.action.STOP_VPN"

        private const val notificationChannelId = "clambhook_vpn"
        private const val notificationId = 1002
        private const val logTag = "ClambhookVpnService"
        private const val DEFAULT_MTU = 1500
        private const val MAX_PACKET_SIZE = 32767
        private val DEFAULT_ROUTES = listOf("0.0.0.0/0", "::/0")

        fun start(context: Context) {
            context.startForegroundService(Intent(context, ClambhookVpnService::class.java))
        }

        fun stop(context: Context) {
            context.startService(
                Intent(context, ClambhookVpnService::class.java).setAction(ACTION_STOP)
            )
        }
    }
}

/** Mirrors `pkg/mobile.tunnelNetworkSettings` emitted by `Mobile.tunnelNetworkSettingsJSON`. */
@Serializable
data class TunnelNetworkSettings(
    val mtu: Int = 0,
    val remote_address: String = "",
    val ipv4: List<TunnelIpPrefix> = emptyList(),
    val ipv6: List<TunnelIpPrefix> = emptyList(),
    val dns_servers: List<String> = emptyList(),
    val included_routes: List<String> = emptyList(),
    val excluded_routes: List<String> = emptyList(),
) {
    val dnsServers: List<String> get() = dns_servers
    val includedRoutes: List<String> get() = included_routes
    val excludedRoutes: List<String> get() = excluded_routes
}

@Serializable
data class TunnelIpPrefix(
    val address: String = "",
    val prefix_len: Int = 0,
) {
    val prefixLen: Int get() = prefix_len
}
