#include <jni.h>

#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>

#include "clambhook/license_json.h"

static void license_jni_throw(JNIEnv *environment, const char *message) {
    jclass exception = (*environment)->FindClass(
        environment, "java/lang/IllegalStateException");
    if (exception != NULL) {
        (*environment)->ThrowNew(environment, exception,
                                 message == NULL ? "native license error" :
                                 message);
    }
}

static const char *license_jni_utf(JNIEnv *environment, jstring value) {
    return value == NULL ? NULL :
        (*environment)->GetStringUTFChars(environment, value, NULL);
}

static void license_jni_release(JNIEnv *environment, jstring value,
                                const char *utf) {
    if (value != NULL && utf != NULL) {
        (*environment)->ReleaseStringUTFChars(environment, value, utf);
    }
}

static jstring license_jni_result(JNIEnv *environment, ch_status status,
                                  char *result, const ch_error *error) {
    if (status != CH_OK || result == NULL) {
        license_jni_throw(environment,
                          error == NULL ? "native license operation failed" :
                          error->message);
        free(result);
        return NULL;
    }
    jstring value = (*environment)->NewStringUTF(environment, result);
    free(result);
    return value;
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_android_NativeClambhookLicenseBridge_nativeNewInstallId(
    JNIEnv *environment, jobject bridge) {
    (void)bridge;
    ch_error error;
    char *result = ch_license_new_install_id(&error);
    return license_jni_result(environment,
                              result == NULL ? error.code : CH_OK,
                              result, &error);
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_android_NativeClambhookLicenseBridge_nativeEnsureTrial(
    JNIEnv *environment, jobject bridge, jstring snapshot,
    jlong now_millis) {
    (void)bridge;
    const char *snapshot_utf = license_jni_utf(environment, snapshot);
    if (snapshot != NULL && snapshot_utf == NULL) return NULL;
    ch_error error;
    char *result = NULL;
    ch_status status = ch_license_ensure_trial_json(
        snapshot_utf, (int64_t)now_millis, &result, &error);
    license_jni_release(environment, snapshot, snapshot_utf);
    return license_jni_result(environment, status, result, &error);
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_android_NativeClambhookLicenseBridge_nativeStatus(
    JNIEnv *environment, jobject bridge, jstring snapshot,
    jlong published_millis, jlong now_millis) {
    (void)bridge;
    const char *snapshot_utf = license_jni_utf(environment, snapshot);
    if (snapshot != NULL && snapshot_utf == NULL) return NULL;
    ch_error error;
    char *result = NULL;
    ch_status status = ch_license_status_json(
        snapshot_utf, (int64_t)published_millis, (int64_t)now_millis,
        &result, &error);
    license_jni_release(environment, snapshot, snapshot_utf);
    return license_jni_result(environment, status, result, &error);
}

JNIEXPORT jboolean JNICALL
Java_com_clambhook_android_NativeClambhookLicenseBridge_nativeUpdateAllowed(
    JNIEnv *environment, jobject bridge, jstring snapshot,
    jlong published_millis, jlong now_millis) {
    (void)bridge;
    const char *snapshot_utf = license_jni_utf(environment, snapshot);
    if (snapshot != NULL && snapshot_utf == NULL) return JNI_FALSE;
    ch_error error;
    bool allowed = false;
    ch_status status = ch_license_update_allowed_json(
        snapshot_utf, (int64_t)published_millis, (int64_t)now_millis,
        &allowed, &error);
    license_jni_release(environment, snapshot, snapshot_utf);
    if (status != CH_OK) {
        license_jni_throw(environment, error.message);
        return JNI_FALSE;
    }
    return allowed ? JNI_TRUE : JNI_FALSE;
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_android_NativeClambhookLicenseBridge_nativeApplyServerResponse(
    JNIEnv *environment, jobject bridge, jstring response,
    jstring install_id, jlong now_millis) {
    (void)bridge;
    const char *response_utf = license_jni_utf(environment, response);
    const char *install_utf = license_jni_utf(environment, install_id);
    if (response_utf == NULL || install_utf == NULL) {
        license_jni_release(environment, response, response_utf);
        license_jni_release(environment, install_id, install_utf);
        return NULL;
    }
    ch_error error;
    char *result = NULL;
    ch_status status = ch_license_apply_server_response_json(
        response_utf, install_utf, (int64_t)now_millis, &result, &error);
    license_jni_release(environment, response, response_utf);
    license_jni_release(environment, install_id, install_utf);
    return license_jni_result(environment, status, result, &error);
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_android_NativeClambhookLicenseBridge_nativeMarkVerificationFailure(
    JNIEnv *environment, jobject bridge, jstring snapshot,
    jlong now_millis) {
    (void)bridge;
    const char *snapshot_utf = license_jni_utf(environment, snapshot);
    if (snapshot != NULL && snapshot_utf == NULL) return NULL;
    ch_error error;
    char *result = NULL;
    ch_status status = ch_license_mark_verification_failure_json(
        snapshot_utf, (int64_t)now_millis, &result, &error);
    license_jni_release(environment, snapshot, snapshot_utf);
    return license_jni_result(environment, status, result, &error);
}
