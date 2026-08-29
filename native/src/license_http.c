// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "clambhook/license_json.h"

#include <ctype.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <curl/curl.h>
#include <uv.h>

#include "clambhook/json.h"
#include "clambhook/license.h"
#include "internal.h"

#define CH_LICENSE_RESPONSE_LIMIT (1024U * 1024U)

typedef struct ch_http_response {
    char *data;
    size_t length;
    int overflow;
} ch_http_response;

typedef struct ch_registration {
    ch_json_value *root;
    const char *install_id;
    const char *display_name;
    const char *platform;
    const char *architecture;
    const char *app_version;
} ch_registration;

static uv_once_t ch_curl_once = UV_ONCE_INIT;
static CURLcode ch_curl_init_status = CURLE_OK;

static void ch_curl_initialize(void) {
    ch_curl_init_status = curl_global_init(CURL_GLOBAL_DEFAULT);
}

static char *ch_trimmed_copy(const char *value) {
    const char *start = value == NULL ? "" : value;
    while (isspace((unsigned char)*start)) ++start;
    const char *end = start + strlen(start);
    while (end > start && isspace((unsigned char)end[-1])) --end;
    size_t length = (size_t)(end - start);
    char *copy = malloc(length + 1U);
    if (copy == NULL) return NULL;
    memcpy(copy, start, length);
    copy[length] = '\0';
    return copy;
}

static ch_status ch_registration_string(
    const ch_json_value *object,
    const char *key,
    const char **output,
    ch_error *error
) {
    const ch_json_value *value = ch_json_object_get(object, key);
    if (value == NULL || ch_json_value_type(value) == CH_JSON_NULL) {
        *output = "";
        return CH_OK;
    }
    *output = ch_json_string_value(value);
    if (*output == NULL) {
        ch_error_set(error, CH_ERROR_PARSE, "license: device registration %s must be a string", key);
        return CH_ERROR_PARSE;
    }
    return CH_OK;
}

static ch_status ch_registration_parse(
    const char *json,
    ch_registration *registration,
    ch_error *error
) {
    memset(registration, 0, sizeof(*registration));
    const char *text = json == NULL ? "" : json;
    registration->root = ch_json_parse(text, strlen(text), error);
    if (registration->root == NULL) return error == NULL ? CH_ERROR_PARSE : error->code;
    if (ch_json_value_type(registration->root) != CH_JSON_OBJECT) {
        ch_json_value_destroy(registration->root);
        registration->root = NULL;
        ch_error_set(error, CH_ERROR_PARSE, "license: device registration must be an object");
        return CH_ERROR_PARSE;
    }
    ch_status status = ch_registration_string(
        registration->root, "install_id", &registration->install_id, error
    );
    if (status == CH_OK) status = ch_registration_string(
        registration->root, "display_name", &registration->display_name, error
    );
    if (status == CH_OK) status = ch_registration_string(
        registration->root, "platform", &registration->platform, error
    );
    if (status == CH_OK) status = ch_registration_string(
        registration->root, "architecture", &registration->architecture, error
    );
    if (status == CH_OK) status = ch_registration_string(
        registration->root, "app_version", &registration->app_version, error
    );
    if (status != CH_OK) {
        ch_json_value_destroy(registration->root);
        memset(registration, 0, sizeof(*registration));
    }
    return status;
}

static int ch_append_registration(ch_json_buffer *json, const ch_registration *registration) {
    int ok = ch_json_append(json, "{\"install_id\":") &&
        ch_json_append_string(json, registration->install_id) &&
        ch_json_append(json, ",\"display_name\":") &&
        ch_json_append_string(json, registration->display_name) &&
        ch_json_append(json, ",\"platform\":") &&
        ch_json_append_string(json, registration->platform) &&
        ch_json_append(json, ",\"architecture\":") &&
        ch_json_append_string(json, registration->architecture);
    if (ok && registration->app_version[0] != '\0') {
        ok = ch_json_append(json, ",\"app_version\":") &&
            ch_json_append_string(json, registration->app_version);
    }
    return ok && ch_json_append(json, "}");
}

static size_t ch_license_response_write(
    char *contents,
    size_t size,
    size_t count,
    void *context
) {
    ch_http_response *response = context;
    if (size != 0U && count > SIZE_MAX / size) {
        response->overflow = 1;
        return 0U;
    }
    size_t incoming = size * count;
    if (incoming > CH_LICENSE_RESPONSE_LIMIT - response->length) {
        response->overflow = 1;
        return 0U;
    }
    char *next = realloc(response->data, response->length + incoming + 1U);
    if (next == NULL) return 0U;
    response->data = next;
    memcpy(response->data + response->length, contents, incoming);
    response->length += incoming;
    response->data[response->length] = '\0';
    return incoming;
}

static char *ch_license_url(const char *base_url, const char *action, ch_error *error) {
    char *base = ch_trimmed_copy(base_url);
    if (base == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate license URL");
        return NULL;
    }
    if (base[0] == '\0') {
        free(base);
        base = ch_strdup(CH_LICENSE_VALIDATION_BASE_URL);
        if (base == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate license URL");
            return NULL;
        }
    }
    size_t base_length = strlen(base);
    while (base_length > 0U && base[base_length - 1U] == '/') base[--base_length] = '\0';
    static const char middle[] = "/v1/devices/";
    size_t action_length = strlen(action);
    if (base_length > SIZE_MAX - sizeof(middle) - action_length) {
        free(base);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "license URL is too long");
        return NULL;
    }
    char *url = malloc(base_length + sizeof(middle) - 1U + action_length + 1U);
    if (url == NULL) {
        free(base);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate license URL");
        return NULL;
    }
    memcpy(url, base, base_length);
    memcpy(url + base_length, middle, sizeof(middle) - 1U);
    memcpy(url + base_length + sizeof(middle) - 1U, action, action_length + 1U);
    free(base);
    return url;
}

static ch_status ch_license_post(
    const char *base_url,
    const char *action,
    const char *body,
    char **response_json,
    ch_error *error
) {
    *response_json = NULL;
    uv_once(&ch_curl_once, ch_curl_initialize);
    if (ch_curl_init_status != CURLE_OK) {
        ch_error_set(error, CH_ERROR_IO, "initialize license HTTP client: %s", curl_easy_strerror(ch_curl_init_status));
        return CH_ERROR_IO;
    }
    char *url = ch_license_url(base_url, action, error);
    if (url == NULL) return error == NULL ? CH_ERROR_OUT_OF_MEMORY : error->code;
    CURL *curl = curl_easy_init();
    if (curl == NULL) {
        free(url);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "create license HTTP request");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    struct curl_slist *headers = curl_slist_append(NULL, "Content-Type: application/json");
    ch_http_response response = {0};
    CURLcode result = headers == NULL ? CURLE_OUT_OF_MEMORY : CURLE_OK;
    if (result == CURLE_OK) result = curl_easy_setopt(curl, CURLOPT_URL, url);
    if (result == CURLE_OK) result = curl_easy_setopt(curl, CURLOPT_POST, 1L);
    if (result == CURLE_OK) result = curl_easy_setopt(curl, CURLOPT_POSTFIELDS, body);
    if (result == CURLE_OK) result = curl_easy_setopt(
        curl, CURLOPT_POSTFIELDSIZE_LARGE, (curl_off_t)strlen(body)
    );
    if (result == CURLE_OK) result = curl_easy_setopt(curl, CURLOPT_HTTPHEADER, headers);
    if (result == CURLE_OK) result = curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, ch_license_response_write);
    if (result == CURLE_OK) result = curl_easy_setopt(curl, CURLOPT_WRITEDATA, &response);
    if (result == CURLE_OK) result = curl_easy_setopt(curl, CURLOPT_TIMEOUT_MS, 20000L);
    if (result == CURLE_OK) result = curl_easy_setopt(curl, CURLOPT_NOSIGNAL, 1L);
    if (result == CURLE_OK) result = curl_easy_setopt(curl, CURLOPT_FOLLOWLOCATION, 1L);
    if (result == CURLE_OK) result = curl_easy_setopt(curl, CURLOPT_MAXREDIRS, 10L);
#if LIBCURL_VERSION_NUM >= 0x075500
    if (result == CURLE_OK) result = curl_easy_setopt(curl, CURLOPT_PROTOCOLS_STR, "http,https");
    if (result == CURLE_OK) result = curl_easy_setopt(curl, CURLOPT_REDIR_PROTOCOLS_STR, "http,https");
#else
    if (result == CURLE_OK) result = curl_easy_setopt(
        curl, CURLOPT_PROTOCOLS, (long)(CURLPROTO_HTTP | CURLPROTO_HTTPS)
    );
    if (result == CURLE_OK) result = curl_easy_setopt(
        curl, CURLOPT_REDIR_PROTOCOLS, (long)(CURLPROTO_HTTP | CURLPROTO_HTTPS)
    );
#endif
    if (result == CURLE_OK) result = curl_easy_perform(curl);
    long http_status = 0L;
    if (result == CURLE_OK) result = curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, &http_status);
    curl_slist_free_all(headers);
    curl_easy_cleanup(curl);
    free(url);

    if (result != CURLE_OK) {
        ch_status status = response.overflow ? CH_ERROR_PARSE : CH_ERROR_IO;
        const char *message = response.overflow
            ? "license response exceeds 1 MiB"
            : curl_easy_strerror(result);
        ch_error_set(error, status, "%s", message);
        free(response.data);
        return status;
    }
    if (response.data == NULL) {
        response.data = ch_strdup("");
        if (response.data == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate empty license response");
            return CH_ERROR_OUT_OF_MEMORY;
        }
    }
    if (http_status < 200L || http_status >= 300L) {
        const char *message = NULL;
        ch_error parse_error;
        ch_json_value *envelope = ch_json_parse(response.data, response.length, &parse_error);
        if (envelope != NULL && ch_json_value_type(envelope) == CH_JSON_OBJECT) {
            message = ch_json_string_value(ch_json_object_get(envelope, "error"));
        }
        if (message != NULL && message[0] != '\0') {
            ch_error_set(error, CH_ERROR_IO, "%s", message);
        } else {
            ch_error_set(error, CH_ERROR_IO, "license request failed (%ld)", http_status);
        }
        ch_json_value_destroy(envelope);
        free(response.data);
        return CH_ERROR_IO;
    }
    *response_json = response.data;
    return CH_OK;
}

ch_status ch_license_activate_json(
    const char *base_url,
    const char *license_key,
    const char *email,
    const char *device_registration_json,
    int64_t now_unix_millis,
    char **result_json,
    ch_error *error
) {
    ch_error_clear(error);
    if (result_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "result JSON is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *result_json = NULL;
    ch_registration registration;
    ch_status status = ch_registration_parse(device_registration_json, &registration, error);
    if (status != CH_OK) return status;
    char *trimmed_key = ch_trimmed_copy(license_key);
    char *trimmed_email = ch_trimmed_copy(email);
    ch_json_buffer body;
    ch_json_init(&body);
    int ok = trimmed_key != NULL && trimmed_email != NULL &&
        ch_json_append(&body, "{\"license_key\":") && ch_json_append_string(&body, trimmed_key);
    if (ok && trimmed_email[0] != '\0') {
        ok = ch_json_append(&body, ",\"email\":") && ch_json_append_string(&body, trimmed_email);
    }
    ok = ok && ch_json_append(&body, ",\"device\":") &&
        ch_append_registration(&body, &registration) && ch_json_append(&body, "}");
    free(trimmed_key);
    free(trimmed_email);
    char *body_json = ok ? ch_json_take(&body) : NULL;
    ch_json_dispose(&body);
    if (body_json == NULL) {
        ch_json_value_destroy(registration.root);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode license activation request");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    char *server_response = NULL;
    status = ch_license_post(base_url, "activate", body_json, &server_response, error);
    free(body_json);
    if (status == CH_OK) status = ch_license_apply_server_response_json(
        server_response, registration.install_id, now_unix_millis, result_json, error
    );
    free(server_response);
    ch_json_value_destroy(registration.root);
    return status;
}

ch_status ch_license_device_action_json(
    const char *base_url,
    const char *action,
    const char *license_key,
    const char *install_id,
    const char *device_id,
    const char *device_registration_json,
    int64_t now_unix_millis,
    char **result_json,
    ch_error *error
) {
    ch_error_clear(error);
    if (result_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "result JSON is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *result_json = NULL;
    if (action == NULL || (strcmp(action, "deactivate") != 0 &&
        strcmp(action, "reactivate") != 0 && strcmp(action, "transfer") != 0)) {
        ch_error_set(
            error,
            CH_ERROR_INVALID_ARGUMENT,
            "license: unsupported device action \"%s\"",
            action == NULL ? "" : action
        );
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_registration registration;
    ch_status status = ch_registration_parse(device_registration_json, &registration, error);
    if (status != CH_OK) return status;
    char *trimmed_key = ch_trimmed_copy(license_key);
    ch_json_buffer body;
    ch_json_init(&body);
    int ok = trimmed_key != NULL && ch_json_append(&body, "{\"license_key\":") &&
        ch_json_append_string(&body, trimmed_key) &&
        ch_json_append(&body, ",\"install_id\":") &&
        ch_json_append_string(&body, install_id == NULL ? "" : install_id);
    if (ok && device_id != NULL && device_id[0] != '\0') {
        ok = ch_json_append(&body, ",\"device_id\":") && ch_json_append_string(&body, device_id);
    }
    ok = ok && ch_json_append(&body, ",\"device\":") &&
        ch_append_registration(&body, &registration) && ch_json_append(&body, "}");
    free(trimmed_key);
    char *body_json = ok ? ch_json_take(&body) : NULL;
    ch_json_dispose(&body);
    if (body_json == NULL) {
        ch_json_value_destroy(registration.root);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode license device request");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    char *server_response = NULL;
    status = ch_license_post(base_url, action, body_json, &server_response, error);
    free(body_json);
    if (status == CH_OK) status = ch_license_apply_server_response_json(
        server_response, install_id, now_unix_millis, result_json, error
    );
    free(server_response);
    ch_json_value_destroy(registration.root);
    return status;
}
