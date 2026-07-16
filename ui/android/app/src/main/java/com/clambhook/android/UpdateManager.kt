package com.clambhook.android

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.content.FileProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.File
import java.security.MessageDigest
import java.time.Instant

/**
 * In-app sideload updater. Polls the clambercloud.com Android update manifest,
 * compares against the installed version, gates installs through the shared
 * license update policy (renewed update window), downloads the signed APK,
 * verifies its SHA-256, and hands it to the system installer.
 *
 * @param licenseGate returns whether a release published at the given epoch-ms
 *   may be installed under the current license (delegates to [LicenseManager]).
 */
class UpdateManager(
    context: Context,
    private val licenseGate: suspend (Long) -> Boolean,
) {
    private val appContext = context.applicationContext
    private val json = Json { ignoreUnknownKeys = true }
    private val client = OkHttpClient()

    private val _state = MutableStateFlow(UpdateUiState())
    val state: StateFlow<UpdateUiState> = _state.asStateFlow()

    suspend fun check() = withContext(Dispatchers.IO) {
        _state.update { it.copy(checking = true, message = "", upToDate = false) }
        try {
            val body = client.newCall(Request.Builder().url(MANIFEST_URL).build()).execute().use { resp ->
                if (!resp.isSuccessful) {
                    // 503 means no APK/manifest is published yet.
                    _state.update { it.copy(available = null, upToDate = true, message = "No update available.") }
                    return@withContext
                }
                resp.body?.string().orEmpty()
            }
            val manifest = json.decodeFromString<AndroidUpdateManifest>(body)
            val current = currentVersionCode()
            when {
                manifest.versionCode <= current ->
                    _state.update { it.copy(available = null, upToDate = true, message = "ClambHook is up to date.") }
                Build.VERSION.SDK_INT < manifest.minSdk ->
                    _state.update {
                        it.copy(
                            available = null,
                            message = "Update ${manifest.versionName} needs a newer Android version.",
                        )
                    }
                manifest.apkUrl.isBlank() || manifest.sha256.isBlank() ->
                    _state.update { it.copy(available = null, message = "Update manifest is incomplete.") }
                else -> {
                    val millis = parsePublishedAt(manifest.publishedAt)
                    val installable = licenseGate(millis)
                    _state.update {
                        it.copy(
                            available = AvailableUpdate(manifest, millis, installable),
                            upToDate = false,
                            message = "",
                        )
                    }
                }
            }
        } catch (error: Throwable) {
            _state.update { it.copy(message = error.message ?: "Update check failed.") }
        } finally {
            _state.update { it.copy(checking = false) }
        }
    }

    suspend fun downloadAndInstall() = withContext(Dispatchers.IO) {
        val update = _state.value.available ?: return@withContext
        if (!update.installable) {
            _state.update { it.copy(message = "Renew updates to install this release.") }
            return@withContext
        }
        if (!appContext.packageManager.canRequestPackageInstalls()) {
            requestInstallPermission()
            _state.update { it.copy(message = "Allow installing unknown apps, then try again.") }
            return@withContext
        }
        _state.update { it.copy(downloading = true, message = "") }
        try {
            val apk = downloadVerified(update.manifest)
            launchInstall(apk)
            _state.update { it.copy(message = "Opening installer for ${update.manifest.versionName}." ) }
        } catch (error: Throwable) {
            _state.update { it.copy(message = error.message ?: "Update download failed.") }
        } finally {
            _state.update { it.copy(downloading = false) }
        }
    }

    fun clearMessage() = _state.update { it.copy(message = "") }

    private fun downloadVerified(manifest: AndroidUpdateManifest): File {
        val dir = File(appContext.cacheDir, "updates").apply { mkdirs() }
        val target = File(dir, "clambhook-update.apk")
        val digest = MessageDigest.getInstance("SHA-256")
        client.newCall(Request.Builder().url(manifest.apkUrl).build()).execute().use { resp ->
            if (!resp.isSuccessful) error("download failed (${resp.code})")
            val source = resp.body?.byteStream() ?: error("empty download body")
            target.outputStream().use { out ->
                val buffer = ByteArray(64 * 1024)
                while (true) {
                    val n = source.read(buffer)
                    if (n < 0) break
                    digest.update(buffer, 0, n)
                    out.write(buffer, 0, n)
                }
            }
        }
        val actual = digest.digest().joinToString("") { "%02x".format(it) }
        if (!actual.equals(manifest.sha256.trim(), ignoreCase = true)) {
            target.delete()
            error("checksum mismatch; download rejected")
        }
        return target
    }

    private fun launchInstall(apk: File) {
        val uri: Uri = FileProvider.getUriForFile(appContext, "${appContext.packageName}.updates", apk)
        val intent = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, "application/vnd.android.package-archive")
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
        }
        appContext.startActivity(intent)
    }

    private fun requestInstallPermission() {
        val intent = Intent(
            Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
            Uri.parse("package:${appContext.packageName}"),
        ).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        runCatching { appContext.startActivity(intent) }
    }

    private fun currentVersionCode(): Long = runCatching {
        appContext.packageManager.getPackageInfo(appContext.packageName, 0).longVersionCode
    }.getOrDefault(0L)

    private fun parsePublishedAt(value: String): Long =
        runCatching { Instant.parse(value.trim()).toEpochMilli() }.getOrDefault(0L)

    private companion object {
        const val MANIFEST_URL = "https://clambercloud.com/api/clambhook/android-manifest"
    }
}
