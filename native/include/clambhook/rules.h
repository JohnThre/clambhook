#ifndef CLAMBHOOK_RULES_H
#define CLAMBHOOK_RULES_H

#include <stdbool.h>
#include <stddef.h>

#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

#define CH_RULE_ACTION_CHAIN "chain"
#define CH_RULE_ACTION_GROUP "group"
#define CH_RULE_ACTION_DIRECT "direct"
#define CH_RULE_ACTION_BLOCK "block"
#define CH_RULE_ACTION_REJECT "reject"

typedef struct ch_string_list {
    const char *const *items;
    size_t count;
} ch_string_list;

typedef struct ch_port_list {
    const int *items;
    size_t count;
} ch_port_list;

typedef struct ch_rule_spec {
    const char *name;
    const char *action;
    ch_string_list rule_sets;
    ch_string_list domains;
    ch_string_list domain_suffixes;
    ch_string_list domain_keywords;
    ch_string_list cidrs;
    ch_string_list source_cidrs;
    ch_port_list ports;
    ch_string_list networks;
    ch_string_list processes;
} ch_rule_spec;

typedef struct ch_rule_set_spec {
    const char *name;
    ch_string_list domains;
    ch_string_list domain_suffixes;
    ch_string_list domain_keywords;
    ch_string_list cidrs;
} ch_rule_set_spec;

typedef struct ch_rule_match_context {
    const char *network;
    const char *target;
    const char *source;
    const char *process_name;
    const char *process_path;
} ch_rule_match_context;

typedef struct ch_rule_decision {
    char *rule_name;
    size_t rule_number;
    char *action;
    char *chain_name;
    char *group_name;
    char *target;
    char *host;
    char *port;
    char *network;
    char *source;
    bool is_default;
    long long elapsed_ns;
    char *matcher_kind;
    char *matcher_value;
    char *summary;
} ch_rule_decision;

typedef struct ch_rule_engine ch_rule_engine;
struct ch_config;

ch_rule_engine *ch_rule_engine_compile(
    const ch_rule_spec *rules,
    size_t rule_count,
    const char *default_chain,
    ch_string_list known_chains,
    ch_string_list known_groups,
    const ch_rule_set_spec *known_rule_sets,
    size_t known_rule_set_count,
    ch_error *error
);

/* Compiles the selected validated TOML profile into the native rule engine. */
ch_rule_engine *ch_rule_engine_compile_config(
    const struct ch_config *config,
    const char *profile_name,
    ch_error *error
);

/* Returns the frozen route-explanation JSON contract for one match context. */
ch_status ch_rule_explain_config_json(
    const struct ch_config *config,
    const char *profile_name,
    const ch_rule_match_context *context,
    char **out_json,
    ch_error *error
);

void ch_rule_engine_destroy(ch_rule_engine *engine);
ch_status ch_rule_engine_decide(
    const ch_rule_engine *engine,
    const ch_rule_match_context *context,
    ch_rule_decision *decision,
    ch_error *error
);
void ch_rule_decision_clear(ch_rule_decision *decision);

#ifdef __cplusplus
}
#endif

#endif
