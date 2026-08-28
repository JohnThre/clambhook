# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Compose instrumentation tests call these application classes and top-level
# functions directly from the separate test APK. Keep their binary names for
# the managed-device-only minified debug variant while allowing R8 to discard
# unused dependency and material-icon classes.
-keep class com.clambhook.android.** { *; }

# The AndroidX instrumentation runner uses Kotlin runtime entry points that
# are not referenced by the application graph R8 sees. The runtime is small
# compared with the removed icon graph, so preserve it across both APKs.
-keep class kotlin.** { *; }
-keep class kotlinx.coroutines.** { *; }

# These libraries span the application and instrumentation APK class loaders.
# Preserve their public/test-runner ABI because R8 analyzes each APK as a
# separate graph and cannot see every reflective or cross-APK call.
-keep class androidx.test.** { *; }
-keep class androidx.compose.ui.** { *; }
-keep class androidx.compose.runtime.** { *; }
-keep class org.junit.** { *; }
-keep class com.google.common.util.concurrent.** { *; }
-keep class androidx.test.espresso.core.internal.deps.guava.** { *; }

# AndroidX Test references these compile-time-only Error Prone annotations.
-dontwarn com.google.errorprone.annotations.CanIgnoreReturnValue
-dontwarn com.google.errorprone.annotations.MustBeClosed
