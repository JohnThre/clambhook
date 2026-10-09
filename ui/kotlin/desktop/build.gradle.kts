// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    id("org.jetbrains.kotlin.jvm")
    id("org.jetbrains.kotlin.plugin.compose")
    id("org.jetbrains.compose")
}


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
        // `./gradlew :desktop:run` for local development.
        mainClass = "com.clambhook.desktop.MainKt"
        jvmArgs += listOf("-Dfile.encoding=UTF-8")
    }
}

// GNU/Linux distributable for the Ubuntu and Fedora packages:
//   build/linux-dist/clambhook-ui/{bin/clambhook-ui, lib/app/*.jar, lib/runtime/}
// The private runtime is produced with jlink from the build JDK (Ubuntu's
// OpenJDK 21 does not ship jpackage, and Fedora 44 ships OpenJDK 25), so the
// packages never depend on a system JRE. debian/ and the RPM spec install the
// tree under /usr/lib/clambhook/ui and link /usr/bin/clambhook-ui to the launcher.
val linuxRuntimeModules = listOf(
    "java.base", "java.datatransfer", "java.desktop", "java.instrument", "java.logging",
    "java.management", "java.naming", "java.net.http", "java.prefs", "java.scripting",
    "java.security.jgss", "java.sql", "java.xml", "jdk.accessibility", "jdk.crypto.ec",
    "jdk.unsupported", "jdk.zipfs",
)
val buildJdkHome = providers.environmentVariable("JAVA_HOME").orElse(providers.systemProperty("java.home"))
val linuxRuntimeDir = layout.buildDirectory.dir("linux-dist/runtime")

val linuxRuntime by tasks.registering(Exec::class) {
    description = "Creates the private jlink runtime for the GNU/Linux controller."
    val output = linuxRuntimeDir.get().asFile
    inputs.property("modules", linuxRuntimeModules)
    inputs.property("jdk", buildJdkHome)
    outputs.dir(output)
    doFirst { output.deleteRecursively() }
    executable = File(buildJdkHome.get(), "bin/jlink").path
    args(
        "--add-modules", linuxRuntimeModules.joinToString(","),
        "--strip-debug", "--no-header-files", "--no-man-pages",
        "--output", output.path,
    )
}

val linuxDistribution by tasks.registering(Sync::class) {
    description = "Assembles the self-contained clambhook-ui tree for Ubuntu and Fedora packages."
    dependsOn(linuxRuntime)
    into(layout.buildDirectory.dir("linux-dist/clambhook-ui"))
    from(layout.projectDirectory.file("src/linux/clambhook-ui")) {
        into("bin")
        filePermissions { unix("rwxr-xr-x") }
    }
    from(tasks.named("jar")) { into("lib/app") }
    from(configurations.named("runtimeClasspath")) { into("lib/app") }
    from(linuxRuntimeDir) { into("lib/runtime") }
}
