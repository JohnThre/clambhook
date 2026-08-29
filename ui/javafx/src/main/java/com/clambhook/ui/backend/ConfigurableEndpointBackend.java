// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.backend;

/** Backend whose loopback control endpoint can be changed at runtime. */
public interface ConfigurableEndpointBackend extends Backend {
    String baseUrl();

    void configure(String baseUrl, String bearerToken);
}
