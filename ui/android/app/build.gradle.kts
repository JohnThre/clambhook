import java.io.FileInputStream
import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
    id("org.jetbrains.kotlin.plugin.serialization")
}

val keystorePropertiesFile = rootProject.file("keystore.properties")
val keystoreProperties = Properties().apply {
    if (keystorePropertiesFile.exists()) {
        FileInputStream(keystorePropertiesFile).use { load(it) }
    }
}
val managedDeviceTestAbi = providers.gradleProperty("clambhook.managedDeviceTestAbi").orNull
val repositoryRoot = rootProject.layout.projectDirectory.dir("../..")
val generatedThirdPartyNoticesDirectory =
    layout.buildDirectory.dir("generated/thirdPartyNotices")

android {
    namespace = "com.clambhook.android"
    compileSdk = 36

    defaultConfig {
        applicationId = "org.jpfchang.clambhook"
        minSdk = 30
        targetSdk = 36
        versionCode = 3
        versionName = "1.0.2"
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        externalNativeBuild {
            cmake {
                arguments += "-DANDROID_STL=none"
            }
        }
        if (!managedDeviceTestAbi.isNullOrBlank()) {
            // Managed devices in this matrix are arm64. Filtering only when
            // the explicit QA property is present keeps the large transitional
            // gomobile APK below emulator install timeouts without changing
            // release ABI coverage.
            ndk {
                abiFilters += managedDeviceTestAbi
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
        compose = true
    }

    sourceSets.getByName("main").assets.srcDir(generatedThirdPartyNoticesDirectory)

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    packaging {
        resources.excludes += "/META-INF/{AL2.0,LGPL2.1}"
    }
    signingConfigs {
        create("release") {
            if (keystorePropertiesFile.exists()) {
                storeFile = file(keystoreProperties.getProperty("storeFile"))
                storePassword = keystoreProperties.getProperty("storePassword")
                keyAlias = keystoreProperties.getProperty("keyAlias")
                keyPassword = keystoreProperties.getProperty("keyPassword")
            }
        }
    }

    buildTypes {
        debug {
            if (!managedDeviceTestAbi.isNullOrBlank()) {
                // Instrumented compatibility tests exercise application
                // classes directly, so preserve those names while shrinking
                // the otherwise enormous unreferenced icon/dependency graph.
                isMinifyEnabled = true
                proguardFiles(
                    getDefaultProguardFile("proguard-android-optimize.txt"),
                    "proguard-rules.pro",
                    "managed-test-proguard-rules.pro",
                )
                testProguardFiles("managed-test-proguard-rules.pro")
            }
        }
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
            if (keystorePropertiesFile.exists()) {
                signingConfig = signingConfigs.getByName("release")
            }
        }
    }

    // ClambHook is distributed only via clambercloud.com (sideload), never Play.
    // The Play dependency-info blob is unnecessary and its collector task reads
    // the generated AAR without a declared dependency, breaking Gradle 9 builds.
    dependenciesInfo {
        includeInApk = false
        includeInBundle = false
    }

    // Android 11 is the compatibility floor. Keep instrumented Compose tests
    // running on the floor, a representative middle release, and the current
    // target so platform behavior cannot drift unnoticed during JNI cutover.
    testOptions {
        animationsDisabled = true
        managedDevices {
            localDevices {
                create("pixel2Api30") {
                    device = "Pixel 2"
                    apiLevel = 30
                    systemImageSource = "aosp"
                }
                create("pixel6Api33") {
                    device = "Pixel 6"
                    apiLevel = 33
                    systemImageSource = "aosp"
                }
                create("pixel6Api36") {
                    device = "Pixel 6"
                    apiLevel = 36
                    systemImageSource = "aosp"
                }
            }
            groups {
                create("androidCompatibility") {
                    targetDevices.add(allDevices["pixel2Api30"])
                    targetDevices.add(allDevices["pixel6Api33"])
                    targetDevices.add(allDevices["pixel6Api36"])
                }
            }
        }
    }

}

val clambhookMobileAar = layout.projectDirectory.file("libs/clambhookmobile.aar")
val generateClambhookMobileAar = tasks.register<Exec>("generateClambhookMobileAar") {
    workingDir = repositoryRoot.asFile
    commandLine(
        repositoryRoot.file("scripts/build-android-mobile-aar.sh").asFile.absolutePath,
        clambhookMobileAar.asFile.absolutePath
    )
    outputs.file(clambhookMobileAar)
}

val generateThirdPartyNotices = tasks.register<Sync>("generateThirdPartyNotices") {
    from(repositoryRoot.file("THIRD_PARTY_NOTICES.md"))
    from(repositoryRoot.file("third_party/openssl/LICENSE.txt")) {
        into("licenses/openssl")
    }
    into(generatedThirdPartyNoticesDirectory)
}

// Every build task transitively depends on preBuild, so wiring the AAR
// generator here makes it a proper prerequisite for all consumers (assemble,
// R8, lint, dependency collection) without enumerating each one.
tasks.named("preBuild") {
    dependsOn(generateClambhookMobileAar)
    dependsOn(generateThirdPartyNotices)
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

dependencies {
    val composeBom = platform("androidx.compose:compose-bom:2025.12.00")
    implementation(composeBom)
    testImplementation(composeBom)
    androidTestImplementation(composeBom)

    implementation("androidx.activity:activity-compose:1.12.2")
    implementation("androidx.compose.material:material-icons-core:1.7.8")
    implementation("androidx.compose.material:material-icons-extended:1.7.8")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.datastore:datastore-preferences:1.1.1")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.9.4")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.9.4")
    implementation("androidx.security:security-crypto:1.1.0-alpha06")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.9.0")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.7.3")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
    implementation(fileTree("libs") { include("*.aar") })

    debugImplementation("androidx.compose.ui:ui-tooling")
    debugImplementation("androidx.compose.ui:ui-test-manifest")

    testImplementation("junit:junit:4.13.2")
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.9.0")

    androidTestImplementation("androidx.test.ext:junit:1.2.1")
    androidTestImplementation("androidx.test:runner:1.6.2")
    androidTestImplementation("androidx.compose.ui:ui-test-junit4")
}
