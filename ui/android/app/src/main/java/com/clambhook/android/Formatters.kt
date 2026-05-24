package com.clambhook.android

import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

fun formatRate(bytesPerSecond: Double): String {
    val units = listOf("B/s", "KB/s", "MB/s", "GB/s")
    var value = bytesPerSecond
    var unit = 0
    while (value >= 1024.0 && unit < units.lastIndex) {
        value /= 1024.0
        unit += 1
    }
    return if (unit == 0) {
        "${value.toInt()} ${units[unit]}"
    } else {
        "%.1f %s".format(value, units[unit])
    }
}

fun formatBytes(bytes: Long): String {
    val units = listOf("B", "KB", "MB", "GB")
    var value = bytes.toDouble()
    var unit = 0
    while (value >= 1024.0 && unit < units.lastIndex) {
        value /= 1024.0
        unit += 1
    }
    return if (unit == 0) {
        "${value.toLong()} ${units[unit]}"
    } else {
        "%.1f %s".format(value, units[unit])
    }
}

fun formatDurationNs(ns: Long): String {
    if (ns <= 0) return "--"
    val seconds = ns / 1_000_000_000
    return when {
        seconds < 1 -> "${ns / 1_000_000} ms"
        seconds < 60 -> "$seconds s"
        else -> "${seconds / 60} min"
    }
}

fun formatUpdatedAt(epochMillis: Long): String {
    if (epochMillis <= 0) return "Never"
    val time = Instant.ofEpochMilli(epochMillis).atZone(ZoneId.systemDefault())
    return DateTimeFormatter.ofPattern("HH:mm:ss").format(time)
}

fun serverLocation(server: ServerPayload): String {
    return listOf(server.geo.city, server.geo.country)
        .filter { it.isNotBlank() }
        .joinToString(", ")
        .ifBlank { server.address }
}
