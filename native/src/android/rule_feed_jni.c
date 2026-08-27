#include <jni.h>

#include <stdint.h>
#include <stdlib.h>

#include "clambhook/rule_feed.h"

static void rule_feed_jni_throw(JNIEnv *environment, const char *message) {
    jclass error_class = (*environment)->FindClass(
        environment, "java/lang/IllegalStateException");
    if (error_class != NULL) {
        (void)(*environment)->ThrowNew(
            environment, error_class,
            message == NULL ? "native rule feed error" : message);
        (*environment)->DeleteLocalRef(environment, error_class);
    }
}

static const char *rule_feed_jni_utf(JNIEnv *environment, jstring value) {
    return value == NULL ? NULL :
        (*environment)->GetStringUTFChars(environment, value, NULL);
}

static void rule_feed_jni_release(JNIEnv *environment, jstring value,
                                  const char *text) {
    if (value != NULL && text != NULL) {
        (*environment)->ReleaseStringUTFChars(environment, value, text);
    }
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_android_NativeClambhookRuleFeedBridge_nativeMetadata(
    JNIEnv *environment, jobject bridge, jstring config_path,
    jstring profile, jstring name, jstring url
) {
    (void)bridge;
    const char *path_utf = rule_feed_jni_utf(environment, config_path);
    const char *profile_utf = rule_feed_jni_utf(environment, profile);
    const char *name_utf = rule_feed_jni_utf(environment, name);
    const char *url_utf = rule_feed_jni_utf(environment, url);
    if (path_utf == NULL || profile_utf == NULL || name_utf == NULL ||
        url_utf == NULL) goto cleanup;
    char *metadata = NULL;
    ch_error error;
    ch_status status = ch_rule_feed_cache_metadata_json(
        path_utf, CH_RULE_FEED_RULE_SET, profile_utf, name_utf, url_utf,
        &metadata, &error);
    if (status != CH_OK) {
        rule_feed_jni_throw(environment, error.message);
        goto cleanup;
    }
    jstring result = (*environment)->NewStringUTF(environment, metadata);
    free(metadata);
    rule_feed_jni_release(environment, config_path, path_utf);
    rule_feed_jni_release(environment, profile, profile_utf);
    rule_feed_jni_release(environment, name, name_utf);
    rule_feed_jni_release(environment, url, url_utf);
    return result;

cleanup:
    rule_feed_jni_release(environment, config_path, path_utf);
    rule_feed_jni_release(environment, profile, profile_utf);
    rule_feed_jni_release(environment, name, name_utf);
    rule_feed_jni_release(environment, url, url_utf);
    return NULL;
}

JNIEXPORT void JNICALL
Java_com_clambhook_android_NativeClambhookRuleFeedBridge_nativeStoreResponse(
    JNIEnv *environment, jobject bridge, jstring config_path,
    jstring profile, jstring name, jstring url, jstring format,
    jbyteArray body, jstring etag, jstring last_modified,
    jlong fetched_ts_ns
) {
    (void)bridge;
    const char *path_utf = rule_feed_jni_utf(environment, config_path);
    const char *profile_utf = rule_feed_jni_utf(environment, profile);
    const char *name_utf = rule_feed_jni_utf(environment, name);
    const char *url_utf = rule_feed_jni_utf(environment, url);
    const char *format_utf = rule_feed_jni_utf(environment, format);
    const char *etag_utf = rule_feed_jni_utf(environment, etag);
    const char *modified_utf = rule_feed_jni_utf(environment, last_modified);
    jbyte *bytes = body == NULL ? NULL :
        (*environment)->GetByteArrayElements(environment, body, NULL);
    jsize length = body == NULL ? 0 :
        (*environment)->GetArrayLength(environment, body);
    if (path_utf != NULL && profile_utf != NULL && name_utf != NULL &&
        url_utf != NULL && format_utf != NULL && bytes != NULL) {
        ch_rule_feed_refresh_options options = {
            .config_path = path_utf,
            .kind = CH_RULE_FEED_RULE_SET,
            .profile = profile_utf,
            .name = name_utf,
            .url = url_utf,
            .format = format_utf,
            .action = "block"
        };
        ch_error error;
        ch_status status = ch_rule_feed_cache_store_response(
            &options, (const char *)bytes, (size_t)length, etag_utf,
            modified_utf, (int64_t)fetched_ts_ns, &error);
        if (status != CH_OK) rule_feed_jni_throw(environment, error.message);
    }
    if (bytes != NULL) {
        (*environment)->ReleaseByteArrayElements(environment, body, bytes,
                                                 JNI_ABORT);
    }
    rule_feed_jni_release(environment, config_path, path_utf);
    rule_feed_jni_release(environment, profile, profile_utf);
    rule_feed_jni_release(environment, name, name_utf);
    rule_feed_jni_release(environment, url, url_utf);
    rule_feed_jni_release(environment, format, format_utf);
    rule_feed_jni_release(environment, etag, etag_utf);
    rule_feed_jni_release(environment, last_modified, modified_utf);
}

JNIEXPORT void JNICALL
Java_com_clambhook_android_NativeClambhookRuleFeedBridge_nativeTouch(
    JNIEnv *environment, jobject bridge, jstring config_path,
    jstring profile, jstring name, jstring url, jlong fetched_ts_ns
) {
    (void)bridge;
    const char *path_utf = rule_feed_jni_utf(environment, config_path);
    const char *profile_utf = rule_feed_jni_utf(environment, profile);
    const char *name_utf = rule_feed_jni_utf(environment, name);
    const char *url_utf = rule_feed_jni_utf(environment, url);
    if (path_utf != NULL && profile_utf != NULL && name_utf != NULL &&
        url_utf != NULL) {
        ch_error error;
        ch_status status = ch_rule_feed_cache_touch(
            path_utf, CH_RULE_FEED_RULE_SET, profile_utf, name_utf, url_utf,
            (int64_t)fetched_ts_ns, &error);
        if (status != CH_OK) rule_feed_jni_throw(environment, error.message);
    }
    rule_feed_jni_release(environment, config_path, path_utf);
    rule_feed_jni_release(environment, profile, profile_utf);
    rule_feed_jni_release(environment, name, name_utf);
    rule_feed_jni_release(environment, url, url_utf);
}
