// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_PROFILE_CONVERTER_H
#define CLAMBHOOK_PROFILE_CONVERTER_H

#include "clambhook/config.h"
#include "clambhook/error.h"

/* Reviews a Mihomo YAML or Surge profile request. The response includes the
 * validated canonical TOML and therefore must be handled as sensitive data. */
ch_status ch_profile_converter_review_request_json(const char *request_json,
                                                    char **out_json,
                                                    ch_error *error);

/* Re-runs conversion, verifies expected_sha256, and merges the selected
 * profile into current without writing it. The caller owns both outputs. */
ch_status ch_profile_converter_import_request_json(
    const ch_config *current, const char *request_json, char **out_toml,
    char **out_json, ch_error *error);

#endif
