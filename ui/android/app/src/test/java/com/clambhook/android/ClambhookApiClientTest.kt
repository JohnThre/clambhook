package com.clambhook.android

import kotlinx.coroutines.runBlocking
import okhttp3.Interceptor
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import okio.Buffer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test

class ClambhookApiClientTest {
    @Test
    fun statusSendsBearerTokenAndDecodesResponse() = runBlocking {
        val interceptor = CapturingInterceptor("""{"running":true,"profile":"A","listeners":[]}""")
        val client = ClambhookApiClient(
            baseUrl = "http://127.0.0.1:9090/",
            tokenProvider = { "secret-token" },
            okHttpClient = OkHttpClient.Builder().addInterceptor(interceptor).build()
        )

        val status = client.status()

        val request = interceptor.requests.single()
        assertEquals("/api/v1/status", request.url.encodedPath)
        assertEquals("Bearer secret-token", request.header("Authorization"))
        assertEquals(true, status.running)
        assertEquals("A", status.profile)
    }

    @Test
    fun setActiveProfileSendsPutBody() = runBlocking {
        val interceptor = CapturingInterceptor("", statusCode = 204)
        val client = ClambhookApiClient(
            baseUrl = "http://127.0.0.1:9090",
            okHttpClient = OkHttpClient.Builder().addInterceptor(interceptor).build()
        )

        client.setActiveProfile("B")

        val request = interceptor.requests.single()
        assertEquals("PUT", request.method)
        assertEquals("/api/v1/profiles/active", request.url.encodedPath)
        assertEquals("application/json", request.header("Content-Type"))
        assertEquals("""{"name":"B"}""", requireNotNull(request.body).bodyToString())
    }

    @Test
    fun httpErrorsPreserveStatusAndBody() = runBlocking {
        val interceptor = CapturingInterceptor("unauthorized\n", statusCode = 401)
        val client = ClambhookApiClient(
            baseUrl = "http://127.0.0.1:9090",
            okHttpClient = OkHttpClient.Builder().addInterceptor(interceptor).build()
        )

        try {
            client.status()
            fail("expected ApiHttpException")
        } catch (error: ApiHttpException) {
            assertEquals(401, error.statusCode)
            assertEquals("unauthorized", error.body)
        }
    }

    @Test
    fun eventsRequestUsesWebSocketSchemeAndBearerToken() {
        val httpClient = ClambhookApiClient(
            baseUrl = "http://127.0.0.1:9090/",
            tokenProvider = { "secret-token" }
        )
        val httpsClient = ClambhookApiClient(baseUrl = "https://proxy.example.test")

        val httpRequest = httpClient.eventsRequest()
        val httpsRequest = httpsClient.eventsRequest()

        assertEquals(
            "ws://127.0.0.1:9090/api/v1/events?types=connection.*,log.*",
            httpClient.eventsUrl()
        )
        assertEquals("Bearer secret-token", httpRequest.headers["Authorization"])
        assertEquals(
            "wss://proxy.example.test/api/v1/events?types=connection.*,log.*",
            httpsClient.eventsUrl()
        )
        assertTrue(httpsRequest.headers["Authorization"].isNullOrEmpty())
    }
}

private class CapturingInterceptor(
    private val body: String,
    private val statusCode: Int = 200
) : Interceptor {
    val requests = mutableListOf<Request>()

    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        requests += request
        return Response.Builder()
            .request(request)
            .protocol(Protocol.HTTP_1_1)
            .code(statusCode)
            .message(if (statusCode in 200..299) "OK" else "Error")
            .body(body.toResponseBody("application/json".toMediaType()))
            .build()
    }
}

private fun RequestBody.bodyToString(): String {
    val buffer = Buffer()
    writeTo(buffer)
    return buffer.readUtf8()
}
