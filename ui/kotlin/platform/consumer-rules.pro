# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# The C runtime is reached through JNI; keep native method names stable.
-keepclasseswithmembernames class com.clambhook.android.** { native <methods>; }
-keep class com.clambhook.android.ClambhookPlatformInitializer { *; }
-keep class com.clambhook.android.ClambhookVpnService { *; }
-keep class com.clambhook.android.VpnConsentActivity { *; }
-keep class com.clambhook.android.QrScanActivity { *; }
