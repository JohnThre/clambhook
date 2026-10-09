// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.format

import kotlin.math.roundToLong

object Formatters {
    private val BYTE_UNITS = listOf("B", "KiB", "MiB", "GiB", "TiB")

    fun bytes(value: Long): String {
        var amount = value.coerceAtLeast(0).toDouble()
        var unit = 0
        while (amount >= 1024.0 && unit < BYTE_UNITS.lastIndex) {
            amount /= 1024.0
            unit++
        }
        if (unit == 0) return "${amount.roundToLong()} ${BYTE_UNITS[unit]}"
        val tenths = (amount * 10.0).roundToLong()
        return "${tenths / 10}.${tenths % 10} ${BYTE_UNITS[unit]}"
    }

    fun rate(value: Double): String = bytes(value.coerceAtLeast(0.0).toLong()) + "/s"

    fun count(value: Long, singular: String, plural: String): String =
        "$value ${if (value == 1L) singular else plural}"
}
