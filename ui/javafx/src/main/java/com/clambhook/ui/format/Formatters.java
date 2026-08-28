// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.format;

import java.util.Locale;

public final class Formatters {
    private static final String[] BYTE_UNITS = {"B", "KiB", "MiB", "GiB", "TiB"};

    private Formatters() {
    }

    public static String bytes(long value) {
        double amount = Math.max(0, value);
        int unit = 0;
        while (amount >= 1024.0 && unit < BYTE_UNITS.length - 1) {
            amount /= 1024.0;
            unit++;
        }
        return unit == 0
                ? String.format(Locale.ROOT, "%.0f %s", amount, BYTE_UNITS[unit])
                : String.format(Locale.ROOT, "%.1f %s", amount, BYTE_UNITS[unit]);
    }

    public static String rate(double value) {
        return bytes((long) Math.max(0, value)) + "/s";
    }

    public static String count(long value, String singular, String plural) {
        return value + " " + (value == 1 ? singular : plural);
    }
}
