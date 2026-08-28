package com.clambhook.ui.backend;

import java.util.Locale;

/** Selects the platform adapter without duplicating any JavaFX view code. */
public final class BackendFactory {
    private BackendFactory() {
    }

    public static Backend create() {
        String osName = System.getProperty("os.name", "").toLowerCase(Locale.ROOT);
        String javafxPlatform = System.getProperty("javafx.platform", "").toLowerCase(Locale.ROOT);
        if (osName.contains("android") || javafxPlatform.contains("android")) {
            return new AndroidBackend();
        }
        String baseUrl = setting("CLAMBHOOK_API_URL", "clambhook.apiUrl", "http://127.0.0.1:9090");
        String token = setting("CLAMBHOOK_API_TOKEN", "clambhook.apiToken", "");
        return new HttpBackend(baseUrl, token);
    }

    private static String setting(String environmentName, String propertyName, String fallback) {
        String property = System.getProperty(propertyName, "").trim();
        if (!property.isEmpty()) {
            return property;
        }
        String environment = System.getenv(environmentName);
        return environment == null || environment.isBlank() ? fallback : environment.trim();
    }
}
