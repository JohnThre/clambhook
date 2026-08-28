// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "clambhook/runtime.h"

#include <stdbool.h>
#include <stdlib.h>

#include "clambhook/config.h"
#include "internal.h"

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
    *response_json = ch_config_query_payload_json(
        config, profile_name, operation,
        request_json == NULL ? "{}" : request_json, error);
    free(profile_name);
    ch_config_free(config);
    if (*response_json == NULL) {
        if (error == NULL || error->code == CH_OK) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY,
                         "encode file-backed config query");
        }
        return error == NULL ? CH_ERROR_OUT_OF_MEMORY : error->code;
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
