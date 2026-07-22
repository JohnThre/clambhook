package com.clambhook.linux.settings

import com.clambhook.linux.daemon.DaemonSupervisor
import com.clambhook.linux.format.Formatters
import com.clambhook.linux.model.ServerPayload
import com.clambhook.linux.model.LocationPayload
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue
import java.nio.file.Files
import java.nio.file.Path

class SettingsDaemonTest {
    @Test
    fun normalizesDefaultsAndRefreshInterval() {
        val settings = AppSettings(
            apiEndpoint = "   ",
            refreshIntervalSeconds = 1,
            logRetention = 5,
            daemonPath = " /usr/local/bin/clambhook "
        )
        val normalized = settings.normalized()
        assertEquals("http://127.0.0.1:9090", normalized.apiEndpoint)
        assertEquals(2, normalized.refreshIntervalSeconds)
        assertEquals(MIN_LOG_RETENTION, normalized.logRetention)
        assertEquals("/usr/local/bin/clambhook", normalized.daemonPath)
        assertTrue(isSupportedApiEndpoint("https://proxy.example.test:9443/"))
        assertFalse(isSupportedApiEndpoint("ftp://proxy.example.test"))
    }

    @Test
    fun persistsJsonToConfigPath() {
        val tmpDir = Files.createTempDirectory("clambhook-linux-settings-test")
        val path = tmpDir.resolve("settings.json")
        val store = FileSettingsStore(path)
        val settings = AppSettings(
            apiEndpoint = " http://proxy.example:9090/ ",
            refreshIntervalSeconds = 90,
            logRetention = 900,
            eventStreamEnabled = false
        )
        store.save(settings)
        val loaded = store.load()
        assertEquals("http://proxy.example:9090", loaded.apiEndpoint)
        assertEquals(60, loaded.refreshIntervalSeconds)
        assertEquals(MAX_LOG_RETENTION, loaded.logRetention)
        assertFalse(loaded.eventStreamEnabled)
    }

    @Test
    fun resolvesConfiguredPathAndAdjacentPath() {
        val tmpDir = Files.createTempDirectory("clambhook-linux-daemon-path-test")
        val configured = tmpDir.resolve("configured/clambhook")
        val appDir = tmpDir.resolve("app")
        val adjacent = appDir.resolve("clambhook")
        Files.createDirectories(configured.parent)
        Files.createDirectories(appDir)
        Files.writeString(configured, "configured daemon")

        val settings = AppSettings(daemonPath = " $configured ")
        assertEquals(configured.toString(), DaemonSupervisor.resolveExecutablePath(settings, appDir.toString(), false))

        val settings2 = AppSettings(daemonPath = "")
        Files.writeString(adjacent, "adjacent daemon")
        assertEquals(adjacent.toString(), DaemonSupervisor.resolveExecutablePath(settings2, appDir.toString(), false))

        Files.delete(adjacent)
        assertNull(DaemonSupervisor.resolveExecutablePath(settings2, appDir.toString(), false))

        val settings3 = AppSettings(configPath = " /tmp/clambhook.toml ")
        val args = DaemonSupervisor.buildArguments(settings3, " token ")
        assertEquals("""-api "127.0.0.1:9090" -api-token "token" -config "/tmp/clambhook.toml"""", args)

        val settings4 = AppSettings(apiEndpoint = " http://[::1]:9091/ ", configPath = " /tmp/clambhook.toml ")
        val args4 = DaemonSupervisor.buildArguments(settings4, " token ")
        assertEquals("""-api "[::1]:9091" -api-token "token" -config "/tmp/clambhook.toml"""", args4)
    }

    @Test
    fun formatsRatesAndServerLocation() {
        assertEquals("512 B/s", Formatters.formatRate(512.0))
        assertEquals("1.5 KB/s", Formatters.formatRate(1536.0))
        val server = ServerPayload(address = "uk.example:443", geo = LocationPayload(city = "London", country = "United Kingdom"))
        assertEquals("London, United Kingdom", Formatters.serverLocation(server))
    }
}