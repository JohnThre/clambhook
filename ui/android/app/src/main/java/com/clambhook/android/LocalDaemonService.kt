package com.clambhook.android

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.IBinder
import android.util.Log
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch

class LocalDaemonService : Service() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                scope.launch {
                    stopRuntime()
                    stopForeground(STOP_FOREGROUND_REMOVE)
                    stopSelf(startId)
                }
                return START_NOT_STICKY
            }

            ACTION_RESTART -> {
                startForeground(notificationId, notification())
                scope.launch {
                    stopRuntime()
                    startRuntime()
                }
                return START_STICKY
            }

            ACTION_RELOAD -> {
                startForeground(notificationId, notification())
                scope.launch { reloadRuntime() }
                return START_STICKY
            }

            else -> {
                startForeground(notificationId, notification())
                scope.launch { startRuntime() }
                return START_STICKY
            }
        }
    }

    override fun onDestroy() {
        stopRuntime()
        scope.cancel()
        super.onDestroy()
    }

    private suspend fun startRuntime() {
        runCatching {
            val configPath = AndroidConfigStore(this).ensureConfig()
            val token = EncryptedTokenStore(this).currentToken()
            GomobileClambhookRuntime.start(configPath, defaultAndroidApiListenAddress, token)
        }.onFailure { error ->
            Log.e(logTag, "failed to start embedded clambhook runtime", error)
        }
    }

    private suspend fun reloadRuntime() {
        runCatching {
            val configPath = AndroidConfigStore(this).ensureConfig()
            if (GomobileClambhookRuntime.isRunning()) {
                GomobileClambhookRuntime.reload(configPath)
            } else {
                startRuntime()
            }
        }.onFailure { error ->
            Log.e(logTag, "failed to reload embedded clambhook runtime", error)
        }
    }

    private fun stopRuntime() {
        runCatching { GomobileClambhookRuntime.stop() }
            .onFailure { error -> Log.e(logTag, "failed to stop embedded clambhook runtime", error) }
    }

    private fun notification(): Notification {
        ensureNotificationChannel()
        val contentIntent = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        val stopIntent = PendingIntent.getService(
            this,
            1,
            Intent(this, LocalDaemonService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        return Notification.Builder(this, notificationChannelId)
            .setContentTitle(getString(R.string.daemon_notification_title))
            .setContentText(getString(R.string.daemon_notification_text))
            .setSmallIcon(R.drawable.ic_stat_clambhook)
            .setContentIntent(contentIntent)
            .setOngoing(true)
            .addAction(
                Notification.Action.Builder(
                    R.drawable.ic_stat_clambhook,
                    getString(R.string.daemon_notification_stop),
                    stopIntent
                ).build()
            )
            .build()
    }

    private fun ensureNotificationChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) {
            return
        }
        val manager = getSystemService(NotificationManager::class.java)
        if (manager.getNotificationChannel(notificationChannelId) != null) {
            return
        }
        manager.createNotificationChannel(
            NotificationChannel(
                notificationChannelId,
                getString(R.string.daemon_notification_channel),
                NotificationManager.IMPORTANCE_LOW
            )
        )
    }

    companion object {
        private const val ACTION_RELOAD = "com.clambhook.android.action.RELOAD_DAEMON"
        private const val ACTION_RESTART = "com.clambhook.android.action.RESTART_DAEMON"
        private const val ACTION_STOP = "com.clambhook.android.action.STOP_DAEMON"
        private const val notificationChannelId = "clambhook_daemon"
        private const val notificationId = 1001
        private const val logTag = "LocalDaemonService"

        fun start(context: Context) {
            val intent = Intent(context, LocalDaemonService::class.java)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                context.startForegroundService(intent)
            } else {
                context.startService(intent)
            }
        }

        fun reload(context: Context) {
            context.startService(Intent(context, LocalDaemonService::class.java).setAction(ACTION_RELOAD))
        }

        fun restart(context: Context) {
            val intent = Intent(context, LocalDaemonService::class.java).setAction(ACTION_RESTART)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                context.startForegroundService(intent)
            } else {
                context.startService(intent)
            }
        }

        fun stop(context: Context) {
            context.startService(Intent(context, LocalDaemonService::class.java).setAction(ACTION_STOP))
        }
    }
}
