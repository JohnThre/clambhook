// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

import kotlinx.serialization.json.Json

/** Deterministic JSON policy shared by the Android platform adapters. */
internal val ApiJson =
    Json {
        ignoreUnknownKeys = true
        coerceInputValues = true
        encodeDefaults = true
    }
