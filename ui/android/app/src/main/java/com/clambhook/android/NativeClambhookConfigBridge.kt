// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package com.clambhook.android

/** File-backed C config queries used before Android establishes its VPN TUN. */
internal object NativeClambhookConfigBridge {
    @Synchronized
    fun importReview(importText: String): String = nativeImportReview(importText)

    @Synchronized
    fun applyReviewedImport(configPath: String, requestJson: String) =
        nativeApplyReviewedImport(configPath, requestJson)

    @Synchronized
    fun importConfig(configPath: String, document: String): String =
        nativeImportConfig(configPath, document)

    @Synchronized
    fun setActiveProfile(configPath: String, requestJson: String): String =
        nativeSetActiveProfile(configPath, requestJson)

    @Synchronized
    fun query(configPath: String, operation: String, requestJson: String = "{}"): String =
        nativeQueryConfig(configPath, operation, requestJson)

    @Synchronized
    fun mutate(
        configPath: String,
        mutation: String,
        responseOperation: String,
        requestJson: String,
    ): String = nativeMutateConfig(configPath, mutation, responseOperation, requestJson)

    @Synchronized
    fun outlineReview(requestJson: String): String = nativeOutlineReview(requestJson)

    @Synchronized
    fun outlineImport(configPath: String, requestJson: String): String =
        nativeOutlineImport(configPath, requestJson)

    @Synchronized
    fun outlineRefresh(configPath: String, requestJson: String): String =
        nativeOutlineRefresh(configPath, requestJson)

    private external fun nativeQueryConfig(
        configPath: String,
        operation: String,
        requestJson: String,
    ): String
    private external fun nativeMutateConfig(
        configPath: String,
        mutation: String,
        responseOperation: String,
        requestJson: String,
    ): String
    private external fun nativeImportReview(importText: String): String
    private external fun nativeApplyReviewedImport(configPath: String, requestJson: String)
    private external fun nativeImportConfig(configPath: String, document: String): String
    private external fun nativeSetActiveProfile(configPath: String, requestJson: String): String
    private external fun nativeOutlineReview(requestJson: String): String
    private external fun nativeOutlineImport(configPath: String, requestJson: String): String
    private external fun nativeOutlineRefresh(configPath: String, requestJson: String): String

    init {
        System.loadLibrary("clambhook_jni")
    }
}
