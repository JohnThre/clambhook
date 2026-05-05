package com.clambhook.android

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

fun serverLocation(server: ServerPayload): String {
    return listOf(server.geo.city, server.geo.country)
        .filter { it.isNotBlank() }
        .joinToString(", ")
        .ifBlank { server.address }
}
