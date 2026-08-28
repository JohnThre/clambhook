// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

import java.io.IOException
import java.net.Inet4Address
import java.net.Inet6Address
import java.net.InetAddress
import java.net.Proxy
import java.net.UnknownHostException
import java.util.concurrent.TimeUnit
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.put
import okhttp3.Dns
import okhttp3.HttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okio.Buffer

/**
 * Android owns HTTP transport while C owns rule-feed parsing and cache files.
 * DNS answers are checked and pinned before each refresh, redirects stay on the
 * original origin, and response bodies are bounded to the native parser limit.
 */
internal class AndroidRuleSetRefresher(
    private val systemDns: Dns = Dns.SYSTEM,
    baseClient: OkHttpClient = OkHttpClient(),
) {
    private val clientTemplate = baseClient.newBuilder()
        .proxy(Proxy.NO_PROXY)
        .followRedirects(false)
        .followSslRedirects(false)
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .callTimeout(15, TimeUnit.SECONDS)
        .build()

    fun refresh(configPath: String, profile: String, names: List<String>): RuleSetsPayload {
        val before = query(configPath, profile)
        val selectedNames = names.map { it.trim() }
        require(selectedNames.none { it.isEmpty() }) { "rule set name must not be empty" }
        val wanted = selectedNames.toSet()
        val available = before.ruleSets.associateBy { it.name }
        selectedNames.firstOrNull { it !in available }?.let {
            throw IllegalArgumentException("rule set $it not found")
        }

        val errors = mutableMapOf<String, String>()
        before.ruleSets
            .filter { wanted.isEmpty() || it.name in wanted }
            .filterNot { it.disabled || it.url.isBlank() }
            .forEach { ruleSet ->
                try {
                    refreshOne(configPath, before.profile, ruleSet)
                } catch (error: Throwable) {
                    errors[ruleSet.name] = error.message ?: "rule set refresh failed"
                }
            }
        val after = query(configPath, profile)
        if (errors.isEmpty()) return after
        return after.copy(
            statuses = after.statuses.map { status ->
                errors[status.name]?.let { status.copy(lastError = it) } ?: status
            },
        )
    }

    private fun query(configPath: String, profile: String): RuleSetsPayload =
        ApiJson.decodeFromString(
            NativeClambhookConfigBridge.query(
                configPath,
                "rule_sets",
                configProfileRequest(profile),
            ),
        )

    private fun refreshOne(configPath: String, profile: String, ruleSet: RuleSetPayload) {
        val initial = ruleSet.url.toHttpUrlOrNull()
            ?: throw IllegalArgumentException("rule set URL must be http or https with a host")
        require(initial.scheme == "http" || initial.scheme == "https") {
            "rule set URL must be http or https with a host"
        }
        require(initial.username.isEmpty() && initial.password.isEmpty()) {
            "rule set URL credentials are not allowed"
        }
        validateHost(initial.host)
        val addresses = systemDns.lookup(initial.host)
        if (addresses.isEmpty() || addresses.any { !isPublicAddress(it) }) {
            throw UnknownHostException("rule set host ${initial.host} resolves to a non-public address")
        }
        val client = clientTemplate.newBuilder()
            .dns(PinnedDns(initial.host, addresses))
            .build()
        val metadata = ApiJson.decodeFromString<NativeRuleFeedMetadata>(
            NativeClambhookRuleFeedBridge.metadata(
                configPath,
                profile,
                ruleSet.name,
                ruleSet.url,
            ),
        )
        var current = initial
        repeat(MAX_REDIRECTS + 1) { redirectCount ->
            val request = Request.Builder()
                .url(current)
                .header("User-Agent", "clambhook-android/1")
                .apply {
                    metadata.etag.takeIf { it.isNotEmpty() }?.let {
                        header("If-None-Match", it)
                    }
                    metadata.lastModified.takeIf { it.isNotEmpty() }?.let {
                        header("If-Modified-Since", it)
                    }
                }
                .build()
            client.newCall(request).execute().use { response ->
                if (response.code in REDIRECT_CODES) {
                    if (redirectCount == MAX_REDIRECTS) {
                        throw IOException("rule set stopped after ${MAX_REDIRECTS + 1} requests")
                    }
                    val location = response.header("Location")
                        ?: throw IOException("rule set redirect has no location")
                    val next = current.resolve(location)
                        ?: throw IOException("rule set redirect URL is invalid")
                    require(sameOrigin(initial, next)) {
                        "rule set redirect to a different origin is not allowed"
                    }
                    current = next
                    return@use
                }
                val fetchedTsNs = nowNanoseconds()
                if (response.code == 304) {
                    check(metadata.cached) {
                        "rule set was not modified without an existing cache"
                    }
                    NativeClambhookRuleFeedBridge.touch(
                        configPath,
                        profile,
                        ruleSet.name,
                        ruleSet.url,
                        fetchedTsNs,
                    )
                    return
                }
                if (!response.isSuccessful) {
                    throw IOException("fetch rule set: HTTP status ${response.code}")
                }
                val body = readBounded(response)
                NativeClambhookRuleFeedBridge.storeResponse(
                    configPath = configPath,
                    profile = profile,
                    name = ruleSet.name,
                    url = ruleSet.url,
                    format = ruleSet.format.ifBlank { "auto" },
                    body = body,
                    etag = response.header("ETag").orEmpty(),
                    lastModified = response.header("Last-Modified").orEmpty(),
                    fetchedTsNs = fetchedTsNs,
                )
                return
            }
        }
    }

    private fun readBounded(response: Response): ByteArray {
        val source = response.body?.source() ?: return ByteArray(0)
        val output = Buffer()
        var total = 0L
        while (true) {
            val remaining = MAX_BODY_BYTES + 1L - total
            val read = source.read(output, minOf(8192L, remaining))
            if (read == -1L) break
            total += read
            if (total > MAX_BODY_BYTES) {
                throw IOException("rule set body exceeds $MAX_BODY_BYTES bytes")
            }
        }
        return output.readByteArray()
    }

    private fun configProfileRequest(profile: String): String =
        if (profile.isBlank()) "{}" else
            ApiJson.encodeToString(
                kotlinx.serialization.json.JsonObject.serializer(),
                kotlinx.serialization.json.buildJsonObject { put("profile", profile) },
            )

    private class PinnedDns(
        private val expectedHost: String,
        private val addresses: List<InetAddress>,
    ) : Dns {
        override fun lookup(hostname: String): List<InetAddress> {
            if (!hostname.equals(expectedHost, ignoreCase = true)) {
                throw UnknownHostException("unexpected rule set host $hostname")
            }
            return addresses
        }
    }

    internal companion object {
        const val MAX_BODY_BYTES = 5L * 1024L * 1024L
        const val MAX_REDIRECTS = 10
        val REDIRECT_CODES = setOf(301, 302, 303, 307, 308)

        fun sameOrigin(first: HttpUrl, second: HttpUrl): Boolean =
            first.scheme.equals(second.scheme, ignoreCase = true) &&
                first.host.equals(second.host, ignoreCase = true) &&
                first.port == second.port

        fun validateHost(host: String) {
            val normalized = host.trimEnd('.').lowercase()
            require(
                normalized != "localhost" &&
                    !normalized.endsWith(".localhost") &&
                    normalized !in METADATA_HOSTS,
            ) { "rule set host $host is not public" }
        }

        fun isPublicAddress(address: InetAddress): Boolean {
            if (address.isAnyLocalAddress || address.isLoopbackAddress ||
                address.isLinkLocalAddress || address.isSiteLocalAddress ||
                address.isMulticastAddress) return false
            val bytes = address.address.map { it.toInt() and 0xff }
            if (address is Inet4Address && bytes.size == 4) {
                return bytes[0] != 0 && bytes[0] != 10 && bytes[0] != 127 &&
                    !(bytes[0] == 100 && bytes[1] in 64..127) &&
                    !(bytes[0] == 169 && bytes[1] == 254) &&
                    !(bytes[0] == 172 && bytes[1] in 16..31) &&
                    !(bytes[0] == 192 && bytes[1] == 168) &&
                    bytes[0] < 224
            }
            if (address is Inet6Address && bytes.size == 16) {
                return bytes[0] and 0xfe != 0xfc
            }
            return false
        }

        private val METADATA_HOSTS = setOf(
            "metadata",
            "instance-data",
            "metadata.google.internal",
            "metadata.azure.internal",
        )
    }
}

@Serializable
private data class NativeRuleFeedMetadata(
    val cached: Boolean = false,
    val etag: String = "",
    @SerialName("last_modified") val lastModified: String = "",
    @SerialName("fetched_ts_ns") val fetchedTsNs: Long = 0,
)

private fun nowNanoseconds(): Long {
    val millis = System.currentTimeMillis()
    return if (millis > Long.MAX_VALUE / 1_000_000L) Long.MAX_VALUE else
        millis * 1_000_000L
}
