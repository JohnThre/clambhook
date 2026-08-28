// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_TEMPORARY_RULES_H
#define CLAMBHOOK_TEMPORARY_RULES_H

#include <stdbool.h>
#include <stddef.h>

#include "clambhook/error.h"
#include "clambhook/rules.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct ch_temporary_rules ch_temporary_rules;
struct ch_config;
struct ch_traffic_store;

ch_temporary_rules *ch_temporary_rules_create(size_t limit, ch_error *error);
void ch_temporary_rules_destroy(ch_temporary_rules *rules);

/* Creates a temporary rule from a C-owned traffic record. */
char *ch_temporary_rules_create_from_connection_json(
    ch_temporary_rules *rules,
    struct ch_traffic_store *traffic,
    const struct ch_config *config,
    const char *active_profile,
    const char *request_json,
    ch_error *error
);

/* Installs a rule mutation produced by the prompt rule builder. A zero
 * until_quit_pid creates a normal TTL-bound session rule. */
char *ch_temporary_rules_create_from_rule_json(
    ch_temporary_rules *rules,
    const char *rule_request_json,
    long long ttl_seconds,
    int until_quit_pid,
    const char *source_conn_id,
    const char *source_target,
    const char *source_target_host,
    ch_error *error
);

/* True when live temporary rules require process attribution to match. */
bool ch_temporary_rules_needs_process(ch_temporary_rules *rules);

/* Removes one live rule by JSON {"id":"..."}. */
char *ch_temporary_rules_remove_json(ch_temporary_rules *rules,
                                     const char *request_json,
                                     ch_error *error);

/* Newest matching rule wins. The caller clears a successful decision. */
ch_status ch_temporary_rules_decide(
    ch_temporary_rules *rules,
    const char *profile,
    const ch_rule_match_context *context,
    ch_rule_decision *decision,
    bool *matched,
    ch_error *error
);

/* Returns a JSON array of non-expired rules for profile (empty means all). */
char *ch_temporary_rules_snapshot_json(ch_temporary_rules *rules,
                                       const char *profile,
                                       ch_error *error);
char *ch_temporary_rules_payload_json(ch_temporary_rules *rules,
                                      const char *profile,
                                      ch_error *error);

#ifdef __cplusplus
}
#endif

#endif
