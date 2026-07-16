package com.clambhook.android

import android.content.Context
import android.os.Build
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import com.clambhook.mobile.Mobile

/**
 * Encrypted persistence for license state. The license key, email, install ID,
 * and the cached snapshot / server grant / device-state JSON blobs all live in a
 * dedicated encrypted preferences file so credentials never touch plaintext
 * storage or cloud backup.
 */
class LicenseStorage(context: Context) {
    private val appContext = context.applicationContext
    private val prefs = EncryptedSharedPreferences.create(
        appContext,
        "clambhook_license",
        MasterKey.Builder(appContext)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build(),
        EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
        EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
    )

    var licenseKey: String
        get() = prefs.getString(KEY_LICENSE, "").orEmpty()
        set(value) = prefs.edit().putString(KEY_LICENSE, value.trim()).apply()

    var email: String
        get() = prefs.getString(KEY_EMAIL, "").orEmpty()
        set(value) = prefs.edit().putString(KEY_EMAIL, value.trim()).apply()

    var snapshotJson: String
        get() = prefs.getString(KEY_SNAPSHOT, "").orEmpty()
        set(value) = prefs.edit().putString(KEY_SNAPSHOT, value).apply()

    var grantJson: String
        get() = prefs.getString(KEY_GRANT, "").orEmpty()
        set(value) = prefs.edit().putString(KEY_GRANT, value).apply()

    var deviceStateJson: String
        get() = prefs.getString(KEY_DEVICE_STATE, "").orEmpty()
        set(value) = prefs.edit().putString(KEY_DEVICE_STATE, value).apply()

    /** Stable lowercase install ID, generated once via the Go bridge. */
    fun installId(): String {
        val existing = prefs.getString(KEY_INSTALL_ID, "").orEmpty()
        if (existing.isNotBlank()) return existing
        val generated = Mobile.newLicenseInstallID()
        prefs.edit().putString(KEY_INSTALL_ID, generated).apply()
        return generated
    }

    /** Builds the backend device registration for this install. */
    fun deviceRegistration(): LicenseDeviceRegistration {
        val versionName: String
        val versionCode: Long
        try {
            val info = appContext.packageManager.getPackageInfo(appContext.packageName, 0)
            versionName = info.versionName.orEmpty()
            versionCode = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
                info.longVersionCode
            } else {
                @Suppress("DEPRECATION") info.versionCode.toLong()
            }
        } catch (_: Throwable) {
            return LicenseDeviceRegistration(
                installId = installId(),
                displayName = Build.MODEL ?: "Android",
                platform = "android",
                architecture = androidArchitecture(),
            )
        }
        val appVersion = when {
            versionName.isBlank() -> versionCode.toString()
            else -> "$versionName ($versionCode)"
        }
        return LicenseDeviceRegistration(
            installId = installId(),
            displayName = Build.MODEL ?: "Android",
            platform = "android",
            architecture = androidArchitecture(),
            appVersion = appVersion,
        )
    }

    private companion object {
        const val KEY_LICENSE = "license_key"
        const val KEY_EMAIL = "email"
        const val KEY_INSTALL_ID = "install_id"
        const val KEY_SNAPSHOT = "snapshot_json"
        const val KEY_GRANT = "grant_json"
        const val KEY_DEVICE_STATE = "device_state_json"
    }
}

/** Maps the primary ABI to the license backend's architecture vocabulary. */
fun androidArchitecture(): String {
    val abi = Build.SUPPORTED_ABIS.firstOrNull().orEmpty()
    return when (abi) {
        "arm64-v8a" -> "arm64"
        "armeabi-v7a", "armeabi" -> "arm"
        "x86_64" -> "amd64"
        "x86" -> "386"
        else -> abi.ifBlank { "unknown" }
    }
}
