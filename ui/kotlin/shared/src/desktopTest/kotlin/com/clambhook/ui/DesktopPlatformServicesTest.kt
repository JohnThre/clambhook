// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui

import com.clambhook.ui.json.Json
import com.clambhook.ui.platform.DesktopPlatformServices
import com.clambhook.ui.platform.DesktopPlatformServices.ProcessResult
import com.clambhook.ui.platform.PlatformServiceException
import com.clambhook.ui.platform.PlatformServices.Capability
import com.clambhook.ui.runtime.DefaultRuntimeClient
import kotlinx.coroutines.runBlocking
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

class DesktopPlatformServicesTest {
    private val temporary: Path = Files.createTempDirectory("clambhook-desktop-test")
    private val ubuntu = osRelease("NAME=\"Ubuntu\"\nID=ubuntu\nID_LIKE=debian\n")

    @AfterTest
    fun cleanUp() {
        System.clearProperty("clambhook.packageManager")
        System.clearProperty("clambhook.licenseHelper")
        temporary.toFile().deleteRecursively()
    }

    private fun osRelease(contents: String): Path =
        Files.createTempFile(temporary, "os-release", "").also { Files.writeString(it, contents) }

    private fun services(
        backend: ScriptedBackend,
        osRelease: Path = ubuntu,
        runner: DesktopPlatformServices.CommandRunner,
    ) = DesktopPlatformServices(
        DefaultRuntimeClient(backend), runner, temporary.resolve("linux-license.json"), osRelease,
    )

    @Test
    fun startsPackagedDaemonBeforeConnectingWhenLoopbackApiIsDown() = runBlocking {
        val backend = ScriptedBackend(failStatus = true)
        val commands = mutableListOf<List<String>>()
        val services = services(backend) { command, _, _ ->
            commands += command
            backend.failStatus = false
            ProcessResult(0, "")
        }
        services.startVpn()
        assertTrue(services.supports(Capability.DAEMON_SUPERVISION))
        assertEquals(listOf(listOf("systemctl", "start", "clambhook-daemon.service")), commands)
        assertEquals(2, backend.statusCalls.get())
        assertEquals(1, backend.connectCalls.get())
    }

    @Test
    fun doesNotStartSystemServiceForHealthyOrRemoteDaemons() = runBlocking {
        val commands = mutableListOf<List<String>>()
        val healthy = ScriptedBackend(baseUrl = "http://localhost:9090")
        services(healthy) { command, _, _ -> commands += command; ProcessResult(0, "") }.startVpn()
        assertTrue(commands.isEmpty())
        assertEquals(1, healthy.connectCalls.get())

        val remote = ScriptedBackend(baseUrl = "https://controller.example:9443", failStatus = true)
        assertFailsWith<PlatformServiceException> {
            services(remote) { command, _, _ -> commands += command; ProcessResult(0, "") }.startVpn()
        }
        assertTrue(commands.isEmpty())
        assertEquals(0, remote.connectCalls.get())
    }

    @Test
    fun checksAndInstallsOnlyThroughTheSignedUbuntuRepository() = runBlocking {
        val commands = mutableListOf<List<String>>()
        val services = services(ScriptedBackend()) { command, _, _ ->
            commands += command
            if (command.first() == "apt-cache") ProcessResult(0, "Installed: 1.0.1\nCandidate: 1.0.2\n")
            else ProcessResult(0, "updated\n")
        }
        val check = services.updates("check", "{}")
        val install = services.updates("install", "{}")
        assertTrue(check.successful)
        assertTrue(check.payload.contains("\"update_available\":true"))
        assertEquals("a signed package update is available", check.message)
        assertTrue(install.successful)
        assertEquals(
            listOf(
                listOf("apt-cache", "policy", "clambhook"),
                listOf("pkexec", "apt-get", "--only-upgrade", "install", "-y", "clambhook"),
            ),
            commands,
        )
    }

    @Test
    fun fedoraUsesDnfAndOtherDistributionsAreUnsupported() = runBlocking {
        val commands = mutableListOf<List<String>>()
        val fedora = services(ScriptedBackend(), osRelease("ID=fedora\nVERSION_ID=44\n")) { command, _, _ ->
            commands += command
            ProcessResult(100, "clambhook.x86_64 1.0.3-1 clambhook\n")
        }
        assertTrue(fedora.updates("check", "{}").payload.contains("\"update_available\":true"))
        assertEquals(listOf(listOf("dnf", "--quiet", "check-upgrade", "clambhook")), commands)

        commands.clear()
        listOf("ID=debian\n", "ID=arch\n", "ID=\"opensuse-tumbleweed\"\n", "ID=linuxmint\nID_LIKE=\"ubuntu debian\"\n", "")
            .forEach { release ->
                val other = services(ScriptedBackend(), osRelease(release)) { command, _, _ ->
                    commands += command
                    ProcessResult(0, "")
                }
                val result = other.updates("check", "{}")
                assertFalse(result.successful, release)
                assertEquals("updates are supported only on Ubuntu (apt) and Fedora (dnf)", result.message)
            }
        assertTrue(commands.isEmpty())
        assertEquals("ubuntu", DesktopPlatformServices.supportedDistribution(ubuntu))
        assertNull(DesktopPlatformServices.supportedDistribution(temporary.resolve("missing")))
    }

    @Test
    fun rejectsUnknownUpdateOperationsWithoutLaunchingACommand() = runBlocking {
        var commands = 0
        val result = services(ScriptedBackend()) { _, _, _ -> commands++; ProcessResult(0, "") }.updates("remove", "{}")
        assertFalse(result.successful)
        assertEquals("unsupported update operation", result.message)
        assertEquals(0, commands)
    }

    @Test
    fun bootstrapsTrialAndPersistsFrozenLicenseSnapshot() = runBlocking {
        System.setProperty("clambhook.licenseHelper", "test-license-helper")
        val requests = mutableListOf<String>()
        val services = services(ScriptedBackend()) { command, input, required ->
            if (command.first() == "secret-tool") return@services ProcessResult(1, "not found")
            assertEquals("test-license-helper", command.first())
            assertTrue(required)
            val request = Json.parse(input)
            requests += request["command"].text()
            val result = when (request["command"].text()) {
                "install-id" -> "install-1"
                "ensure-trial" -> """{"trialStartDate":"2026-08-29T00:00:00Z","transactions":null}"""
                "status" -> """{"decision":{"reason":"trial","trialDaysRemaining":30}}"""
                else -> error(input)
            }
            ProcessResult(0, Json.obj("ok" to true, "result" to result))
        }
        val result = services.licensing("status", "{}")
        assertTrue(result.successful)
        assertTrue(result.payload.contains("\"reason\":\"trial\""))
        assertFalse(result.payload.contains("test-license-helper"))
        assertEquals(listOf("install-id", "ensure-trial", "status"), requests)
        val state = temporary.resolve("linux-license.json")
        assertEquals("install-1", Json.parse(Files.readString(state))["installId"].text())
        assertTrue(Files.readString(state.resolveSibling("license-snapshot.json")).contains("trialStartDate"))
    }
}
