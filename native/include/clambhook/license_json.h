// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_LICENSE_JSON_H
#define CLAMBHOOK_LICENSE_JSON_H

#include <stdbool.h>
#include <stdint.h>

#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

char *ch_license_new_install_id(ch_error *error);
char *ch_license_commercial_terms_json(ch_error *error);
ch_status ch_license_ensure_trial_json(
    const char *snapshot_json,
    int64_t now_unix_millis,
    char **result_json,
    ch_error *error
);
ch_status ch_license_evaluate_json(
    const char *snapshot_json,
    int64_t now_unix_millis,
    char **result_json,
    ch_error *error
);
ch_status ch_license_status_json(
    const char *snapshot_json,
    int64_t update_published_at_millis,
    int64_t now_unix_millis,
    char **result_json,
    ch_error *error
);
ch_status ch_license_apply_server_response_json(
    const char *server_response_json,
    const char *install_id,
    int64_t now_unix_millis,
    char **result_json,
    ch_error *error
);
ch_status ch_license_activate_json(
    const char *base_url,
    const char *license_key,
    const char *email,
    const char *device_registration_json,
    int64_t now_unix_millis,
    char **result_json,
    ch_error *error
);
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
);
ch_status ch_license_update_allowed_json(
    const char *snapshot_json,
    int64_t published_at_millis,
    int64_t now_unix_millis,
    bool *allowed,
    ch_error *error
);
ch_status ch_license_mark_verification_failure_json(
    const char *snapshot_json,
    int64_t now_unix_millis,
    char **result_json,
    ch_error *error
);

#ifdef __cplusplus
}
#endif

#endif
