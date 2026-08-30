// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

import android.Manifest
import android.accessibilityservice.AccessibilityService
import android.app.NotificationManager
import android.content.ComponentName
import android.content.Context
import android.content.pm.PackageManager
import android.net.VpnService
import android.net.Uri
import android.os.Build
import android.os.ParcelFileDescriptor
import android.view.accessibility.AccessibilityNodeInfo
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.long
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/** Device-side contract tests for the small Kotlin AAR and its C17 runtime. */
@RunWith(AndroidJUnit4::class)
class NativeClambhookBridgeTest {
    private val context: Context
        get() = ApplicationProvider.getApplicationContext()

    private fun objectJson(value: String): JsonObject =
        ApiJson.parseToJsonElement(value).jsonObject

    private fun array(root: JsonObject, key: String): JsonArray =
        requireNotNull(root[key]).jsonArray

    private fun configFile(name: String = "native-bridge-test.toml"): File =
        File(context.cacheDir, name).apply {
            writeText(
                """
                active = "work"

                [[profile]]
                name = "home"
                [[profile.chain]]
                name = "home-default"
                [[profile.chain.server]]
                protocol = "direct"
                [[profile.rule]]
                name = "block-discard"
                action = "block"
                ports = [9]
                networks = ["udp"]

                [[profile]]
                name = "work"
                [[profile.chain]]
                name = "work-default"
                [[profile.chain.server]]
                protocol = "direct"
                [[profile.rule]]
                name = "direct-web"
                action = "direct"
                ports = [80, 443]
                networks = ["tcp"]
                """.trimIndent() + "\n",
            )
        }

    @Test
    fun converterReviewAndImportRunThroughAndroidJniAdapter() {
        val config = configFile("converter-bridge.toml")
        val source = """
            [Proxy]
            Mobile = ss, proxy.example, 8388, encrypt-method=aes-256-gcm, password=private
            [Rule]
            FINAL,Mobile
        """.trimIndent()
        val reviewRequest = buildJsonObject {
            put("source", JsonPrimitive(source))
            put("format", JsonPrimitive("surge"))
            put("profile_name", JsonPrimitive("Mobile Import"))
        }.toString()
        val review = objectJson(
            NativeClambhookConfigBridge.converterReview(reviewRequest),
        )
        assertEquals("surge", review.getValue("format").jsonPrimitive.content)
        val importRequest = buildJsonObject {
            put("source", JsonPrimitive(source))
            put("format", JsonPrimitive("surge"))
            put("profile_name", JsonPrimitive("Mobile Import"))
            put("expected_sha256", review.getValue("sha256"))
            put("activate", JsonPrimitive(false))
        }.toString()
        NativeClambhookConfigBridge.converterImport(config.absolutePath, importRequest)
        assertTrue(config.readText().contains("name = \"Mobile Import\""))
        assertTrue(config.readText().contains("active = \"work\""))
    }

    private fun shell(command: String): String {
        val descriptor = InstrumentationRegistry.getInstrumentation()
            .uiAutomation.executeShellCommand(command)
        return ParcelFileDescriptor.AutoCloseInputStream(descriptor)
            .bufferedReader().use { it.readText() }
    }

    private fun setVpnAuthorization(mode: String) {
        check(mode == "allow" || mode == "ignore")
        check(context.packageName.matches(Regex("[A-Za-z0-9._]+")))
        shell("appops set ${context.packageName} ACTIVATE_VPN $mode")
        val status = shell("appops get ${context.packageName} ACTIVATE_VPN")
        assertTrue("unexpected VPN app-op status: $status", status.contains(mode))
    }

    private fun waitUntil(timeoutMillis: Long = 15_000, condition: () -> Boolean): Boolean {
        val deadline = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(timeoutMillis)
        while (System.nanoTime() < deadline) {
            if (condition()) return true
            Thread.sleep(25)
        }
        return condition()
    }

    private fun clickSystemDialogButton(
        resourceName: String,
        fallbackLabels: List<String>,
    ): Boolean {
        val automation = InstrumentationRegistry.getInstrumentation().uiAutomation
        return waitUntil(10_000) {
            val root = automation.rootInActiveWindow ?: return@waitUntil false
            val byId = root.findAccessibilityNodeInfosByViewId("android:id/$resourceName")
                .firstOrNull { it.isClickable }
            val byLabel = fallbackLabels.asSequence()
                .flatMap { label -> root.findAccessibilityNodeInfosByText(label).asSequence() }
                .firstOrNull { it.isClickable }
            (byId ?: byLabel)?.performAction(AccessibilityNodeInfo.ACTION_CLICK) == true
        }
    }

    private fun answerVpnConsent(accept: Boolean): Boolean {
        val response = AtomicReference<String?>()
        val failure = AtomicReference<Throwable?>()
        val request = Thread {
            try {
                response.set(GluonPlatformFacade.dispatch("vpn-consent", "{}"))
            } catch (error: Throwable) {
                failure.set(error)
            }
        }.apply { start() }
        val clicked = clickSystemDialogButton(
            if (accept) "button1" else "button2",
            if (accept) listOf("OK", "Allow") else listOf("Cancel", "Don't allow"),
        )
        if (!clicked) {
            VpnConsentCoordinator.cancelPending()
            InstrumentationRegistry.getInstrumentation().uiAutomation.performGlobalAction(
                AccessibilityService.GLOBAL_ACTION_BACK,
            )
        }
        // Managed ATD instances can take longer to deliver the activity result
        // while three API levels run sequentially on the same hosted runner.
        request.join(20_000)
        assertTrue("system VPN consent request did not finish", !request.isAlive)
        failure.get()?.let { throw AssertionError("VPN consent request failed", it) }
        assertTrue("system VPN consent dialog button was not found", clicked)
        return objectJson(requireNotNull(response.get()))
            .getValue("granted").jsonPrimitive.boolean
    }

    private fun encryptedDnsConfig(protocol: String): File {
        val address = if (protocol == "doh") {
            "url = \"https://dns.example/dns-query\"\nbootstrap_ips = [\"127.0.0.1\"]"
        } else {
            "address = \"127.0.0.1:9\"\nserver_name = \"localhost\""
        }
        return File(context.cacheDir, "native-$protocol-dns-test.toml").apply {
            writeText(
                """
                active = "encrypted-dns"
                [[profile]]
                name = "encrypted-dns"
                [profile.dns]
                enabled = true
                timeout = "50ms"
                [[profile.dns.upstream]]
                name = "device-$protocol"
                protocol = "$protocol"
                $address
                [[profile.chain]]
                name = "direct"
                [[profile.chain.server]]
                protocol = "direct"
                """.trimIndent() + "\n",
            )
        }
    }

    private fun writeU16(bytes: ByteArray, offset: Int, value: Int) {
        bytes[offset] = (value ushr 8).toByte()
        bytes[offset + 1] = value.toByte()
    }

    private fun checksum(bytes: ByteArray, offset: Int, length: Int): Int {
        var sum = 0L
        var cursor = offset
        var remaining = length
        while (remaining >= 2) {
            sum += ((bytes[cursor].toInt() and 0xff) shl 8) or
                (bytes[cursor + 1].toInt() and 0xff)
            cursor += 2
            remaining -= 2
        }
        if (remaining == 1) sum += (bytes[cursor].toInt() and 0xff) shl 8
        while ((sum ushr 16) != 0L) sum = (sum and 0xffffL) + (sum ushr 16)
        return sum.inv().toInt() and 0xffff
    }

    private fun ipv4UdpPacket(targetPort: Int, payload: ByteArray): ByteArray {
        val packet = ByteArray(28 + payload.size)
        packet[0] = 0x45
        writeU16(packet, 2, packet.size)
        packet[4] = 0x12
        packet[5] = 0x34
        packet[6] = 0x40
        packet[8] = 64
        packet[9] = 17
        packet[12] = 198.toByte()
        packet[13] = 18
        packet[15] = 2
        packet[16] = 127
        packet[19] = 1
        writeU16(packet, 20, 42000)
        writeU16(packet, 22, targetPort)
        writeU16(packet, 24, 8 + payload.size)
        payload.copyInto(packet, 28)
        writeU16(packet, 10, checksum(packet, 0, 20))
        return packet
    }

    private fun dnsQuery(): ByteArray = byteArrayOf(
        0x12, 0x34, 0x01, 0x00, 0x00, 0x01, 0x00, 0x00,
        0x00, 0x00, 0x00, 0x00, 0x07,
        'e'.code.toByte(), 'x'.code.toByte(), 'a'.code.toByte(),
        'm'.code.toByte(), 'p'.code.toByte(), 'l'.code.toByte(),
        'e'.code.toByte(), 0x03, 'c'.code.toByte(), 'o'.code.toByte(),
        'm'.code.toByte(), 0x00, 0x00, 0x01, 0x00, 0x01,
    )

    private fun assertDnsServfail(packet: ByteArray) {
        assertEquals(53, ((packet[20].toInt() and 0xff) shl 8) or
            (packet[21].toInt() and 0xff))
        assertEquals(42000, ((packet[22].toInt() and 0xff) shl 8) or
            (packet[23].toInt() and 0xff))
        assertEquals(0x12, packet[28].toInt() and 0xff)
        assertEquals(0x34, packet[29].toInt() and 0xff)
        assertEquals(0x80, packet[30].toInt() and 0x80)
        assertEquals(0x02, packet[31].toInt() and 0x0f)
    }

    @Test
    fun startsWithPersistedActiveProfileAndExposesFrozenQueries() {
        NativeClambhookBridge { }.use { bridge ->
            bridge.start(configFile().absolutePath)
            assertTrue(bridge.isRunning())
            val status = objectJson(bridge.query("status"))
            assertEquals("work", status.getValue("profile").jsonPrimitive.content)
            assertEquals("tun", status.getValue("tunnel_mode").jsonPrimitive.content)

            val profiles = objectJson(bridge.query("profiles"))
            assertEquals(listOf("home", "work"), array(profiles, "profiles").map {
                it.jsonPrimitive.content
            })
            assertEquals("work", profiles.getValue("active").jsonPrimitive.content)

            val rules = objectJson(bridge.query("rules"))
            assertEquals("work", rules.getValue("profile").jsonPrimitive.content)
            assertEquals(
                "direct-web",
                array(rules, "rules").single().jsonObject
                    .getValue("name").jsonPrimitive.content,
            )
            val crypto = objectJson(bridge.query("crypto_self_test"))
            assertTrue(crypto.getValue("aes_256_gcm").jsonPrimitive.boolean)
            assertTrue(crypto.getValue("chacha20_poly1305").jsonPrimitive.boolean)
            bridge.stop()
            assertFalse(bridge.isRunning())
        }
    }

    @Test
    fun fileBackedQueriesAndMutationsRemainTransactionalWhileVpnIsStopped() {
        val config = configFile("native-file-config-test.toml")
        val profiles = objectJson(
            NativeClambhookConfigBridge.query(config.absolutePath, "profiles"),
        )
        assertEquals("work", profiles.getValue("active").jsonPrimitive.content)

        val result = objectJson(
            NativeClambhookConfigBridge.mutate(
                config.absolutePath,
                "create_rule",
                "rules_persistence",
                """{"position":"append","rule":{"name":"device-native-rule","action":"direct"}}""",
            ),
        )
        assertEquals(
            "device-native-rule",
            array(result, "rules").last().jsonObject
                .getValue("name").jsonPrimitive.content,
        )
        assertThrows(IllegalStateException::class.java) {
            NativeClambhookConfigBridge.mutate(
                config.absolutePath,
                "replace_rules",
                "rules_persistence",
                """{"rules":{}}""",
            )
        }
        val persisted = objectJson(
            NativeClambhookConfigBridge.query(
                config.absolutePath,
                "rules_persistence",
            ),
        )
        assertEquals(
            "device-native-rule",
            array(persisted, "rules").last().jsonObject
                .getValue("name").jsonPrimitive.content,
        )
    }

    @Test
    fun persistsProfileAndImportsConfigWithLiveRollbackBoundary() {
        val config = configFile("native-persistence-test.toml")
        NativeClambhookBridge { }.use { bridge ->
            bridge.start(config.absolutePath)
            val switched = objectJson(
                bridge.mutate("persist_active_profile", "{\"name\":\"home\"}"),
            )
            assertTrue(switched.getValue("persisted").jsonPrimitive.boolean)
            assertEquals("home", switched.getValue("profile").jsonPrimitive.content)
            assertEquals(
                "home",
                objectJson(
                    NativeClambhookConfigBridge.query(config.absolutePath, "profiles"),
                ).getValue("active").jsonPrimitive.content,
            )

            val importedToml = """
                active = "imported"
                [[profile]]
                name = "imported"
                [[profile.chain]]
                name = "direct"
                [[profile.chain.server]]
                protocol = "direct"
            """.trimIndent() + "\n"
            val imported = objectJson(bridge.mutate("config_import", importedToml))
            assertEquals("imported", imported.getValue("active").jsonPrimitive.content)
            assertTrue(imported.getValue("message").jsonPrimitive.content.contains("1 profile"))
            assertEquals(
                "imported",
                objectJson(bridge.query("status")).getValue("profile")
                    .jsonPrimitive.content,
            )
            assertThrows(IllegalStateException::class.java) {
                bridge.mutate("config_import", "not valid toml = [")
            }
            assertEquals(
                "imported",
                objectJson(bridge.query("status")).getValue("profile")
                    .jsonPrimitive.content,
            )
        }
    }

    @Test
    fun emitsTrafficDecisionsEventsAndTemporaryRules() {
        NativeClambhookBridge { }.use { bridge ->
            bridge.start(configFile("native-events-test.toml").absolutePath)
            bridge.mutate("set_active_profile", "{\"name\":\"home\"}")
            assertThrows(IllegalStateException::class.java) {
                bridge.injectPacket(ipv4UdpPacket(9, "blocked".encodeToByteArray()))
            }
            val traffic = objectJson(bridge.query("traffic_filter", "{\"limit\":20}"))
            val blocked = array(traffic, "connections").map { it.jsonObject }.first {
                it["rule_action"]?.jsonPrimitive?.content == "block"
            }
            val connectionId = blocked.getValue("conn_id").jsonPrimitive.content
            assertEquals("block-discard", blocked.getValue("rule_name").jsonPrimitive.content)

            val decisions = objectJson(bridge.query("decisions", "{\"limit\":20}"))
            assertTrue(array(decisions, "decisions").isNotEmpty())
            val events = objectJson(
                bridge.query("events", "{\"after_sequence\":0,\"limit\":16}"),
            )
            val emitted = array(events, "events")
            assertTrue(emitted.isNotEmpty())
            assertTrue(emitted.all { it.jsonObject.getValue("sequence").jsonPrimitive.long > 0 })

            val temporary = objectJson(
                bridge.mutate(
                    "create_temporary_rule_from_connection",
                    """{"conn_id":"$connectionId","profile":"home","name":"allow-discard-temporarily","action":"direct","scope":"auto","ttl_seconds":60}""",
                ),
            )
            assertTrue(temporary.toString().contains("allow-discard-temporarily"))
            val rules = objectJson(bridge.query("temporary_rules"))
            assertEquals(1, array(rules, "temporary_rules").size)
            val identifier = array(rules, "temporary_rules").single().jsonObject
                .getValue("id").jsonPrimitive.content
            bridge.mutate("remove_temporary_rule", "{\"id\":\"$identifier\"}")
            assertTrue(
                array(
                    objectJson(bridge.query("temporary_rules")),
                    "temporary_rules",
                ).isEmpty(),
            )
        }
    }

    @Test
    fun resolvesPromptWhilePacketInjectionWaits() {
        val config = File(context.cacheDir, "native-prompt-test.toml").apply {
            writeText(
                """
                active = "prompt"
                [prompt]
                enabled = true
                timeout_seconds = 5
                default_allow = false
                [[profile]]
                name = "prompt"
                [[profile.chain]]
                name = "default"
                [[profile.chain.server]]
                protocol = "direct"
                """.trimIndent() + "\n",
            )
        }
        NativeClambhookBridge { }.use { bridge ->
            bridge.start(config.absolutePath)
            val injectionFailure = AtomicReference<Throwable?>()
            val injection = Thread {
                try {
                    bridge.injectPacket(ipv4UdpPacket(9, "prompt".encodeToByteArray()))
                } catch (error: Throwable) {
                    injectionFailure.set(error)
                }
            }.apply { start() }
            var prompt: JsonObject? = null
            for (attempt in 0 until 100) {
                prompt = array(objectJson(bridge.query("pending_prompts")), "prompts")
                    .singleOrNull()?.jsonObject
                if (prompt != null) break
                Thread.sleep(10)
            }
            val pending = requireNotNull(prompt)
            val identifier = pending.getValue("id").jsonPrimitive.content
            val resolved = objectJson(
                bridge.mutate(
                    "resolve_prompt",
                    """{"id":"$identifier","action":"block","scope":"once"}""",
                ),
            )
            assertTrue(resolved.getValue("resolved").jsonPrimitive.boolean)
            injection.join(2_000)
            assertFalse(injection.isAlive)
            assertTrue(injectionFailure.get() is IllegalStateException)
        }
    }

    @Test
    fun encryptedDnsTransportsReturnServfailWhenLocalUpstreamIsUnavailable() {
        for (protocol in listOf("dot", "doh", "doq")) {
            var output: ByteArray? = null
            NativeClambhookBridge { packet -> output = packet }.use { bridge ->
                bridge.start(encryptedDnsConfig(protocol).absolutePath)
                bridge.injectPacket(ipv4UdpPacket(53, dnsQuery()))
            }
            assertDnsServfail(requireNotNull(output))
        }
    }

    @Test
    fun forwardsDirectUdpAndTicksRemoteResponse() {
        val server = DatagramSocket(0, InetAddress.getByName("127.0.0.1"))
        val responseReady = CountDownLatch(1)
        val serverFailure = AtomicReference<Throwable?>()
        var output: ByteArray? = null
        val serverThread = Thread {
            try {
                val incoming = DatagramPacket(ByteArray(128), 128)
                server.receive(incoming)
                val response = "native-udp-response".encodeToByteArray()
                server.send(DatagramPacket(response, response.size, incoming.socketAddress))
            } catch (error: Throwable) {
                if (!server.isClosed) serverFailure.set(error)
                responseReady.countDown()
            }
        }.apply { start() }
        try {
            NativeClambhookBridge { packet ->
                if (packet.size >= 28 && packet[9].toInt() == 17) {
                    output = packet
                    responseReady.countDown()
                }
            }.use { bridge ->
                bridge.start(configFile("native-udp-test.toml").absolutePath)
                bridge.injectPacket(
                    ipv4UdpPacket(server.localPort, "native-udp-request".encodeToByteArray()),
                )
                assertTrue(responseReady.await(3, TimeUnit.SECONDS))
                serverFailure.get()?.let { throw AssertionError("UDP server failed", it) }
            }
            val packet = requireNotNull(output)
            assertEquals(
                "native-udp-response",
                packet.copyOfRange(28, packet.size).decodeToString(),
            )
        } finally {
            server.close()
            serverThread.join(1_000)
        }
    }

    @Test
    fun facadeReadsConfigAndPlatformCapabilitiesWithoutLegacyUiActivity() {
        AndroidPlatformEnvironment.initialize(context)
        runBlocking { AndroidConfigStore(context).saveConfig(configFile().readText()) }
        val profiles = objectJson(
            GluonPlatformFacade.request("GET", "/api/v1/profiles", ""),
        )
        assertEquals("work", profiles.getValue("active").jsonPrimitive.content)
        val installed = objectJson(GluonPlatformFacade.dispatch("installed-apps", "{}"))
        assertNotNull(installed["applications"])
    }

    @Test
    fun outlineDeepLinksAreReviewedAndExplicitlyImportedWithoutConnecting() {
        AndroidPlatformEnvironment.initialize(context)
        AndroidPlatformEnvironment.receiveOutlineUri(Uri.parse("https://example.com/not-a-key"))
        assertEquals("", AndroidPlatformEnvironment.consumeOutlineUri())

        val accessKey = "ss://aes-128-gcm:device-secret@example.com:443#Device%20Outline"
        AndroidPlatformEnvironment.receiveOutlineUri(Uri.parse(accessKey))
        assertEquals(accessKey, AndroidPlatformEnvironment.consumeOutlineUri())
        assertEquals("", AndroidPlatformEnvironment.consumeOutlineUri())

        runBlocking {
            AndroidConfigStore(context).saveConfig(configFile("outline-import.toml").readText())
        }
        val review = GluonPlatformFacade.request(
            "POST",
            "/api/v1/outline/review",
            """{"access_key":${JsonPrimitive(accessKey)}}""",
        )
        assertFalse(review.contains("device-secret"))
        assertEquals(
            "Device Outline",
            objectJson(review).getValue("suggested_name").jsonPrimitive.content,
        )

        val imported = objectJson(
            GluonPlatformFacade.request(
                "POST",
                "/api/v1/outline/import",
                """{"access_key":${JsonPrimitive(accessKey)},"profile_name":"Device Outline","activate":false}""",
            ),
        )
        assertEquals("work", imported.getValue("active").jsonPrimitive.content)
        assertTrue(array(imported, "profiles").any {
            it.jsonPrimitive.content == "Device Outline"
        })
        assertNull(ClambhookTunnelSession.runtime.value)
    }

    @Test
    fun platformFacadePersistsFilesSecretsRoutingAndQrExport() {
        AndroidPlatformEnvironment.initialize(context)
        val target = File(context.cacheDir, "platform/facade-profile.toml")
        val contents = configFile("platform-source.toml").readText()
        GluonPlatformFacade.dispatch(
            "file-write",
            """{"path":"${target.absolutePath}","value":${JsonPrimitive(contents)}}""",
        )
        assertEquals(
            contents,
            GluonPlatformFacade.dispatch(
                "file-read",
                """{"path":"${target.absolutePath}","maximum_bytes":1048576}""",
            ),
        )
        assertThrows(IllegalStateException::class.java) {
            GluonPlatformFacade.dispatch(
                "file-read",
                """{"path":"${File(context.filesDir, "../escape").absolutePath}"}""",
            )
        }

        val storageKey = "managed-device-secret"
        GluonPlatformFacade.dispatch(
            "secure-write",
            """{"key":"$storageKey","value":"sensitive-device-value"}""",
        )
        assertEquals(
            "sensitive-device-value",
            GluonPlatformFacade.dispatch("secure-read", """{"key":"$storageKey"}"""),
        )
        GluonPlatformFacade.dispatch("secure-delete", """{"key":"$storageKey"}""")
        assertEquals("", GluonPlatformFacade.dispatch("secure-read", """{"key":"$storageKey"}"""))

        val routing = objectJson(
            GluonPlatformFacade.dispatch(
                "per-app-routing-update",
                """{"mode":"include","packages":["com.example.zeta","invalid package","com.example.alpha"]}""",
            ),
        )
        assertEquals("include", routing.getValue("mode").jsonPrimitive.content)
        assertEquals(
            listOf("com.example.alpha", "com.example.zeta"),
            array(routing, "packages").map { it.jsonPrimitive.content },
        )

        val qrFile = createQrShareFile(context, contents)
        assertTrue(qrFile.isFile)
        assertTrue(qrFile.length() > 1_024)
        val header = ByteArray(4)
        qrFile.inputStream().use { input -> assertEquals(4, input.read(header)) }
        assertEquals(
            listOf(0x89, 0x50, 0x4e, 0x47),
            header.map { it.toInt() and 0xff },
        )

        val imported = objectJson(
            GluonPlatformFacade.request("POST", "/api/v1/config/import", contents),
        )
        assertEquals("work", imported.getValue("active").jsonPrimitive.content)
        assertEquals(
            "work",
            objectJson(GluonPlatformFacade.request("GET", "/api/v1/profiles", ""))
                .getValue("active").jsonPrimitive.content,
        )
    }

    @Test
    @Suppress("DEPRECATION")
    fun vpnConsentForegroundRuntimeReconnectAndFrameworkRevokeJourney() {
        AndroidPlatformEnvironment.initialize(context)
        runBlocking { AndroidConfigStore(context).saveConfig(configFile("vpn-service.toml").readText()) }
        GluonPlatformFacade.dispatch("vpn-stop", "{}")
        waitUntil { ClambhookTunnelSession.runtime.value == null }

        try {
            setVpnAuthorization("ignore")
            assertNotNull(VpnService.prepare(context))
            assertFalse("denied VPN consent was accepted", answerVpnConsent(accept = false))
            assertNotNull(VpnService.prepare(context))

            assertTrue("approved VPN consent was rejected", answerVpnConsent(accept = true))
            assertNull(VpnService.prepare(context))
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                shell("pm grant ${context.packageName} ${Manifest.permission.POST_NOTIFICATIONS}")
            }

            val service = context.packageManager.getServiceInfo(
                ComponentName(context, ClambhookVpnService::class.java),
                PackageManager.GET_META_DATA,
            )
            assertEquals(Manifest.permission.BIND_VPN_SERVICE, service.permission)
            assertFalse(service.exported)
            assertTrue(
                service.metaData?.getBoolean(
                    VpnService.SERVICE_META_DATA_SUPPORTS_ALWAYS_ON,
                    true,
                ) ?: true,
            )

            assertEquals(
                true,
                objectJson(GluonPlatformFacade.dispatch("vpn-start", "{}"))
                    .getValue("accepted").jsonPrimitive.boolean,
            )
            assertTrue("foreground VPN runtime did not attach", waitUntil {
                ClambhookTunnelSession.runtime.value?.isRunning() == true
            })
            val firstRuntime = requireNotNull(ClambhookTunnelSession.runtime.value)
            assertTrue(
                objectJson(GluonPlatformFacade.request("GET", "/api/v1/status", ""))
                    .getValue("running").jsonPrimitive.boolean,
            )
            assertNotNull(
                context.getSystemService(NotificationManager::class.java)
                    .getNotificationChannel("clambhook_vpn"),
            )

            GluonPlatformFacade.dispatch("vpn-start", "{}")
            assertTrue("reconnect did not replace the runtime atomically", waitUntil {
                ClambhookTunnelSession.runtime.value?.let {
                    it !== firstRuntime && it.isRunning()
                } == true
            })
            GluonPlatformFacade.dispatch("vpn-stop", "{}")
            assertTrue("explicit stop left the runtime attached", waitUntil {
                ClambhookTunnelSession.runtime.value == null
            })

            GluonPlatformFacade.dispatch("vpn-start", "{}")
            assertTrue("restart did not restore the runtime", waitUntil {
                ClambhookTunnelSession.runtime.value?.isRunning() == true
            })
            setVpnAuthorization("ignore")
            assertNotNull(VpnService.prepare(context))
            assertTrue("framework revocation left the runtime attached", waitUntil {
                ClambhookTunnelSession.runtime.value == null
            })
        } finally {
            GluonPlatformFacade.dispatch("vpn-stop", "{}")
            waitUntil { ClambhookTunnelSession.runtime.value == null }
            setVpnAuthorization("ignore")
        }
    }

    @Test
    fun evaluatesLicenseStateThroughNativeC() {
        val installId = NativeClambhookLicenseBridge.newInstallId()
        assertTrue(
            Regex("^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")
                .matches(installId),
        )
        val snapshot = NativeClambhookLicenseBridge.ensureTrial("", 1_788_000_000_000L)
        val status = objectJson(
            NativeClambhookLicenseBridge.status(
                snapshot,
                publishedMillis = 0,
                nowMillis = 1_788_000_000_000L,
            ),
        )
        assertEquals(
            "trial",
            status.getValue("decision").jsonObject
                .getValue("reason").jsonPrimitive.content,
        )
        assertTrue(
            status.getValue("decision").jsonObject
                .getValue("reason").jsonPrimitive.content != "locked",
        )
    }
}
