// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.serialization")
}

val repositoryRoot = rootProject.layout.projectDirectory.dir("../..")
val generatedThirdPartyNoticesDirectory =
    layout.buildDirectory.dir("generated/thirdPartyNotices")

base {
    archivesName.set("clambhook-android-platform")
}

android {
    namespace = "com.clambhook.android"
    compileSdk = 36

    defaultConfig {
        minSdk = 31
        testApplicationId = "org.jpfchang.clambhook.platform.test"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        consumerProguardFiles("consumer-rules.pro")
        ndk {
            // Gluon Android packages are AArch64-only by product decision.
            abiFilters += "arm64-v8a"
        }
        externalNativeBuild {
            cmake {
                arguments += "-DANDROID_STL=none"
            }
        }
    }

    externalNativeBuild {
        cmake {
            path = file("src/main/cpp/CMakeLists.txt")
            version = "3.22.1"
        }
    }

    buildFeatures {
        buildConfig = false
    }

    sourceSets.getByName("main").assets.srcDir(generatedThirdPartyNoticesDirectory)

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    packaging {
        resources.excludes += "/META-INF/{AL2.0,LGPL2.1}"
    }

    buildTypes {
        debug {
            isMinifyEnabled = false
        }
        release {
            isMinifyEnabled = false
        }
    }

    testOptions {
        targetSdk = 36
        animationsDisabled = true
        managedDevices {
            localDevices {
                create("pixel2Api31") {
                    device = "Pixel 2"
                    apiLevel = 31
                    systemImageSource = "aosp-atd"
                    require64Bit = true
                }
                create("pixel6Api33") {
                    device = "Pixel 6"
                    apiLevel = 33
                    systemImageSource = "aosp-atd"
                    require64Bit = true
                }
                create("pixel6Api36") {
                    device = "Pixel 6"
                    apiLevel = 36
                    systemImageSource = "aosp-atd"
                    require64Bit = true
                }
            }
            groups {
                create("androidCompatibility") {
                    targetDevices.add(allDevices["pixel2Api31"])
                    targetDevices.add(allDevices["pixel6Api33"])
                    targetDevices.add(allDevices["pixel6Api36"])
                }
            }
        }
    }
}

val generateThirdPartyNotices = tasks.register<Sync>("generateThirdPartyNotices") {
    from(repositoryRoot.file("LICENSE"))
    from(repositoryRoot.file("LICENSE-APACHE"))
    from(repositoryRoot.file("LICENSING.md"))
    from(repositoryRoot.file("NOTICE"))
    from(repositoryRoot.file("TRADEMARKS.md"))
    from(repositoryRoot.file("THIRD_PARTY_NOTICES.md"))
    from(repositoryRoot.file("third_party/openssl/LICENSE.txt")) {
        into("licenses/openssl")
    }
    from(repositoryRoot.file("third_party/curl/LICENSE.txt")) {
        into("licenses/curl")
    }
    into(generatedThirdPartyNoticesDirectory)
}

tasks.named("preBuild") {
    dependsOn(generateThirdPartyNotices)
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

dependencies {
    implementation("androidx.core:core-ktx:1.19.0")
    implementation("androidx.datastore:datastore-preferences:1.1.1")
    implementation("androidx.security:security-crypto:1.1.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.11.0")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.11.0")
    implementation("com.squareup.okhttp3:okhttp:5.5.0")
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")

    testImplementation("junit:junit:4.13.2")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.11.0")

    androidTestImplementation("androidx.test:core:1.7.0")
    androidTestImplementation("androidx.test.ext:junit:1.3.0")
    androidTestImplementation("androidx.test:runner:1.7.0")
}
