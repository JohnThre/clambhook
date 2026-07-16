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
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.cancel
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
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
    private var tunInterface: ParcelFileDescriptor? = null
    private var runtime: ClambhookTunnelRuntime? = null
    private var readJob: Job? = null
    private var outStream: FileOutputStream? = null

    @Volatile
    private var running = false

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopTunnel()
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf(startId)
            return START_NOT_STICKY
        }
        startForegroundNotification(getString(R.string.vpn_notification_connecting))
        scope.launch { startTunnel() }
        return START_STICKY
    }

    override fun onRevoke() {
        stopTunnel()
        stopForeground(STOP_FOREGROUND_REMOVE)
        stopSelf()
        super.onRevoke()
    }

    override fun onDestroy() {
        stopTunnel()
        scope.cancel()
        super.onDestroy()
    }

    private suspend fun startTunnel() {
        runCatching {
            val configPath = AndroidConfigStore(this).ensureConfig()
            val settings = json.decodeFromString<TunnelNetworkSettings>(
                GomobileClambhookTunnelRuntimeFactory.networkSettingsJson(configPath)
            )
            val appSettings = DataStoreSettingsStore(this).settings.first()
            val pfd = establishInterface(settings, appSettings)
                ?: error("system rejected VPN interface establishment")

            val out = FileOutputStream(pfd.fileDescriptor)
            val rt = GomobileClambhookTunnelRuntimeFactory.create(PacketWriterImpl(out, writeLock))
            rt.start(configPath)

            tunInterface = pfd
            outStream = out
            runtime = rt
            running = true
            ClambhookTunnelSession.publish(rt, configPath)
            startReadLoop(pfd, rt)
            updateNotification(getString(R.string.vpn_notification_connected))
        }.onFailure { error ->
            Log.e(logTag, "failed to start clambhook packet tunnel", error)
            updateNotification(getString(R.string.vpn_notification_error, notificationError(error)))
            stopTunnel()
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
        for (route in routes) {
            parsePrefix(route)?.let { builder.addRoute(it.first, it.second) }
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            for (route in settings.excludedRoutes) {
                parsePrefix(route)?.let { (addr, len) ->
                    runCatching { builder.excludeRoute(IpPrefix(InetAddress.getByName(addr), len)) }
                        .onFailure { Log.w(logTag, "skip excluded route $route", it) }
                }
            }
        } else if (settings.excludedRoutes.isNotEmpty()) {
            Log.w(logTag, "excluded routes require Android 13+; running full tunnel")
        }
        for (dns in settings.dnsServers) {
            builder.addDnsServer(dns)
        }

        applyPerAppRouting(builder, appSettings)

        builder.setConfigureIntent(configureIntent())
        return builder.establish()
    }

    private fun applyPerAppRouting(builder: Builder, appSettings: AppSettings) {
        val selectedPackages = appSettings.normalizedSplitTunnelPackages
            .filter { it != packageName }
            .toSortedSet()
        when (appSettings.normalizedSplitTunnelMode) {
            SplitTunnelMode.Include -> {
                if (selectedPackages.isEmpty()) {
                    Log.w(logTag, "include-only app routing selected with no apps; falling back to all apps")
                    disallowOwnPackage(builder)
                    return
                }
                selectedPackages.forEach { pkg ->
                    runCatching { builder.addAllowedApplication(pkg) }
                        .onFailure { Log.w(logTag, "skip allowed app $pkg", it) }
                }
            }
            SplitTunnelMode.Exclude -> {
                disallowOwnPackage(builder)
                selectedPackages.forEach { pkg ->
                    runCatching { builder.addDisallowedApplication(pkg) }
                        .onFailure { Log.w(logTag, "skip excluded app $pkg", it) }
                }
            }
            else -> disallowOwnPackage(builder)
        }
    }

    private fun disallowOwnPackage(builder: Builder) {
        // Keep the app's own proxy egress sockets outside the tunnel.
        runCatching { builder.addDisallowedApplication(packageName) }
            .onFailure { Log.w(logTag, "could not exclude own package from tunnel", it) }
    }

    private fun startReadLoop(pfd: ParcelFileDescriptor, rt: ClambhookTunnelRuntime) {
        readJob = scope.launch {
            val input = FileInputStream(pfd.fileDescriptor)
            val buffer = ByteArray(MAX_PACKET_SIZE)
            try {
                while (isActive && running) {
                    val n = input.read(buffer)
                    if (n < 0) break
                    if (n == 0) continue
                    runCatching { rt.injectPacket(buffer.copyOf(n)) }
                        .onFailure { Log.w(logTag, "inject packet failed", it) }
                }
            } catch (error: Throwable) {
                if (running) Log.w(logTag, "tun read loop ended", error)
            }
        }
    }

    private fun stopTunnel() {
        running = false
        ClambhookTunnelSession.clear()
        readJob?.cancel()
        readJob = null
        runCatching { runtime?.stop() }
            .onFailure { Log.w(logTag, "stop runtime failed", it) }
        runtime = null
        runCatching { outStream?.close() }
        outStream = null
        runCatching { tunInterface?.close() }
        tunInterface = null
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

    /** Splits a CIDR string ("0.0.0.0/0") into an address and a prefix length. */
    private fun parsePrefix(cidr: String): Pair<String, Int>? {
        val trimmed = cidr.trim()
        val slash = trimmed.indexOf('/')
        if (slash <= 0 || slash == trimmed.length - 1) {
            Log.w(logTag, "ignoring malformed route $cidr")
            return null
        }
        val prefix = trimmed.substring(slash + 1).toIntOrNull() ?: return null
        return trimmed.substring(0, slash) to prefix
    }

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
