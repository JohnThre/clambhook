// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "clambhook/runtime.h"

#include <stdbool.h>
#include <ctype.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "clambhook/config.h"
#include "clambhook/json.h"
#include "internal.h"

#define CH_CONFIG_FILE_TRANSFER_LIMIT (4U * 1024U * 1024U)

static char *runtime_config_with_metadata(char *payload,
                                          const char *backup_path,
                                          const char *message,
                                          ch_error *error) {
    if (payload == NULL) return NULL;
    size_t length = strlen(payload);
    if (length == 0U || payload[length - 1U] != '}') {
        free(payload);
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "config response is not a JSON object");
        return NULL;
    }
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append_bytes(&json, payload, length - 1U) &&
        ch_json_append(&json, ",\"backup_path\":") &&
        ch_json_append_string(&json, backup_path == NULL ? "" : backup_path);
    if (okay && message != NULL) {
        okay = ch_json_append(&json, ",\"message\":") &&
            ch_json_append_string(&json, message);
    }
    if (okay) okay = ch_json_append(&json, "}");
    free(payload);
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    if (result == NULL && (error == NULL || error->code == CH_OK)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                     "encode config response metadata");
    }
    return result;
}

static ch_status runtime_config_profiles_response(
    const ch_config *config, const char *backup_path, const char *message,
    char **response_json, ch_error *error) {
    const ch_config_table *profile = ch_config_active_profile(config);
    char *profile_name = NULL;
    if (profile == NULL || ch_config_table_get_string(
            profile, "name", &profile_name, error) != CH_OK) {
        free(profile_name);
        if (error == NULL || error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_PARSE,
                         "active profile has no name");
        }
        return error == NULL ? CH_ERROR_PARSE : error->code;
    }
    char *payload = ch_config_query_payload_json(
        config, profile_name, "profiles", "{}", error);
    free(profile_name);
    if (payload == NULL) {
        return error == NULL || error->code == CH_OK ?
            CH_ERROR_OUT_OF_MEMORY : error->code;
    }
    *response_json = runtime_config_with_metadata(
        payload, backup_path, message, error);
    return *response_json == NULL ?
        (error == NULL || error->code == CH_OK ? CH_ERROR_OUT_OF_MEMORY :
                                                 error->code) : CH_OK;
}

ch_status ch_runtime_config_query_file(const char *config_path,
                                       const char *operation,
                                       const char *request_json,
                                       char **response_json,
                                       ch_error *error) {
    ch_error_clear(error);
    if (config_path == NULL || config_path[0] == '\0' || operation == NULL ||
        response_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "config path, operation, and response are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *response_json = NULL;
    ch_config *config = NULL;
    ch_status status = ch_config_load(config_path, &config, error);
    if (status != CH_OK) return status;
    const ch_config_table *profile = ch_config_active_profile(config);
    char *profile_name = NULL;
    if (profile == NULL || ch_config_table_get_string(
            profile, "name", &profile_name, error) != CH_OK) {
        ch_config_free(config);
        free(profile_name);
        if (error == NULL || error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_PARSE,
                         "active profile has no name");
        }
        return error == NULL ? CH_ERROR_PARSE : error->code;
    }
    if (strcmp(operation, "test_rule") == 0) {
        status = ch_rule_explain_request_json(
            config, profile_name, request_json == NULL ? "{}" : request_json,
            response_json, error);
    } else {
        *response_json = ch_config_query_payload_json(
            config, profile_name, operation,
            request_json == NULL ? "{}" : request_json, error);
        if (*response_json == NULL) {
            status = error == NULL || error->code == CH_OK ?
                CH_ERROR_OUT_OF_MEMORY : error->code;
        }
    }
    free(profile_name);
    ch_config_free(config);
    if (status != CH_OK || *response_json == NULL) {
        if (error == NULL || error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "encode file-backed config query");
        }
        return status != CH_OK ? status :
            (error == NULL ? CH_ERROR_OUT_OF_MEMORY : error->code);
    }
    return CH_OK;
}

ch_status ch_runtime_config_mutate_file(const char *config_path,
                                        const char *mutation,
                                        const char *response_operation,
                                        const char *request_json,
                                        char **response_json,
                                        ch_error *error) {
    ch_error_clear(error);
    if (config_path == NULL || config_path[0] == '\0' || mutation == NULL ||
        response_operation == NULL || request_json == NULL ||
        response_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "config path, mutation, response operation, request, "
                     "and response are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *response_json = NULL;
    ch_config *before = NULL;
    ch_config *after = NULL;
    char *profile_name = NULL;
    char *document = NULL;
    char *backup_path = NULL;
    bool wrote_document = false;
    ch_status status = ch_config_load(config_path, &before, error);
    const ch_config_table *profile = status == CH_OK ?
        ch_config_active_profile(before) : NULL;
    if (status == CH_OK &&
        (profile == NULL || ch_config_table_get_string(
            profile, "name", &profile_name, error) != CH_OK)) {
        if (error == NULL || error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_PARSE,
                         "active profile has no name");
        }
        status = error == NULL ? CH_ERROR_PARSE : error->code;
    }
    if (status == CH_OK) {
        status = ch_config_mutate_document_json(
            before, profile_name, mutation, request_json, &document, error);
    }
    if (status == CH_OK) {
        status = ch_config_write_atomic_document(
            config_path, document, &backup_path, error);
        wrote_document = status == CH_OK;
    }
    if (status == CH_OK) status = ch_config_load(config_path, &after, error);
    if (status == CH_OK) {
        *response_json = ch_config_query_payload_json(
            after, profile_name, response_operation, request_json, error);
        if (*response_json == NULL) {
            status = error == NULL || error->code == CH_OK ?
                CH_ERROR_OUT_OF_MEMORY : error->code;
        }
    }
    if (status != CH_OK && wrote_document && before != NULL) {
        ch_error restore_error;
        ch_status restore = ch_config_write_atomic_document(
            config_path, ch_config_document(before), NULL, &restore_error);
        if (restore != CH_OK) {
            ch_error_set(error, CH_ERROR_INTERNAL,
                         "config mutation failed; restore config: %s",
                         restore_error.message);
            status = CH_ERROR_INTERNAL;
        }
    }
    free(profile_name);
    free(document);
    free(backup_path);
    ch_config_free(after);
    ch_config_free(before);
    return status;
}

ch_status ch_runtime_config_import_file(const char *config_path,
                                        const char *document,
                                        char **response_json,
                                        ch_error *error) {
    ch_error_clear(error);
    if (config_path == NULL || config_path[0] == '\0' || document == NULL ||
        response_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "config path, document, and response are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *response_json = NULL;
    size_t length = strlen(document);
    bool nonblank = false;
    for (size_t index = 0U; index < length; ++index) {
        if (isspace((unsigned char)document[index]) == 0) {
            nonblank = true;
            break;
        }
    }
    if (!nonblank || length > CH_CONFIG_FILE_TRANSFER_LIMIT) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, !nonblank ?
                     "empty config body" : "config exceeds import size limit");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_config *before = NULL;
    ch_config *incoming = NULL;
    ch_config *after = NULL;
    char *backup_path = NULL;
    bool wrote = false;
    ch_status status = ch_config_load(config_path, &before, error);
    if (status == CH_OK) {
        status = ch_config_parse(document, config_path, &incoming, error);
    }
    if (status == CH_OK) {
        status = ch_config_write_atomic_document(
            config_path, document, &backup_path, error);
        wrote = status == CH_OK;
    }
    if (status == CH_OK) status = ch_config_load(config_path, &after, error);
    if (status == CH_OK) {
        char message[96];
        (void)snprintf(message, sizeof(message), "imported %zu profile(s)",
                       ch_config_profile_count(after));
        status = runtime_config_profiles_response(
            after, backup_path, message, response_json, error);
    }
    if (status != CH_OK && wrote && before != NULL) {
        ch_error restore_error;
        ch_status restored = ch_config_write_atomic_document(
            config_path, ch_config_document(before), NULL, &restore_error);
        if (restored != CH_OK) {
            ch_error_set(error, CH_ERROR_INTERNAL,
                         "config import failed; restore config: %s",
                         restore_error.message);
            status = CH_ERROR_INTERNAL;
        }
    }
    free(backup_path);
    ch_config_free(after);
    ch_config_free(incoming);
    ch_config_free(before);
    return status;
}

ch_status ch_runtime_config_set_active_file(const char *config_path,
                                            const char *request_json,
                                            char **response_json,
                                            ch_error *error) {
    ch_error_clear(error);
    if (config_path == NULL || config_path[0] == '\0' ||
        request_json == NULL || response_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "config path, active-profile request, and response are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *response_json = NULL;
    ch_json_value *request = ch_json_parse(
        request_json, strlen(request_json), error);
    if (request == NULL) return error->code;
    const char *name = ch_json_string_value(ch_json_object_get(request,
                                                               "name"));
    if (name == NULL || name[0] == '\0') {
        ch_json_value_destroy(request);
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT,
                     "profile name is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    ch_config *before = NULL;
    ch_config *after = NULL;
    char *document = NULL;
    char *backup_path = NULL;
    bool wrote = false;
    ch_status status = ch_config_load(config_path, &before, error);
    if (status == CH_OK) {
        status = ch_config_document_set_active(before, name, &document,
                                               error);
    }
    if (status == CH_OK) {
        status = ch_config_write_atomic_document(
            config_path, document, &backup_path, error);
        wrote = status == CH_OK;
    }
    if (status == CH_OK) status = ch_config_load(config_path, &after, error);
    if (status == CH_OK) {
        char *profiles = NULL;
        status = runtime_config_profiles_response(
            after, backup_path, NULL, &profiles, error);
        if (status == CH_OK) {
            size_t length = strlen(profiles);
            ch_json_buffer result;
            ch_json_init(&result);
            int okay = length > 0U && profiles[length - 1U] == '}' &&
                ch_json_append_bytes(&result, profiles, length - 1U) &&
                ch_json_append(&result, ",\"persisted\":true}");
            free(profiles);
            *response_json = okay ? ch_json_take(&result) : NULL;
            ch_json_dispose(&result);
            if (*response_json == NULL) {
                ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                             "encode active profile response");
                status = CH_ERROR_OUT_OF_MEMORY;
            }
        }
    }
    if (status != CH_OK && wrote && before != NULL) {
        ch_error restore_error;
        ch_status restored = ch_config_write_atomic_document(
            config_path, ch_config_document(before), NULL, &restore_error);
        if (restored != CH_OK) {
            ch_error_set(error, CH_ERROR_INTERNAL,
                         "active profile update failed; restore config: %s",
                         restore_error.message);
            status = CH_ERROR_INTERNAL;
        }
    }
    free(backup_path);
    free(document);
    ch_config_free(after);
    ch_config_free(before);
    ch_json_value_destroy(request);
    return status;
}
