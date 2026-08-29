// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.ui.platform;

import com.clambhook.ui.runtime.RuntimeClient;

import java.util.Locale;

/** Selects Android glue or GNU/Linux desktop services. */
public final class PlatformServicesFactory {
    private PlatformServicesFactory() {
    }

    public static PlatformServices create(RuntimeClient runtime) {
        String osName = System.getProperty("os.name", "").toLowerCase(Locale.ROOT);
        String javafxPlatform = System.getProperty("javafx.platform", "").toLowerCase(Locale.ROOT);
        if (osName.contains("android") || javafxPlatform.contains("android")) {
            return instantiate("AndroidPlatformServices", new Class<?>[0]);
        }
        return instantiate("DesktopPlatformServices",
                new Class<?>[]{RuntimeClient.class}, runtime);
    }

    private static PlatformServices instantiate(String simpleName,
                                                Class<?>[] parameterTypes,
                                                Object... arguments) {
        String className = PlatformServicesFactory.class.getPackageName()
                + "." + simpleName;
        try {
            Class<?> implementation = Class.forName(className);
            return PlatformServices.class.cast(
                    implementation.getConstructor(parameterTypes)
                            .newInstance(arguments));
        } catch (ReflectiveOperationException | ClassCastException error) {
            throw new IllegalStateException(
                    "Cannot create platform services " + className, error);
        }
    }
}
