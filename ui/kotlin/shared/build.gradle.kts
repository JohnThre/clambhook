// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

import com.android.build.api.dsl.KotlinMultiplatformAndroidLibraryTarget
import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    id("org.jetbrains.kotlin.multiplatform")
    id("org.jetbrains.kotlin.plugin.compose")
    id("org.jetbrains.kotlin.plugin.serialization")
    id("org.jetbrains.compose")
}

val composeVersion = "1.11.1"
val androidEnabled = (gradle as ExtensionAware).extensions.extraProperties["clambhook.android"] as Boolean

if (androidEnabled) {
    apply(plugin = "com.android.kotlin.multiplatform.library")
}

kotlin {
    if (androidEnabled) {
        // Applied imperatively, so the generated `android {}` accessor is unavailable.
        (this as ExtensionAware).extensions.configure<KotlinMultiplatformAndroidLibraryTarget>("android") {
            namespace = "com.clambhook.ui"
            compileSdk = 36
            minSdk = 31
            compilerOptions {
                jvmTarget.set(JvmTarget.JVM_17)
            }
        }
    }

    jvm("desktop") {
        compilerOptions {
            jvmTarget.set(JvmTarget.JVM_17)
        }
    }

    sourceSets {
        commonMain.dependencies {
            implementation("org.jetbrains.compose.runtime:runtime:$composeVersion")
            implementation("org.jetbrains.compose.foundation:foundation:$composeVersion")
            implementation("org.jetbrains.compose.ui:ui:$composeVersion")
            implementation("org.jetbrains.compose.material3:material3:1.9.0")
            implementation("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.11.0")
            implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.11.0")
        }
        commonTest.dependencies {
            implementation(kotlin("test"))
            implementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.11.0")
        }
        if (androidEnabled) {
            androidMain.dependencies {
                implementation(project(":platform"))
            }
        }
        val desktopMain by getting {
            dependencies {
                implementation("com.squareup.okhttp3:okhttp:5.4.0")
                implementation("org.jetbrains.kotlinx:kotlinx-coroutines-swing:1.11.0")
            }
        }
        val desktopTest by getting {
            dependencies {
                implementation(compose.desktop.currentOs)
                implementation("org.jetbrains.compose.ui:ui-test:$composeVersion")
                implementation("com.squareup.okhttp3:mockwebserver3:5.4.0")
            }
        }
    }
}
