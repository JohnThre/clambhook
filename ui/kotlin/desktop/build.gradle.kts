// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    id("org.jetbrains.kotlin.jvm")
    id("org.jetbrains.kotlin.plugin.compose")
    id("org.jetbrains.compose")
}

val clambhookVersion = providers.environmentVariable("VERSION").orElse("1.0.2")

kotlin {
    compilerOptions {
        jvmTarget.set(JvmTarget.JVM_17)
    }
}

java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

dependencies {
    implementation(project(":shared"))
    implementation(compose.desktop.currentOs)
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-swing:1.11.0")
}

compose.desktop {
    application {
        mainClass = "com.clambhook.desktop.MainKt"
        jvmArgs += listOf("-Dfile.encoding=UTF-8")
        nativeDistributions {
            // The executable keeps the frozen clambhook-ui name. Packaging into
            // Ubuntu .deb and Fedora .rpm is done by debian/ and the RPM spec,
            // which install this self-contained distributable (bundled jlink
            // runtime, no system JRE) under /usr/lib/clambhook/ui.
            packageName = "clambhook-ui"
            packageVersion = clambhookVersion.get().substringBefore('-').ifBlank { "1.0.2" }
            description = "ClambHook network controller"
            vendor = "Pengfan Chang"
            licenseFile.set(rootProject.file("../../LICENSE"))
            modules("java.net.http", "jdk.crypto.ec", "java.naming")
            linux {
                iconFile.set(rootProject.file("../../packaging/icons/256x256/apps/com.clambhook.Clambhook.png"))
            }
        }
    }
}
