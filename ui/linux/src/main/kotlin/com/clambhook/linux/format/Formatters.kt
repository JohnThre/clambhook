package com.clambhook.linux.format

import com.clambhook.linux.model.ServerPayload

object Formatters {
    fun formatRate(bytesPerSecond: Double): String {
        val units = arrayOf("B/s", "KB/s", "MB/s", "GB/s")
        var value = bytesPerSecond
        var unit = 0
        while (value >= 1024 && unit < units.size - 1) {
            value /= 1024
            unit++
        }
        return if (unit == 0) "${value.toInt()} ${units[unit]}" else "%.1f ${units[unit]}".format(value)
    }

    fun formatBytes(bytes: ULong): String {
        val units = arrayOf("B", "KB", "MB", "GB")
        var value = bytes.toDouble()
        var unit = 0
        while (value >= 1024 && unit < units.size - 1) {
            value /= 1024
            unit++
        }
        return if (unit == 0) "$bytes ${units[unit]}" else "%.1f ${units[unit]}".format(value)
    }

    fun formatDurationNs(ns: Long): String {
        if (ns <= 0) return "--"
        val seconds = ns / 1_000_000_000
        if (seconds < 1) return "${ns / 1_000_000} ms"
        if (seconds < 60) return "$seconds s"
        return "${seconds / 60} min"
    }

    fun serverLocation(server: ServerPayload): String {
        if (server.geo.city.isNotEmpty() && server.geo.country.isNotEmpty()) {
            return "${server.geo.city}, ${server.geo.country}"
        }
        if (server.geo.country.isNotEmpty()) return server.geo.country
        return server.address
    }
}