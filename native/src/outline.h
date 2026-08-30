// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_OUTLINE_H
#define CLAMBHOOK_OUTLINE_H

#include "clambhook/config.h"
#include "clambhook/error.h"

/* Produces a redacted review from {"access_key":"..."}. Dynamic keys are
 * fetched through the hardened native HTTP path. */
ch_status ch_outline_review_request_json(const char *request_json,
                                         char **out_json,
                                         ch_error *error);

/* Parses an already fetched document for deterministic fixtures. It performs
 * the same JSON/YAML/ss validation as dynamic-key retrieval. */
ch_status ch_outline_review_document_json(const char *document,
                                          char **out_json,
                                          ch_error *error);

/* Resolves {access_key, profile_name, activate} into the private mutation
 * request consumed by config_mutation.c. The returned JSON contains secrets
 * and must never be logged. */
ch_status ch_outline_import_mutation_request_json(const char *request_json,
                                                  char **out_json,
                                                  ch_error *error);

/* Resolves the stored dynamic key for a configured profile and returns the
 * private mutation request used to replace only that Outline server. */
ch_status ch_outline_refresh_mutation_request_json(const ch_config *config,
                                                   const char *request_json,
                                                   char **out_json,
                                                   ch_error *error);

#endif
