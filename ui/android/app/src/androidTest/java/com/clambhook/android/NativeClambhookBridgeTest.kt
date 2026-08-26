package com.clambhook.android

import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import java.io.File
import kotlinx.serialization.decodeFromString
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/** Runs the C/JNI bridge on the device, including native TOML configuration. */
@RunWith(AndroidJUnit4::class)
class NativeClambhookBridgeTest {
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
        }
    }
}
