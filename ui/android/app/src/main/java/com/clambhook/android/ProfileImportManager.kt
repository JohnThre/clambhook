package com.clambhook.android

import android.content.Context
import android.net.Uri
import com.clambhook.mobile.Mobile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.withContext
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request

/**
 * Stages and applies ClambHook profile imports. Parsing, validation, and the
 * merge/apply all run in Go (`pkg/mobile`); this manager sources the import text
 * (file, clipboard, subscription URL, QR) and drives the review → apply flow.
 */
class ProfileImportManager(context: Context) {
    private val appContext = context.applicationContext
    private val configStore = AndroidConfigStore(appContext)
    private val json = Json { ignoreUnknownKeys = true }
    private val client = OkHttpClient()

    private val _state = MutableStateFlow(ProfileImportUiState())
    val state: StateFlow<ProfileImportUiState> = _state.asStateFlow()

    /** Parses import text and stages the profile review. */
    suspend fun stageText(text: String) = withContext(Dispatchers.IO) {
        val trimmed = text.trim()
        if (trimmed.isEmpty()) {
            _state.update { it.copy(message = "Nothing to import.") }
            return@withContext
        }
        _state.update { it.copy(busy = true, message = "") }
        try {
            val review = json.decodeFromString<TunnelImportReview>(Mobile.tunnelImportReviewJSON(trimmed))
            if (review.profiles.isEmpty()) {
                _state.update { it.copy(review = null, message = "No profiles found in the import.") }
            } else {
                _state.update { it.copy(importText = trimmed, review = review, message = "") }
            }
        } catch (error: Throwable) {
            _state.update { it.copy(review = null, message = error.message ?: "Import could not be parsed.") }
        } finally {
            _state.update { it.copy(busy = false) }
        }
    }

    /** Reads a picked document and stages it. */
    suspend fun stageFromUri(uri: Uri) = withContext(Dispatchers.IO) {
        val text = try {
            appContext.contentResolver.openInputStream(uri)?.use { it.readBytes().decodeToString() }
        } catch (error: Throwable) {
            _state.update { it.copy(message = error.message ?: "Could not read the selected file.") }
            return@withContext
        }
        if (text == null) {
            _state.update { it.copy(message = "The selected file was empty.") }
            return@withContext
        }
        stageText(text)
    }

    /** Fetches a subscription URL and stages the returned config. */
    suspend fun stageFromUrl(url: String) = withContext(Dispatchers.IO) {
        val target = url.trim()
        if (!target.startsWith("http://") && !target.startsWith("https://")) {
            _state.update { it.copy(message = "Enter an http(s) subscription URL.") }
            return@withContext
        }
        _state.update { it.copy(busy = true, message = "") }
        val body = try {
            client.newCall(Request.Builder().url(target).build()).execute().use { resp ->
                if (!resp.isSuccessful) error("subscription request failed (${resp.code})")
                resp.body?.string().orEmpty()
            }
        } catch (error: Throwable) {
            _state.update { it.copy(busy = false, message = error.message ?: "Subscription fetch failed.") }
            return@withContext
        }
        _state.update { it.copy(busy = false) }
        stageText(body)
    }

    /**
     * Applies the staged review, renaming each source profile to its target name
     * and optionally activating one (identified by its source name). Returns true
     * on success.
     */
    suspend fun apply(targetNames: Map<String, String>, activateSourceName: String?): Boolean =
        withContext(Dispatchers.IO) {
            val review = _state.value.review ?: return@withContext false
            _state.update { it.copy(busy = true, message = "") }
            try {
                val path = configStore.ensureConfig()
                fun targetFor(source: String) = targetNames[source]?.trim().orEmpty().ifBlank { source }
                val request = ReviewedImportRequest(
                    importText = _state.value.importText,
                    profiles = review.profiles.map { profile ->
                        ReviewedImportProfile(sourceName = profile.name, targetName = targetFor(profile.name))
                    },
                    activateProfile = activateSourceName?.takeIf { it.isNotBlank() }?.let { targetFor(it) }.orEmpty(),
                )
                Mobile.applyReviewedTunnelImportJSON(path, json.encodeToString(request))
                val count = review.profiles.size
                _state.value = ProfileImportUiState(
                    message = "Imported $count profile${if (count == 1) "" else "s"}.",
                )
                true
            } catch (error: Throwable) {
                _state.update { it.copy(busy = false, message = error.message ?: "Import failed.") }
                false
            }
        }

    fun cancelReview() = _state.update { it.copy(review = null, importText = "", message = "") }

    fun clearMessage() = _state.update { it.copy(message = "") }
}
