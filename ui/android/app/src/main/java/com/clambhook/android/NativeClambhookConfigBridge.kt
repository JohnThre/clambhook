package com.clambhook.android

/** File-backed C config queries used before Android establishes its VPN TUN. */
internal object NativeClambhookConfigBridge {
    @Synchronized
    fun importReview(importText: String): String = nativeImportReview(importText)

    @Synchronized
    fun applyReviewedImport(configPath: String, requestJson: String) =
        nativeApplyReviewedImport(configPath, requestJson)

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

    init {
        System.loadLibrary("clambhook_jni")
    }
}
