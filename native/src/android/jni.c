// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include <jni.h>

#include <limits.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "clambhook/config.h"
#include "clambhook/runtime.h"

typedef struct ch_jni_runtime {
    JavaVM *vm;
    jobject packet_writer;
    jmethodID write_packet;
    ch_runtime *runtime;
} ch_jni_runtime;

static void ch_jni_throw(JNIEnv *environment, const char *class_name, const char *message) {
    jclass error_class = (*environment)->FindClass(environment, class_name);
    if (error_class != NULL) {
        (void)(*environment)->ThrowNew(environment, error_class, message == NULL ? "native error" : message);
        (*environment)->DeleteLocalRef(environment, error_class);
    }
}

static ch_jni_runtime *ch_jni_from_handle(JNIEnv *environment, jlong handle) {
    if (handle == 0) {
        ch_jni_throw(environment, "java/lang/IllegalStateException", "native runtime is closed");
        return NULL;
    }
    return (ch_jni_runtime *)(uintptr_t)handle;
}

static const char *ch_jni_get_utf(JNIEnv *environment, jstring value) {
    if (value == NULL) return NULL;
    return (*environment)->GetStringUTFChars(environment, value, NULL);
}

static void ch_jni_release_utf(JNIEnv *environment, jstring value, const char *characters) {
    if (value != NULL && characters != NULL) {
        (*environment)->ReleaseStringUTFChars(environment, value, characters);
    }
}

static void ch_jni_packet_writer(const uint8_t *packet, size_t length, void *context) {
    ch_jni_runtime *state = context;
    if (state == NULL || state->packet_writer == NULL || length > (size_t)INT_MAX) return;
    JNIEnv *environment = NULL;
    jint attachment = (*state->vm)->GetEnv(state->vm, (void **)&environment, JNI_VERSION_1_6);
    int detach = 0;
    if (attachment == JNI_EDETACHED) {
#if defined(__ANDROID__)
        if ((*state->vm)->AttachCurrentThread(state->vm, &environment, NULL) != JNI_OK) return;
#else
        if ((*state->vm)->AttachCurrentThread(state->vm, (void **)&environment, NULL) != JNI_OK) return;
#endif
        detach = 1;
    } else if (attachment != JNI_OK) {
        return;
    }
    jbyteArray bytes = (*environment)->NewByteArray(environment, (jsize)length);
    if (bytes != NULL) {
        (*environment)->SetByteArrayRegion(
            environment, bytes, 0, (jsize)length, (const jbyte *)packet
        );
        (*environment)->CallVoidMethod(environment, state->packet_writer, state->write_packet, bytes);
        (*environment)->DeleteLocalRef(environment, bytes);
    }
    if ((*environment)->ExceptionCheck(environment)) {
        (*environment)->ExceptionDescribe(environment);
        (*environment)->ExceptionClear(environment);
    }
    if (detach) {
        (void)(*state->vm)->DetachCurrentThread(state->vm);
    }
}

JNIEXPORT jlong JNICALL
Java_com_clambhook_android_NativeClambhookBridge_nativeCreate(
    JNIEnv *environment,
    jobject bridge,
    jobject packet_writer
) {
    (void)bridge;
    if (packet_writer == NULL) {
        ch_jni_throw(environment, "java/lang/IllegalArgumentException", "packet writer is required");
        return 0;
    }
    ch_jni_runtime *state = calloc(1U, sizeof(*state));
    if (state == NULL) {
        ch_jni_throw(environment, "java/lang/OutOfMemoryError", "allocate native runtime bridge");
        return 0;
    }
    if ((*environment)->GetJavaVM(environment, &state->vm) != JNI_OK) {
        free(state);
        ch_jni_throw(environment, "java/lang/IllegalStateException", "resolve Java VM");
        return 0;
    }
    state->packet_writer = (*environment)->NewGlobalRef(environment, packet_writer);
    jclass writer_class = (*environment)->GetObjectClass(environment, packet_writer);
    if (state->packet_writer == NULL || writer_class == NULL) {
        if (state->packet_writer != NULL) (*environment)->DeleteGlobalRef(environment, state->packet_writer);
        free(state);
        ch_jni_throw(environment, "java/lang/OutOfMemoryError", "retain packet writer");
        return 0;
    }
    state->write_packet = (*environment)->GetMethodID(environment, writer_class, "writePacket", "([B)V");
    (*environment)->DeleteLocalRef(environment, writer_class);
    if (state->write_packet == NULL) {
        (*environment)->DeleteGlobalRef(environment, state->packet_writer);
        free(state);
        return 0; /* GetMethodID already raised NoSuchMethodError. */
    }
    ch_runtime_options options = {
        .packet_writer = ch_jni_packet_writer,
        .packet_writer_context = state
    };
    ch_error error;
    state->runtime = ch_runtime_create(&options, &error);
    if (state->runtime == NULL) {
        (*environment)->DeleteGlobalRef(environment, state->packet_writer);
        free(state);
        ch_jni_throw(environment, "java/lang/IllegalStateException", error.message);
        return 0;
    }
    return (jlong)(uintptr_t)state;
}

JNIEXPORT void JNICALL
Java_com_clambhook_android_NativeClambhookBridge_nativeDestroy(
    JNIEnv *environment,
    jobject bridge,
    jlong handle
) {
    (void)bridge;
    ch_jni_runtime *state = ch_jni_from_handle(environment, handle);
    if (state == NULL) return;
    ch_runtime_destroy(state->runtime);
    (*environment)->DeleteGlobalRef(environment, state->packet_writer);
    memset(state, 0, sizeof(*state));
    free(state);
}

static void ch_jni_check(JNIEnv *environment, ch_status status, const ch_error *error) {
    if (status != CH_OK) {
        ch_jni_throw(environment, "java/lang/IllegalStateException", error->message);
    }
}

JNIEXPORT void JNICALL
Java_com_clambhook_android_NativeClambhookBridge_nativeStart(
    JNIEnv *environment, jobject bridge, jlong handle, jstring config_path
) {
    (void)bridge;
    ch_jni_runtime *state = ch_jni_from_handle(environment, handle);
    if (state == NULL) return;
    const char *path = ch_jni_get_utf(environment, config_path);
    if (config_path != NULL && path == NULL) return;
    ch_error error;
    ch_status status = ch_runtime_start(state->runtime, path, &error);
    ch_jni_release_utf(environment, config_path, path);
    ch_jni_check(environment, status, &error);
}

JNIEXPORT void JNICALL
Java_com_clambhook_android_NativeClambhookBridge_nativeStop(
    JNIEnv *environment, jobject bridge, jlong handle
) {
    (void)bridge;
    ch_jni_runtime *state = ch_jni_from_handle(environment, handle);
    if (state == NULL) return;
    ch_error error;
    ch_jni_check(environment, ch_runtime_stop(state->runtime, &error), &error);
}

JNIEXPORT void JNICALL
Java_com_clambhook_android_NativeClambhookBridge_nativeReload(
    JNIEnv *environment, jobject bridge, jlong handle, jstring config_path
) {
    (void)bridge;
    ch_jni_runtime *state = ch_jni_from_handle(environment, handle);
    if (state == NULL) return;
    const char *path = ch_jni_get_utf(environment, config_path);
    if (path == NULL) return;
    ch_error error;
    ch_status status = ch_runtime_reload(state->runtime, path, &error);
    ch_jni_release_utf(environment, config_path, path);
    ch_jni_check(environment, status, &error);
}

JNIEXPORT void JNICALL
Java_com_clambhook_android_NativeClambhookBridge_nativeInjectPacket(
    JNIEnv *environment, jobject bridge, jlong handle, jbyteArray packet
) {
    (void)bridge;
    ch_jni_runtime *state = ch_jni_from_handle(environment, handle);
    if (state == NULL) return;
    if (packet == NULL) {
        ch_jni_throw(environment, "java/lang/IllegalArgumentException", "packet is required");
        return;
    }
    jsize length = (*environment)->GetArrayLength(environment, packet);
    jbyte *bytes = (*environment)->GetByteArrayElements(environment, packet, NULL);
    if (bytes == NULL) return;
    ch_error error;
    ch_status status = ch_runtime_inject_packet(
        state->runtime, (const uint8_t *)bytes, (size_t)length, &error
    );
    (*environment)->ReleaseByteArrayElements(environment, packet, bytes, JNI_ABORT);
    ch_jni_check(environment, status, &error);
}

JNIEXPORT jboolean JNICALL
Java_com_clambhook_android_NativeClambhookBridge_nativeIsRunning(
    JNIEnv *environment, jobject bridge, jlong handle
) {
    (void)bridge;
    ch_jni_runtime *state = ch_jni_from_handle(environment, handle);
    return state != NULL && ch_runtime_is_running(state->runtime) ? JNI_TRUE : JNI_FALSE;
}

static jstring ch_jni_operation(
    JNIEnv *environment,
    jlong handle,
    jstring operation,
    jstring request_json,
    int mutate
) {
    ch_jni_runtime *state = ch_jni_from_handle(environment, handle);
    if (state == NULL) return NULL;
    const char *operation_utf = ch_jni_get_utf(environment, operation);
    const char *request_utf = ch_jni_get_utf(environment, request_json);
    if (operation_utf == NULL || (request_json != NULL && request_utf == NULL)) {
        ch_jni_release_utf(environment, operation, operation_utf);
        ch_jni_release_utf(environment, request_json, request_utf);
        return NULL;
    }
    char *response = NULL;
    ch_error error;
    ch_status status = mutate
        ? ch_runtime_mutate(state->runtime, operation_utf, request_utf, &response, &error)
        : ch_runtime_query(state->runtime, operation_utf, request_utf, &response, &error);
    ch_jni_release_utf(environment, operation, operation_utf);
    ch_jni_release_utf(environment, request_json, request_utf);
    if (status != CH_OK) {
        ch_jni_check(environment, status, &error);
        return NULL;
    }
    jstring result = (*environment)->NewStringUTF(environment, response);
    ch_string_free(response);
    return result;
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_android_NativeClambhookBridge_nativeQuery(
    JNIEnv *environment, jobject bridge, jlong handle, jstring operation, jstring request_json
) {
    (void)bridge;
    return ch_jni_operation(environment, handle, operation, request_json, 0);
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_android_NativeClambhookBridge_nativeMutate(
    JNIEnv *environment, jobject bridge, jlong handle, jstring operation, jstring request_json
) {
    (void)bridge;
    return ch_jni_operation(environment, handle, operation, request_json, 1);
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_android_NativeClambhookBridge_nativeDeveloperRequest(
    JNIEnv *environment, jobject bridge, jlong handle, jboolean repeat,
    jstring request_json
) {
    (void)bridge;
    ch_jni_runtime *state = ch_jni_from_handle(environment, handle);
    if (state == NULL) return NULL;
    const char *request_utf = ch_jni_get_utf(environment, request_json);
    if (request_json != NULL && request_utf == NULL) return NULL;
    char *response = NULL;
    ch_error error;
    ch_status status = ch_runtime_developer_request(
        state->runtime, repeat == JNI_TRUE, request_utf, &response, &error);
    ch_jni_release_utf(environment, request_json, request_utf);
    if (status != CH_OK) {
        ch_jni_check(environment, status, &error);
        return NULL;
    }
    jstring result = (*environment)->NewStringUTF(environment, response);
    ch_string_free(response);
    return result;
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_android_NativeClambhookConfigBridge_nativeQueryConfig(
    JNIEnv *environment, jobject bridge, jstring config_path,
    jstring operation, jstring request_json
) {
    (void)bridge;
    const char *path_utf = ch_jni_get_utf(environment, config_path);
    const char *operation_utf = ch_jni_get_utf(environment, operation);
    const char *request_utf = ch_jni_get_utf(environment, request_json);
    if (path_utf == NULL || operation_utf == NULL ||
        (request_json != NULL && request_utf == NULL)) {
        ch_jni_release_utf(environment, config_path, path_utf);
        ch_jni_release_utf(environment, operation, operation_utf);
        ch_jni_release_utf(environment, request_json, request_utf);
        return NULL;
    }
    char *response = NULL;
    ch_error error;
    ch_status status = ch_runtime_config_query_file(
        path_utf, operation_utf, request_utf, &response, &error);
    ch_jni_release_utf(environment, config_path, path_utf);
    ch_jni_release_utf(environment, operation, operation_utf);
    ch_jni_release_utf(environment, request_json, request_utf);
    if (status != CH_OK) {
        ch_jni_check(environment, status, &error);
        return NULL;
    }
    jstring result = (*environment)->NewStringUTF(environment, response);
    ch_string_free(response);
    return result;
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_android_NativeClambhookConfigBridge_nativeMutateConfig(
    JNIEnv *environment, jobject bridge, jstring config_path,
    jstring mutation, jstring response_operation, jstring request_json
) {
    (void)bridge;
    const char *path_utf = ch_jni_get_utf(environment, config_path);
    const char *mutation_utf = ch_jni_get_utf(environment, mutation);
    const char *response_utf = ch_jni_get_utf(environment,
                                              response_operation);
    const char *request_utf = ch_jni_get_utf(environment, request_json);
    if (path_utf == NULL || mutation_utf == NULL || response_utf == NULL ||
        request_utf == NULL) {
        ch_jni_release_utf(environment, config_path, path_utf);
        ch_jni_release_utf(environment, mutation, mutation_utf);
        ch_jni_release_utf(environment, response_operation, response_utf);
        ch_jni_release_utf(environment, request_json, request_utf);
        return NULL;
    }
    char *response = NULL;
    ch_error error;
    ch_status status = ch_runtime_config_mutate_file(
        path_utf, mutation_utf, response_utf, request_utf, &response, &error);
    ch_jni_release_utf(environment, config_path, path_utf);
    ch_jni_release_utf(environment, mutation, mutation_utf);
    ch_jni_release_utf(environment, response_operation, response_utf);
    ch_jni_release_utf(environment, request_json, request_utf);
    if (status != CH_OK) {
        ch_jni_check(environment, status, &error);
        return NULL;
    }
    jstring result = (*environment)->NewStringUTF(environment, response);
    ch_string_free(response);
    return result;
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_android_NativeClambhookConfigBridge_nativeImportReview(
    JNIEnv *environment, jobject bridge, jstring import_text
) {
    (void)bridge;
    const char *import_utf = ch_jni_get_utf(environment, import_text);
    if (import_utf == NULL) return NULL;
    char *response = NULL;
    ch_error error;
    ch_status status = ch_config_import_review_json(import_utf, &response,
                                                    &error);
    ch_jni_release_utf(environment, import_text, import_utf);
    if (status != CH_OK) {
        ch_jni_check(environment, status, &error);
        return NULL;
    }
    jstring result = (*environment)->NewStringUTF(environment, response);
    ch_string_free(response);
    return result;
}

JNIEXPORT void JNICALL
Java_com_clambhook_android_NativeClambhookConfigBridge_nativeApplyReviewedImport(
    JNIEnv *environment, jobject bridge, jstring config_path,
    jstring request_json
) {
    (void)bridge;
    const char *path_utf = ch_jni_get_utf(environment, config_path);
    const char *request_utf = ch_jni_get_utf(environment, request_json);
    if (path_utf == NULL || request_utf == NULL) {
        ch_jni_release_utf(environment, config_path, path_utf);
        ch_jni_release_utf(environment, request_json, request_utf);
        return;
    }
    ch_error error;
    ch_status status = ch_config_apply_reviewed_import_file(
        path_utf, request_utf, &error);
    ch_jni_release_utf(environment, config_path, path_utf);
    ch_jni_release_utf(environment, request_json, request_utf);
    ch_jni_check(environment, status, &error);
}

static jstring ch_jni_config_file_operation(
    JNIEnv *environment, jstring config_path, jstring request,
    int set_active
) {
    const char *path_utf = ch_jni_get_utf(environment, config_path);
    const char *request_utf = ch_jni_get_utf(environment, request);
    if (path_utf == NULL || request_utf == NULL) {
        ch_jni_release_utf(environment, config_path, path_utf);
        ch_jni_release_utf(environment, request, request_utf);
        return NULL;
    }
    char *response = NULL;
    ch_error error;
    ch_status status = set_active ? ch_runtime_config_set_active_file(
        path_utf, request_utf, &response, &error) :
        ch_runtime_config_import_file(path_utf, request_utf, &response,
                                      &error);
    ch_jni_release_utf(environment, config_path, path_utf);
    ch_jni_release_utf(environment, request, request_utf);
    if (status != CH_OK) {
        ch_jni_check(environment, status, &error);
        return NULL;
    }
    jstring result = (*environment)->NewStringUTF(environment, response);
    ch_string_free(response);
    return result;
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_android_NativeClambhookConfigBridge_nativeImportConfig(
    JNIEnv *environment, jobject bridge, jstring config_path,
    jstring document
) {
    (void)bridge;
    return ch_jni_config_file_operation(environment, config_path, document,
                                        0);
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_android_NativeClambhookConfigBridge_nativeSetActiveProfile(
    JNIEnv *environment, jobject bridge, jstring config_path,
    jstring request_json
) {
    (void)bridge;
    return ch_jni_config_file_operation(environment, config_path,
                                        request_json, 1);
}
