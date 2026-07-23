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
    implementation("androidx.compose.material:material-icons-core:1.7.8")
    implementation("androidx.compose.material:material-icons-extended:1.7.8")
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