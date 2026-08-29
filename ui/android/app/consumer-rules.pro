# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

# Gluon reaches this Java-facing boundary through reflection.
-keep class com.clambhook.android.GluonPlatformFacade { public static *; }
-keep class com.clambhook.android.ClambhookPlatformInitializer { *; }
-keep class com.clambhook.android.ClambhookVpnService { *; }
-keep class com.clambhook.android.VpnConsentActivity { *; }
-keep class com.clambhook.android.QrScanActivity { *; }
