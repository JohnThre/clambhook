// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.platform;

import com.clambhook.ui.runtime.RuntimeClient;
import org.junit.jupiter.api.Test;

import java.lang.reflect.Proxy;

import static org.junit.jupiter.api.Assertions.assertInstanceOf;

final class PlatformServicesFactoryTest {
    @Test
    void selectsOnlyTheServicesForTheCurrentPlatform() {
        String previousPlatform = System.getProperty("javafx.platform");
        RuntimeClient runtime = (RuntimeClient) Proxy.newProxyInstance(
                RuntimeClient.class.getClassLoader(),
                new Class<?>[]{RuntimeClient.class},
                (proxy, method, arguments) -> null);
        try {
            System.setProperty("javafx.platform", "android");
            try (PlatformServices services = PlatformServicesFactory.create(runtime)) {
                assertInstanceOf(AndroidPlatformServices.class, services);
            }

            System.setProperty("javafx.platform", "desktop");
            try (PlatformServices services = PlatformServicesFactory.create(runtime)) {
                assertInstanceOf(DesktopPlatformServices.class, services);
            }
        } finally {
            if (previousPlatform == null) {
                System.clearProperty("javafx.platform");
            } else {
                System.setProperty("javafx.platform", previousPlatform);
            }
        }
    }
}
