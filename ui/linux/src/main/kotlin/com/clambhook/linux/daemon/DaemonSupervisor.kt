package com.clambhook.linux.daemon

import com.clambhook.linux.settings.AppSettings
import com.clambhook.linux.settings.normalized
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.io.File
import java.nio.file.Files
import java.nio.file.Paths

enum class DaemonState { STOPPED, STARTING, RUNNING, STOPPING, FAILED }

data class DaemonStatus(val state: DaemonState, val message: String = "") {
    fun stateLabel(): String = when (state) {
        DaemonState.STOPPED -> "Daemon stopped"
        DaemonState.STARTING -> "Daemon starting"
        DaemonState.RUNNING -> "Daemon running"
        DaemonState.STOPPING -> "Daemon stopping"
        DaemonState.FAILED -> "Daemon failed"
    }
    fun stateIsBusy(): Boolean = state == DaemonState.STARTING || state == DaemonState.STOPPING
}

class DaemonSupervisor {
    private var process: Process? = null
    private val _status = MutableStateFlow(DaemonStatus(DaemonState.STOPPED))
    val status: StateFlow<DaemonStatus> = _status.asStateFlow()

    val isRunning: Boolean get() = process != null && process!!.isAlive

    fun start(settings: AppSettings, token: String, appBaseDir: String, licensePath: String? = null) {
        if (isRunning) { transition(DaemonState.RUNNING); return }
        transition(DaemonState.STARTING)
        try {
            val executable = resolveExecutablePath(settings, appBaseDir)
                ?: throw IllegalStateException("clambhook daemon executable was not found")
            val argv = buildArgv(settings, token, licensePath).toMutableList().also { it.add(0, executable) }
            val builder = ProcessBuilder(argv)
            process = builder.start()
            transition(DaemonState.RUNNING)
            watchProcess(process!!)
        } catch (e: Exception) {
            process = null
            transition(DaemonState.FAILED, e.message ?: "failed")
            throw e
        }
    }

    fun stop() {
        process?.let { if (it.isAlive) { transition(DaemonState.STOPPING); it.destroyForcibly() } }
        process = null
        transition(DaemonState.STOPPED)
    }

    private fun watchProcess(watched: Process) {
        Thread {
            try { watched.waitFor() } catch (e: Exception) {}
            if (process == watched) {
                process = null
                if (_status.value.state != DaemonState.FAILED) transition(DaemonState.STOPPED)
            }
        }.apply { isDaemon = true; start() }
    }

    private fun transition(next: DaemonState, message: String = "") {
        _status.value = DaemonStatus(next, message)
    }

    companion object {
        fun defaultAppBaseDir(): String = try {
            Paths.get("/proc/self/exe").toRealPath().parent.toString()
        } catch (e: Exception) {
            System.getProperty("user.dir")
        }

        fun resolveExecutablePath(settings: AppSettings, appBaseDir: String, searchPath: Boolean = true): String? {
            val configured = settings.daemonPath.trim()
            if (configured.isNotEmpty() && File(configured).exists()) return configured
            if (searchPath) {
                val pathExec = System.getenv("PATH").split(File.pathSeparator).map { File(it, "clambhook") }
                    .firstOrNull { it.exists() }?.absolutePath
                if (pathExec != null && pathExec.isNotEmpty()) return pathExec
            }
            val bundled = File(appBaseDir, "clambhook")
            return if (bundled.exists()) bundled.absolutePath else null
        }

        fun buildArguments(settings: AppSettings, token: String, licensePath: String? = null): String {
            return buildArgv(settings, token, licensePath).joinToString(" ") { if (it.startsWith("-")) it else "\"${it.replace("\"", "\\\"")}\"" }
        }

        fun buildArgv(settings: AppSettings, token: String, licensePath: String? = null): List<String> {
            val normalized = settings.normalized()
            val args = mutableListOf("-api", apiListenAddress(normalized.apiEndpoint))
            val trimmedToken = token.trim()
            if (trimmedToken.isNotEmpty()) { args.add("-api-token"); args.add(trimmedToken) }
            if (normalized.configPath.isNotEmpty()) { args.add("-config"); args.add(normalized.configPath) }
            if (licensePath != null && licensePath.trim().isNotEmpty()) { args.add("-license"); args.add(licensePath.trim()) }
            return args
        }

        fun apiListenAddress(endpoint: String): String {
            return try {
                val uri = java.net.URI(endpoint)
                val host = uri.host ?: ""
                val port = uri.port
                val addressHost = if (host.contains(":") && !host.startsWith("[")) "[$host]" else host
                if (host.isNotEmpty()) {
                    if (port >= 0) "$addressHost:$port" else addressHost
                } else endpoint
            } catch (e: Exception) {
                endpoint
            }
        }
    }
}