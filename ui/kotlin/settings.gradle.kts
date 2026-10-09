// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "ClambHook"

// Android modules need an Android SDK. Ubuntu/Fedora package builds only build
// the desktop controller, so they run without one (or pass
// -Pclambhook.desktopOnly=true explicitly).
val desktopOnly = providers.gradleProperty("clambhook.desktopOnly").orNull == "true"
val localSdk = file("local.properties").takeIf { it.isFile }?.readLines()
    ?.firstOrNull { it.startsWith("sdk.dir=") }?.substringAfter("sdk.dir=")
val androidSdk = listOfNotNull(
    providers.environmentVariable("ANDROID_HOME").orNull,
    providers.environmentVariable("ANDROID_SDK_ROOT").orNull,
    localSdk,
).any { file(it).isDirectory }
val androidEnabled = !desktopOnly && androidSdk
(gradle as ExtensionAware).extensions.extraProperties["clambhook.android"] = androidEnabled

// Shared Kotlin/Compose Multiplatform UI, runtime client, and platform services.
include(":shared")
// GNU/Linux desktop application (Ubuntu and Fedora).
include(":desktop")
if (androidEnabled) {
    // Android framework integration (VpnService, JNI C runtime, licensing, updater).
    include(":platform")
    // Android application (org.jpfchang.clambhook).
    include(":app")
}
