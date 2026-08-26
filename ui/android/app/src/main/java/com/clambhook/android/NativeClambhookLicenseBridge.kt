package com.clambhook.android

/** JNI boundary for the shared C license evaluator and response verifier. */
internal object NativeClambhookLicenseBridge {
    fun newInstallId(): String = nativeNewInstallId()
    fun ensureTrial(snapshotJson: String, nowMillis: Long = 0): String =
        nativeEnsureTrial(snapshotJson, nowMillis)
    fun status(
        snapshotJson: String,
        publishedMillis: Long = 0,
        nowMillis: Long = 0,
    ): String = nativeStatus(snapshotJson, publishedMillis, nowMillis)
    fun updateAllowed(
        snapshotJson: String,
        publishedMillis: Long,
        nowMillis: Long = 0,
    ): Boolean = nativeUpdateAllowed(snapshotJson, publishedMillis, nowMillis)
    fun applyServerResponse(
        responseJson: String,
        installId: String,
        nowMillis: Long = 0,
    ): String = nativeApplyServerResponse(responseJson, installId, nowMillis)
    fun markVerificationFailure(snapshotJson: String, nowMillis: Long = 0): String =
        nativeMarkVerificationFailure(snapshotJson, nowMillis)

    private external fun nativeNewInstallId(): String
    private external fun nativeEnsureTrial(snapshotJson: String, nowMillis: Long): String
    private external fun nativeStatus(
        snapshotJson: String,
        publishedMillis: Long,
        nowMillis: Long,
    ): String
    private external fun nativeUpdateAllowed(
        snapshotJson: String,
        publishedMillis: Long,
        nowMillis: Long,
    ): Boolean
    private external fun nativeApplyServerResponse(
        responseJson: String,
        installId: String,
        nowMillis: Long,
    ): String
    private external fun nativeMarkVerificationFailure(
        snapshotJson: String,
        nowMillis: Long,
    ): String

    init {
        System.loadLibrary("clambhook_jni")
    }
}
