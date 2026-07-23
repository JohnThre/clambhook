import org.jetbrains.compose.desktop.application.dsl.TargetFormat
import java.util.jar.JarFile

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
    dependsOn("createDistributable", "stageDaemonBinaries")
    val installDir = layout.buildDirectory.dir("install/clambhook-linux")
    outputs.dir(installDir)
    doLast {
        val binDir = installDir.get().dir("bin").asFile
        val resDir = installDir.get().dir("resources/app/bin").asFile
        binDir.mkdirs()
        resDir.mkdirs()

        // createDistributable produces a self-contained app directory with a
        // bundled JRE and platform-specific native libs (libskiko-linux-x64.so).
        // On Linux: compose/binaries/main/app/clambhook-linux/{bin,lib/app,lib/runtime}
        // On macOS: compose/binaries/main/app/clambhook-linux.app/Contents/{MacOS,app,runtime}
        // We normalize to a Linux-style layout: bin/, lib/app/, lib/runtime/
        val appBase = layout.buildDirectory.dir("compose/binaries/main/app").get().asFile
        if (appBase.exists()) {
            println("installDist: appBase = ${appBase.absolutePath}")
            appBase.listFiles()?.forEach { println("  entry: ${it.name} (dir=${it.isDirectory})") }
            val appDir = appBase.listFiles()?.firstOrNull { it.isDirectory }
            if (appDir == null) {
                println("installDist: no subdirectory found in appBase, skipping copy")
            } else {
                println("installDist: appDir = ${appDir.absolutePath}")
                val contentsDir = appDir.resolve("Contents")
                val sourceRoot = if (contentsDir.exists()) contentsDir else appDir
                println("installDist: sourceRoot = ${sourceRoot.absolutePath}")
                sourceRoot.walkTopDown().forEach { f ->
                    if (!f.isFile) return@forEach
                    val rel = f.relativeTo(sourceRoot)
                    val target = file("${installDir.get().asFile.path}/$rel")
                    target.parentFile.mkdirs()
                    f.copyTo(target, overwrite = true)
                    if (f.canExecute()) target.setExecutable(true)
                }
            }
        } else {
            println("installDist: appBase does not exist: ${appBase.absolutePath}")
        }

        // Note: jpackage creates a native launcher at bin/clambhook-linux
        // that handles JVM loading, classpath, and native libs automatically.
        // We don't need to generate our own launcher script.
        // The jpackage launcher reads lib/app/clambhook-linux.cfg for classpath
        // and JVM options. We just need to ensure the daemon binaries are in
        // the right place.

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