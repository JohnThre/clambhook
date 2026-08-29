// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include <jni.h>

#include <stddef.h>
#include <stdlib.h>
#include <string.h>

extern JavaVM *substrateGetAndroidVM(void);
extern jobject substrateGetActivity(void);

static char *ch_bridge_copy(const char *value) {
    if (value == NULL) return NULL;
    size_t length = strlen(value);
    char *copy = malloc(length + 1U);
    if (copy != NULL) memcpy(copy, value, length + 1U);
    return copy;
}

static void ch_bridge_throw(JNIEnv *environment, const char *message) {
    jclass exception = (*environment)->FindClass(
        environment, "java/lang/IllegalStateException");
    if (exception != NULL) {
        (void)(*environment)->ThrowNew(
            environment, exception,
            message == NULL ? "Android platform bridge failed" : message);
        (*environment)->DeleteLocalRef(environment, exception);
    }
}

static JNIEnv *ch_bridge_attach(JavaVM *vm, int *out_attached) {
    *out_attached = 0;
    JNIEnv *environment = NULL;
    jint status = (*vm)->GetEnv(
        vm, (void **)&environment, JNI_VERSION_1_6);
    if (status == JNI_OK) return environment;
    if (status != JNI_EDETACHED ||
        (*vm)->AttachCurrentThread(vm, &environment, NULL) != JNI_OK) {
        return NULL;
    }
    *out_attached = 1;
    return environment;
}

static char *ch_bridge_dalvik_exception(JNIEnv *environment,
                                        const char *fallback) {
    if (!(*environment)->ExceptionCheck(environment)) {
        return ch_bridge_copy(fallback);
    }
    jthrowable failure = (*environment)->ExceptionOccurred(environment);
    (*environment)->ExceptionClear(environment);
    char *message = NULL;
    jclass throwable = (*environment)->FindClass(
        environment, "java/lang/Throwable");
    if (throwable != NULL && failure != NULL) {
        jmethodID to_string = (*environment)->GetMethodID(
            environment, throwable, "toString", "()Ljava/lang/String;");
        if (to_string != NULL) {
            jstring description = (jstring)(*environment)->CallObjectMethod(
                environment, failure, to_string);
            if (description != NULL &&
                !(*environment)->ExceptionCheck(environment)) {
                const char *characters = (*environment)->GetStringUTFChars(
                    environment, description, NULL);
                if (characters != NULL) {
                    message = ch_bridge_copy(characters);
                    (*environment)->ReleaseStringUTFChars(
                        environment, description, characters);
                }
                (*environment)->DeleteLocalRef(environment, description);
            } else if ((*environment)->ExceptionCheck(environment)) {
                (*environment)->ExceptionClear(environment);
            }
        }
    }
    if (throwable != NULL) {
        (*environment)->DeleteLocalRef(environment, throwable);
    }
    if (failure != NULL) {
        (*environment)->DeleteLocalRef(environment, failure);
    }
    return message == NULL ? ch_bridge_copy(fallback) : message;
}

static jclass ch_bridge_facade(JNIEnv *environment) {
    jobject activity = substrateGetActivity();
    if (activity == NULL) return NULL;
    jclass activity_class = (*environment)->GetObjectClass(
        environment, activity);
    if (activity_class == NULL) return NULL;
    jmethodID get_loader = (*environment)->GetMethodID(
        environment, activity_class, "getClassLoader",
        "()Ljava/lang/ClassLoader;");
    jobject loader = get_loader == NULL ? NULL :
        (*environment)->CallObjectMethod(environment, activity, get_loader);
    (*environment)->DeleteLocalRef(environment, activity_class);
    if (loader == NULL || (*environment)->ExceptionCheck(environment)) {
        if (loader != NULL) (*environment)->DeleteLocalRef(environment, loader);
        return NULL;
    }
    jclass loader_class = (*environment)->GetObjectClass(environment, loader);
    jmethodID load_class = loader_class == NULL ? NULL :
        (*environment)->GetMethodID(
            environment, loader_class, "loadClass",
            "(Ljava/lang/String;)Ljava/lang/Class;");
    jstring name = (*environment)->NewStringUTF(
        environment, "com.clambhook.android.GluonPlatformFacade");
    jclass facade = load_class == NULL || name == NULL ? NULL :
        (jclass)(*environment)->CallObjectMethod(
            environment, loader, load_class, name);
    if (name != NULL) (*environment)->DeleteLocalRef(environment, name);
    if (loader_class != NULL) {
        (*environment)->DeleteLocalRef(environment, loader_class);
    }
    (*environment)->DeleteLocalRef(environment, loader);
    return facade;
}

static jstring ch_bridge_call(JNIEnv *native_environment,
                              const char *method_name,
                              const char *signature,
                              jstring first,
                              jstring second,
                              jstring third) {
    const char *first_utf = first == NULL ? "" :
        (*native_environment)->GetStringUTFChars(
            native_environment, first, NULL);
    const char *second_utf = second == NULL ? "" :
        (*native_environment)->GetStringUTFChars(
            native_environment, second, NULL);
    const char *third_utf = third == NULL ? "" :
        (*native_environment)->GetStringUTFChars(
            native_environment, third, NULL);
    if (first_utf == NULL || second_utf == NULL || third_utf == NULL) {
        if (first != NULL && first_utf != NULL) {
            (*native_environment)->ReleaseStringUTFChars(
                native_environment, first, first_utf);
        }
        if (second != NULL && second_utf != NULL) {
            (*native_environment)->ReleaseStringUTFChars(
                native_environment, second, second_utf);
        }
        if (third != NULL && third_utf != NULL) {
            (*native_environment)->ReleaseStringUTFChars(
                native_environment, third, third_utf);
        }
        ch_bridge_throw(native_environment, "encode Android bridge request");
        return NULL;
    }

    JavaVM *vm = substrateGetAndroidVM();
    int attached = 0;
    JNIEnv *dalvik = vm == NULL ? NULL : ch_bridge_attach(vm, &attached);
    char *failure_message = NULL;
    char *response_copy = NULL;
    if (dalvik == NULL) {
        failure_message = ch_bridge_copy("attach Android runtime thread");
    } else {
        jclass facade = ch_bridge_facade(dalvik);
        jmethodID method = facade == NULL ? NULL :
            (*dalvik)->GetStaticMethodID(
                dalvik, facade, method_name, signature);
        jstring dalvik_first = (*dalvik)->NewStringUTF(dalvik, first_utf);
        jstring dalvik_second = (*dalvik)->NewStringUTF(dalvik, second_utf);
        jstring dalvik_third = third == NULL ? NULL :
            (*dalvik)->NewStringUTF(dalvik, third_utf);
        jstring result = NULL;
        if (facade != NULL && method != NULL && dalvik_first != NULL &&
            dalvik_second != NULL && (third == NULL || dalvik_third != NULL)) {
            result = third == NULL ?
                (jstring)(*dalvik)->CallStaticObjectMethod(
                    dalvik, facade, method, dalvik_first, dalvik_second) :
                (jstring)(*dalvik)->CallStaticObjectMethod(
                    dalvik, facade, method, dalvik_first, dalvik_second,
                    dalvik_third);
        }
        if ((*dalvik)->ExceptionCheck(dalvik)) {
            failure_message = ch_bridge_dalvik_exception(
                dalvik, "Android platform operation failed");
        } else if (result == NULL) {
            failure_message = ch_bridge_copy(
                "Android platform facade is unavailable");
        } else {
            const char *response_utf = (*dalvik)->GetStringUTFChars(
                dalvik, result, NULL);
            if (response_utf != NULL) {
                response_copy = ch_bridge_copy(response_utf);
                (*dalvik)->ReleaseStringUTFChars(dalvik, result, response_utf);
            }
            if (response_copy == NULL) {
                failure_message = ch_bridge_copy(
                    "copy Android platform response");
            }
        }
        if (result != NULL) (*dalvik)->DeleteLocalRef(dalvik, result);
        if (dalvik_third != NULL) {
            (*dalvik)->DeleteLocalRef(dalvik, dalvik_third);
        }
        if (dalvik_second != NULL) {
            (*dalvik)->DeleteLocalRef(dalvik, dalvik_second);
        }
        if (dalvik_first != NULL) {
            (*dalvik)->DeleteLocalRef(dalvik, dalvik_first);
        }
        if (facade != NULL) (*dalvik)->DeleteLocalRef(dalvik, facade);
        if (attached) (void)(*vm)->DetachCurrentThread(vm);
    }

    if (first != NULL) {
        (*native_environment)->ReleaseStringUTFChars(
            native_environment, first, first_utf);
    }
    if (second != NULL) {
        (*native_environment)->ReleaseStringUTFChars(
            native_environment, second, second_utf);
    }
    if (third != NULL) {
        (*native_environment)->ReleaseStringUTFChars(
            native_environment, third, third_utf);
    }
    if (failure_message != NULL) {
        ch_bridge_throw(native_environment, failure_message);
        free(failure_message);
        free(response_copy);
        return NULL;
    }
    jstring response = (*native_environment)->NewStringUTF(
        native_environment, response_copy == NULL ? "" : response_copy);
    free(response_copy);
    return response;
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_ui_platform_AndroidDalvikBridge_request(
    JNIEnv *environment, jclass type, jstring method, jstring path,
    jstring body) {
    (void)type;
    return ch_bridge_call(
        environment, "request",
        "(Ljava/lang/String;Ljava/lang/String;Ljava/lang/String;)Ljava/lang/String;",
        method, path, body);
}

JNIEXPORT jstring JNICALL
Java_com_clambhook_ui_platform_AndroidDalvikBridge_dispatch(
    JNIEnv *environment, jclass type, jstring operation, jstring request) {
    (void)type;
    return ch_bridge_call(
        environment, "dispatch",
        "(Ljava/lang/String;Ljava/lang/String;)Ljava/lang/String;",
        operation, request, NULL);
}
