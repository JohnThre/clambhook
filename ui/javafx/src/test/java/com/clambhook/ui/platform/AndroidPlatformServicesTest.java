// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.platform;

import org.junit.jupiter.api.Test;

import java.util.EnumSet;
import java.util.Set;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;

final class AndroidPlatformServicesTest {
    @Test
    void advertisesAndroidGlueWithoutLinuxDaemonSupervision() {
        try (AndroidPlatformServices services = new AndroidPlatformServices()) {
            Set<PlatformServices.Capability> expected =
                    EnumSet.allOf(PlatformServices.Capability.class);
            expected.remove(PlatformServices.Capability.DAEMON_SUPERVISION);
            assertEquals(expected, services.capabilities());
            assertFalse(services.supports(
                    PlatformServices.Capability.DAEMON_SUPERVISION));
        }
    }
}
