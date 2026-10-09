// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.plugin.compose")
}

val versionNameValue = providers.environmentVariable("VERSION").orElse("1.0.2").get()
val versionCodeValue = providers.environmentVariable("VERSION_CODE").orElse("3").get().toInt()
val releaseKeystore = providers.environmentVariable("CLAMBHOOK_ANDROID_KEYSTORE_PATH").orNull

android {
    namespace = "org.jpfchang.clambhook"
    compileSdk = 36
    buildToolsVersion = "36.0.0"

    defaultConfig {
        // Locked identity: existing installs upgrade in place only when the
        // application ID and the release keystore stay unchanged.
        applicationId = "org.jpfchang.clambhook"
        minSdk = 31
        targetSdk = 36
        versionCode = versionCodeValue
        versionName = versionNameValue
        ndk {
            // Product APK/AAB output is ARM64-only by product decision.
            abiFilters += "arm64-v8a"
        }
    }

    signingConfigs {
        if (releaseKeystore != null) {
            create("release") {
                storeFile = file(releaseKeystore)
                storePassword = providers.environmentVariable("CLAMBHOOK_ANDROID_KEYSTORE_PASSWORD").get()
                keyAlias = providers.environmentVariable("CLAMBHOOK_ANDROID_KEY_ALIAS").get()
                keyPassword = providers.environmentVariable("CLAMBHOOK_ANDROID_KEY_PASSWORD").get()
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            if (releaseKeystore != null) {
                signingConfig = signingConfigs.getByName("release")
            }
        }
    }

    buildFeatures {
        compose = true
        buildConfig = false
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    packaging {
        resources.excludes += "/META-INF/{AL2.0,LGPL2.1}"
        resources.excludes += "/META-INF/versions/9/OSGI-INF/MANIFEST.MF"
        // bcprov, bcutil, and bcpg ship the same MIT license text; keep one.
        resources.pickFirsts += "/META-INF/LICENSE.md"
    }

    lint {
        abortOnError = true
        checkReleaseBuilds = true
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

dependencies {
    implementation(project(":shared"))
    implementation(project(":platform"))
    implementation("androidx.activity:activity-compose:1.13.0")
    implementation("androidx.core:core-ktx:1.18.0")
}
