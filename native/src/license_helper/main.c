// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "clambhook/json.h"
#include "clambhook/license.h"
#include "clambhook/license_json.h"
#include "internal.h"

#ifndef CLAMBHOOK_VERSION
#define CLAMBHOOK_VERSION "dev"
#endif

#define CH_LICENSE_REQUEST_LIMIT (8U * 1024U * 1024U)

static char *read_request(void) {
    ch_json_buffer input;
    ch_json_init(&input);
    char chunk[4096];
    for (;;) {
        size_t count = fread(chunk, 1U, sizeof(chunk), stdin);
        if (count > 0U) {
            if (input.length > CH_LICENSE_REQUEST_LIMIT - count ||
                !ch_json_append_format(&input, "%.*s", (int)count, chunk)) {
                ch_json_dispose(&input);
                return NULL;
            }
        }
        if (count < sizeof(chunk)) {
            if (ferror(stdin)) {
                ch_json_dispose(&input);
                return NULL;
            }
            break;
        }
    }
    return ch_json_take(&input);
}

static const char *request_string(const ch_json_value *request, const char *key) {
    const char *value = ch_json_string_value(ch_json_object_get(request, key));
    return value == NULL ? "" : value;
}

static int64_t request_millis(const ch_json_value *request, const char *key) {
    double number = ch_json_number_value(ch_json_object_get(request, key), 0.0);
    return number > (double)INT64_MAX || number < (double)INT64_MIN ? 0 : (int64_t)number;
}

static void write_error(const char *message) {
    ch_json_buffer response;
    ch_json_init(&response);
    (void)ch_json_append(&response, "{\"ok\":false,\"error\":");
    (void)ch_json_append_string(&response, message == NULL ? "unknown error" : message);
    (void)ch_json_append(&response, "}\n");
    char *json = ch_json_take(&response);
    if (json != NULL) {
        (void)fputs(json, stdout);
        free(json);
    }
}

static void write_result(const char *result) {
    ch_json_buffer response;
    ch_json_init(&response);
    (void)ch_json_append(&response, "{\"ok\":true,\"result\":");
    (void)ch_json_append_string(&response, result == NULL ? "" : result);
    (void)ch_json_append(&response, "}\n");
    char *json = ch_json_take(&response);
    if (json != NULL) {
        (void)fputs(json, stdout);
        free(json);
    }
}

static void write_allowed(bool allowed) {
    (void)printf("{\"ok\":true,\"allowed\":%s}\n", allowed ? "true" : "false");
}

static void dispatch(const ch_json_value *request) {
    const char *command = request_string(request, "command");
    const char *snapshot = request_string(request, "snapshot");
    int64_t now = request_millis(request, "nowUnixMillis");
    ch_error error;
    char *result = NULL;
    ch_status status = CH_OK;
    if (strcmp(command, "install-id") == 0) {
        result = ch_license_new_install_id(&error);
    } else if (strcmp(command, "portal-url") == 0) {
        result = ch_strdup(CH_LICENSE_PORTAL_URL);
    } else if (strcmp(command, "validation-base-url") == 0) {
        result = ch_strdup(CH_LICENSE_VALIDATION_BASE_URL);
    } else if (strcmp(command, "commercial-terms") == 0) {
        result = ch_license_commercial_terms_json(&error);
    } else if (strcmp(command, "ensure-trial") == 0) {
        status = ch_license_ensure_trial_json(snapshot, now, &result, &error);
    } else if (strcmp(command, "evaluate") == 0) {
        status = ch_license_evaluate_json(snapshot, now, &result, &error);
    } else if (strcmp(command, "status") == 0) {
        status = ch_license_status_json(
            snapshot, request_millis(request, "updatePublishedAtMillis"), now, &result, &error
        );
    } else if (strcmp(command, "activate") == 0) {
        status = ch_license_activate_json(
            request_string(request, "baseURL"),
            request_string(request, "licenseKey"),
            request_string(request, "email"),
            request_string(request, "deviceRegistration"),
            now,
            &result,
            &error
        );
    } else if (strcmp(command, "device-action") == 0) {
        status = ch_license_device_action_json(
            request_string(request, "baseURL"),
            request_string(request, "action"),
            request_string(request, "licenseKey"),
            request_string(request, "installID"),
            request_string(request, "deviceID"),
            request_string(request, "deviceRegistration"),
            now,
            &result,
            &error
        );
    } else if (strcmp(command, "update-allowed") == 0) {
        bool allowed = false;
        status = ch_license_update_allowed_json(
            snapshot, request_millis(request, "publishedAtMillis"), now, &allowed, &error
        );
        if (status == CH_OK) write_allowed(allowed); else write_error(error.message);
        return;
    } else if (strcmp(command, "mark-verification-failure") == 0) {
        status = ch_license_mark_verification_failure_json(snapshot, now, &result, &error);
    } else {
        ch_json_buffer message;
        ch_json_init(&message);
        (void)ch_json_append(&message, "unknown command ");
        (void)ch_json_append_string(&message, command);
        char *text = ch_json_take(&message);
        write_error(text);
        free(text);
        return;
    }
    if (status != CH_OK || result == NULL) {
        write_error(status == CH_OK ? "out of memory" : error.message);
    } else {
        write_result(result);
    }
    free(result);
}

int main(int argc, char **argv) {
    if (argc == 2 && (strcmp(argv[1], "-version") == 0 || strcmp(argv[1], "--version") == 0)) {
        printf("clambhook-license %s\n", CLAMBHOOK_VERSION);
        return 0;
    }
    if (argc != 1) {
        write_error("usage: clambhook-license [-version]");
        return 0;
    }
    char *raw = read_request();
    if (raw == NULL) {
        write_error("read stdin: request exceeds 8 MiB or an I/O error occurred");
        return 0;
    }
    ch_error error;
    ch_json_value *request = ch_json_parse(raw, strlen(raw), &error);
    free(raw);
    if (request == NULL || ch_json_value_type(request) != CH_JSON_OBJECT) {
        write_error(request == NULL ? error.message : "decode request: expected JSON object");
        ch_json_value_destroy(request);
        return 0;
    }
    dispatch(request);
    ch_json_value_destroy(request);
    return 0;
}
