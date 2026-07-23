import org.jetbrains.compose.desktop.application.dsl.TargetFormat

plugins {
    kotlin("jvm") version "2.3.20"
    kotlin("plugin.compose") version "2.3.20"
    kotlin("plugin.serialization") version "2.3.20"
    id("org.jetbrains.compose") version "1.9.0"
}

group = "com.clambhook"
version = "1.0.1"

kotlin {
    jvmToolchain(17)
}

// Paths to the prebuilt Go daemon binaries, passed by the Makefile as
// -PclambhookDaemon=... -PclambhookTui=... -PclambhookLicense=...
val clambhookDaemon = project.findProperty("clambhookDaemon")?.toString() ?: ""
val clambhookTui = project.findProperty("clambhookTui")?.toString() ?: ""
val clambhookLicense = project.findProperty("clambhookLicense")?.toString() ?: ""

dependencies {
    implementation("org.jetbrains.compose.desktop:desktop-jvm:1.9.0") {
        exclude(group = "org.jetbrains.compose.material")
    }
    implementation("org.jetbrains.compose.material3:material3:1.9.0")
    implementation("androidx.compose.material:material-icons-core:1.7.8") { exclude(group = "androidx.compose.ui"); exclude(group = "androidx.lifecycle"); exclude(group = "androidx.compose.runtime"); exclude(group = "androidx.compose.animation"); exclude(group = "androidx.compose.foundation") }
    implementation("androidx.compose.material:material-icons-extended:1.7.8") { exclude(group = "androidx.compose.ui"); exclude(group = "androidx.lifecycle"); exclude(group = "androidx.compose.runtime"); exclude(group = "androidx.compose.animation"); exclude(group = "androidx.compose.foundation") }
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.9.0")
    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.7.3")
    implementation("com.squareup.okhttp3:okhttp:4.12.0")

    testImplementation(kotlin("test"))
    testImplementation("org.jetbrains.kotlinx:kotlinx-coroutines-test:1.9.0")
}


compose.desktop {
    application {
        mainClass = "com.clambhook.linux.MainKt"
        nativeDistributions {
            targetFormats(TargetFormat.Deb, TargetFormat.Rpm, TargetFormat.AppImage)
            packageName = "clambhook-linux"
            description = "ClambHook GNU/Linux desktop controller"
            vendor = "Pengfan Chang"
            linux {
                iconFile.set(rootProject.file("../../clambhook-icon-1024.png"))
            }
            // Bundle the prebuilt daemon binaries into the distribution's
            // app/bin directory so the controller can find them at runtime.
            appResourcesRootDir.set(layout.projectDirectory.dir("resources"))
        }
    }
}

// Stage the daemon/TUI/license binaries into the resources directory so
// the native distribution bundles them.
tasks.register("stageDaemonBinaries") {
    doLast {
        val resDir = layout.projectDirectory.dir("resources/app/bin").asFile
        resDir.mkdirs()
        listOf(
            "clambhook" to clambhookDaemon,
            "clambhook-tui" to clambhookTui,
            "clambhook-license" to clambhookLicense
        ).forEach { (name, path) ->
            if (path.isNotEmpty() && file(path).exists()) {
                file(path).copyTo(file("$resDir/$name"), overwrite = true)
                file("$resDir/$name").setExecutable(true)
            }
        }
    }
}

tasks.matching { it.name.startsWith("package") || it.name == "createDistributable" || it.name == "createRuntimeArchive" }.configureEach { dependsOn("stageDaemonBinaries") }

tasks.test {
    useJUnitPlatform()
}

// Custom installDist task that produces a standard install layout
// (build/install/clambhook-linux/bin/ + lib/) without the Gradle application
// plugin (which conflicts with Compose Multiplatform's run task).
tasks.register("installDist") {
    dependsOn("jar", "stageDaemonBinaries")
    val installDir = layout.buildDirectory.dir("install/clambhook-linux")
    outputs.dir(installDir)
    doLast {
        val binDir = installDir.get().dir("bin").asFile
        val libDir = installDir.get().dir("lib").asFile
        val resDir = installDir.get().dir("resources/app/bin").asFile
        binDir.mkdirs()
        libDir.mkdirs()
        resDir.mkdirs()

        // Copy all resolved runtime classpath artifacts into lib/.
        // Using resolvedConfiguration gets ALL artifacts including
        // platform-specific native libs (.so) that aren't in .files.
        val byBaseName = mutableMapOf<String, java.io.File>()
        configurations.runtimeClasspath.get().resolvedConfiguration.lenientConfiguration.allModuleDependencies.forEach { dep ->
            dep.allModuleArtifacts.forEach { art ->
                val f = art.file
                if (f.name.endsWith(".jar") || f.name.endsWith(".so") ||
                    f.name.endsWith(".dylib") || f.name.endsWith(".dll") ||
                    f.name.endsWith(".sha256")) {
                    val base = f.name.substringBeforeLast("-")
                    val ver = f.name.substringAfterLast("-").removeSuffix(".jar").removeSuffix(".so").removeSuffix(".sha256")
                    val existing = byBaseName[base]
                    if (existing == null || ver > existing.name.substringAfterLast("-")) {
                        byBaseName[base] = f
                    }
                }
            }
        }
        byBaseName.values.forEach { f -> f.copyTo(file("$libDir/${f.name}"), overwrite = true) }

        // Debug: list all skiko-related resolved artifacts
        configurations.runtimeClasspath.get().resolvedConfiguration.lenientConfiguration.allModuleDependencies.forEach { dep ->
            if (dep.moduleGroup.contains("skiko")) {
                println("installDist: skiko dep: ${dep.moduleGroup}:${dep.moduleName}:${dep.moduleVersion}")
                dep.allModuleArtifacts.forEach { art ->
                    println("  artifact: ${art.file.name} (file=${art.file.isFile}, exists=${art.file.exists()})")
                }
            }
        }

        // Copy the project JAR.
        tasks.jar.get().archiveFile.get().asFile.copyTo(
            file("$libDir/${tasks.jar.get().archiveFileName.get()}"), overwrite = true
        )

        // Generate the launcher script using system java.
        val script = file("$binDir/clambhook-linux")
        script.writeText("""#!/bin/sh
APP_HOME=`dirname "${'$'}0"`/..
CLASSPATH="${'$'}APP_HOME/lib/*"
exec java -classpath "${'$'}CLASSPATH" -Dskiko.library.path="${'$'}APP_HOME/lib" com.clambhook.linux.MainKt "${'$'}@"
""")
        script.setExecutable(true)

        // Copy staged daemon binaries.
        val stagedBinaries = layout.projectDirectory.dir("resources/app/bin").asFile
        if (stagedBinaries.exists()) {
            stagedBinaries.walkTopDown().forEach { f ->
                if (f.isFile) {
                    val rel = f.relativeTo(stagedBinaries)
                    f.copyTo(file("$resDir/${rel.path}"), overwrite = true)
                    file("$resDir/${rel.path}").setExecutable(true)
                }
            }
        }
    }
}