package com.clambhook.android

import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.io.File
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicReference
import kotlinx.serialization.decodeFromString
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/** Runs the C/JNI bridge on the device, including native TOML configuration. */
@RunWith(AndroidJUnit4::class)
class NativeClambhookBridgeTest {
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

    private fun configFile(): File {
        val context = ApplicationProvider.getApplicationContext<android.content.Context>()
        return File(context.cacheDir, "native-bridge-test.toml").apply {
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
    }

    @Test
    fun loadsConfigAndReportsProfilesThroughJni() {
        val config = configFile()
        var outputPacket: ByteArray? = null

        NativeClambhookBridge { outputPacket = it }.use { bridge ->
            assertFalse(bridge.isRunning())
            bridge.start(config.absolutePath)
            assertTrue(bridge.isRunning())
            assertEquals(
                "{\"profiles\":[\"home\",\"work\"],\"active\":\"work\"}",
                bridge.query("profiles"),
            )
            assertEquals(
                "{\"running\":true,\"profile\":\"work\",\"network_info\":{}," +
                    "\"tunnel_mode\":\"tun\"}",
                bridge.query("status"),
            )
            bridge.injectPacket(
                byteArrayOf(
                    0x45, 0x00, 0x00, 0x20, 0x12, 0x34, 0x00, 0x00,
                    64, 1, 0xdc.toByte(), 0x81.toByte(),
                    198.toByte(), 18, 0, 2, 198.toByte(), 18, 0, 1,
                    8, 0, 0x6d, 0x60, 0xab.toByte(), 0xcd.toByte(), 0, 1,
                    'p'.code.toByte(), 'i'.code.toByte(),
                    'n'.code.toByte(), 'g'.code.toByte(),
                ),
            )
            assertEquals(0, outputPacket?.get(20)?.toInt())
            val rules = ApiJson.decodeFromString<RulesPayload>(bridge.query("rules"))
            assertEquals("work", rules.profile)
            assertEquals("direct-web", rules.rules.single().name)
            assertEquals(listOf(80, 443), rules.rules.single().ports)
            val homeRules = ApiJson.decodeFromString<RulesPayload>(
                bridge.query("rules", "{\"profile\":\"home\"}"),
            )
            assertEquals("home", homeRules.profile)
            assertEquals("block-discard", homeRules.rules.single().name)
            bridge.stop()
            assertFalse(bridge.isRunning())
        }
    }

    @Test
    fun kotlinRuntimeFacadeDecodesDashboardAndSwitchesProfile() {
        NativeClambhookTunnelRuntime { }.use { runtime ->
            runtime.start(configFile().absolutePath)
            val dashboard = ApiJson.decodeFromString<TunnelDashboardBundle>(runtime.dashboardJson())
            assertEquals("work", dashboard.status.profile)
            assertEquals("direct-web", dashboard.rules.rules.single().name)
            val explanation = ApiJson.decodeFromString<RuleTestResponse>(
                runtime.testRuleJson("", "tcp", "example.com:443", ""),
            )
            assertEquals("work", explanation.profile)
            assertEquals("direct-web", explanation.decision.ruleName)
            assertEquals("direct", explanation.decision.action)
            runtime.setActiveProfile("home")
            assertEquals("home", ApiJson.decodeFromString<StatusPayload>(runtime.statusJson()).profile)
            assertEquals("home", ApiJson.decodeFromString<RulesPayload>(runtime.rulesJson()).profile)
            assertThrows(IllegalStateException::class.java) {
                runtime.injectPacket(ipv4UdpPacket(9, "blocked".encodeToByteArray()))
            }
        }
    }

    @Test
    fun forwardsDirectUdpAndTicksRemoteResponseInNativeCode() {
        val server = DatagramSocket(0, InetAddress.getByName("127.0.0.1"))
        val responseReady = CountDownLatch(1)
        val serverFailure = AtomicReference<Throwable?>()
        var output: ByteArray? = null
        val serverThread = Thread {
            try {
                val incoming = DatagramPacket(ByteArray(128), 128)
                server.receive(incoming)
                Thread.sleep(50)
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
                bridge.start(configFile().absolutePath)
                bridge.injectPacket(
                    ipv4UdpPacket(
                        server.localPort,
                        "native-udp-request".encodeToByteArray(),
                    ),
                )
                assertTrue(responseReady.await(3, TimeUnit.SECONDS))
                serverFailure.get()?.let { throw AssertionError("UDP server failed", it) }
                bridge.stop()
            }
            val packet = requireNotNull(output)
            assertEquals(127, packet[12].toInt() and 0xff)
            assertEquals(1, packet[15].toInt() and 0xff)
            assertEquals(198, packet[16].toInt() and 0xff)
            assertEquals(18, packet[17].toInt() and 0xff)
            assertEquals(2, packet[19].toInt() and 0xff)
            assertEquals(server.localPort, ((packet[20].toInt() and 0xff) shl 8) or
                (packet[21].toInt() and 0xff))
            assertEquals(42000, ((packet[22].toInt() and 0xff) shl 8) or
                (packet[23].toInt() and 0xff))
            assertEquals(
                "native-udp-response",
                packet.copyOfRange(28, packet.size).decodeToString(),
            )
        } finally {
            server.close()
            serverThread.join(1000)
        }
    }
}
