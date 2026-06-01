package com.clambhook.android

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.decodeFromString
import kotlinx.serialization.encodeToString
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.io.IOException

interface ClambhookApi {
    suspend fun status(): StatusPayload
    suspend fun profiles(): ProfilesPayload
    suspend fun servers(): ServersPayload
    suspend fun rules(): RulesPayload
    suspend fun traffic(): TrafficSnapshotPayload
    suspend fun connect()
    suspend fun disconnect()
    suspend fun setActiveProfile(name: String)
    suspend fun createRule(rule: RulePayload): RulesPayload
}

class ApiHttpException(
    val statusCode: Int,
    val body: String
) : IOException(if (body.isBlank()) statusCode.toString() else "$statusCode: $body")

class ClambhookApiClient(
    baseUrl: String,
    private val tokenProvider: () -> String? = { null },
    private val okHttpClient: OkHttpClient = OkHttpClient()
) : ClambhookApi {
    private val baseUrl = baseUrl.trim().trimEnd('/')
    private val jsonMediaType = "application/json".toMediaType()

    override suspend fun status(): StatusPayload =
        ApiJson.decodeFromString(send("GET", "/api/v1/status"))

    override suspend fun profiles(): ProfilesPayload =
        ApiJson.decodeFromString(send("GET", "/api/v1/profiles"))

    override suspend fun servers(): ServersPayload =
        ApiJson.decodeFromString(send("GET", "/api/v1/servers"))

    override suspend fun rules(): RulesPayload =
        ApiJson.decodeFromString(send("GET", "/api/v1/rules"))

    override suspend fun traffic(): TrafficSnapshotPayload =
        ApiJson.decodeFromString(send("GET", "/api/v1/traffic?limit=200"))

    override suspend fun connect() {
        send("POST", "/api/v1/connect")
    }

    override suspend fun disconnect() {
        send("POST", "/api/v1/disconnect")
    }

    override suspend fun setActiveProfile(name: String) {
        send("PUT", "/api/v1/profiles/active", ApiJson.encodeToString(mapOf("name" to name)))
    }

    override suspend fun createRule(rule: RulePayload): RulesPayload =
        ApiJson.decodeFromString(
            send("POST", "/api/v1/rules", ApiJson.encodeToString(CreateRuleRequest(rule)))
        )

    fun eventsUrl(): String {
        val scheme = when {
            baseUrl.startsWith("https://") -> "wss://"
            baseUrl.startsWith("http://") -> "ws://"
            else -> "ws://"
        }
        val hostAndPath = baseUrl
            .removePrefix("https://")
            .removePrefix("http://")
            .trimEnd('/')
        return "$scheme$hostAndPath/api/v1/events?types=connection.*,rule.*,hop.*,log.*"
    }

    fun eventsRequest(): Request =
        authorizedRequestBuilder(eventsUrl())
            .get()
            .build()

    fun openEventStream(
        onEvent: (DaemonEvent) -> Unit,
        onFailure: (Throwable) -> Unit
    ): WebSocket {
        return okHttpClient.newWebSocket(
            eventsRequest(),
            object : WebSocketListener() {
                override fun onMessage(webSocket: WebSocket, text: String) {
                    runCatching { ApiJson.decodeFromString<DaemonEvent>(text) }
                        .onSuccess(onEvent)
                        .onFailure(onFailure)
                }

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                    onFailure(t)
                }
            }
        )
    }

    private suspend fun send(method: String, path: String, body: String? = null): String =
        withContext(Dispatchers.IO) {
            val builder = authorizedRequestBuilder("$baseUrl$path")
            val requestBody = body?.toRequestBody(jsonMediaType)
            if (requestBody != null) {
                builder.header("Content-Type", "application/json")
            }
            when (method) {
                "GET" -> builder.get()
                "POST" -> builder.post(requestBody ?: ByteArray(0).toRequestBody(null))
                "PUT" -> builder.put(requireNotNull(requestBody))
                else -> error("unsupported method: $method")
            }
            okHttpClient.newCall(builder.build()).execute().use { response ->
                val responseBody = response.body?.string().orEmpty()
                if (!response.isSuccessful) {
                    throw ApiHttpException(response.code, responseBody.take(1024).trim())
                }
                responseBody
            }
        }

    private fun authorizedRequestBuilder(url: String): Request.Builder {
        val builder = Request.Builder().url(url)
        val token = tokenProvider()?.trim().orEmpty()
        if (token.isNotEmpty()) {
            builder.header("Authorization", "Bearer $token")
        }
        return builder
    }
}
