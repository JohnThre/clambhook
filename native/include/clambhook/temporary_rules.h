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

#ifdef __cplusplus
}
#endif

#endif
