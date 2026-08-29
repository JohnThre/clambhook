// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.backend;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertInstanceOf;

final class BackendFactoryTest {
    @Test
    void selectsOnlyTheBackendForTheCurrentPlatform() {
        String previousPlatform = System.getProperty("javafx.platform");
        String previousUrl = System.getProperty("clambhook.apiUrl");
        try {
            System.setProperty("javafx.platform", "android");
            try (Backend backend = BackendFactory.create()) {
                assertInstanceOf(AndroidBackend.class, backend);
            }

            System.setProperty("javafx.platform", "desktop");
            System.setProperty("clambhook.apiUrl", "http://127.0.0.1:19090");
            try (Backend backend = BackendFactory.create()) {
                assertInstanceOf(HttpBackend.class, backend);
            }
        } finally {
            restore("javafx.platform", previousPlatform);
            restore("clambhook.apiUrl", previousUrl);
        }
    }

    private static void restore(String name, String value) {
        if (value == null) {
            System.clearProperty(name);
        } else {
            System.setProperty(name, value);
        }
    }
}
