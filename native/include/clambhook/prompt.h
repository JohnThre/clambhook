#ifndef CLAMBHOOK_PROMPT_H
#define CLAMBHOOK_PROMPT_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct ch_prompt_manager ch_prompt_manager;
struct ch_config;

typedef struct ch_prompt_request {
    const char *conn_id;
    const char *profile;
    const char *network;
    const char *target;
    const char *target_host;
    const char *target_port;
    int process_pid;
    const char *process_name;
    const char *process_path;
    const char *code_sign_id;
    const char *code_sign_status;
    const char *would_use_chain;
    const char *would_use_group;
    const char *source;
} ch_prompt_request;

typedef struct ch_prompt_snapshot {
    char *id;
    char *conn_id;
    char *profile;
    char *network;
    char *target;
    char *target_host;
    char *target_port;
    int process_pid;
    char *process_name;
    char *process_path;
    char *code_sign_id;
    char *code_sign_status;
    char *would_use_chain;
    char *would_use_group;
    int64_t created_ns;
    int64_t expires_ns;
    size_t waiters;
} ch_prompt_snapshot;

typedef struct ch_prompt_action_options {
    char *id;
    char *scope;
    bool allow;
    bool match_host;
    bool match_port;
    bool match_protocol;
    long long ttl_seconds;
} ch_prompt_action_options;

ch_prompt_manager *ch_prompt_manager_create(ch_error *error);
void ch_prompt_manager_destroy(ch_prompt_manager *manager);

/* Applies root-level [prompt] settings. Reconfiguration wakes old waiters. */
ch_status ch_prompt_manager_configure(ch_prompt_manager *manager,
                                      const struct ch_config *config,
                                      ch_error *error);
bool ch_prompt_manager_enabled(ch_prompt_manager *manager);

/* Blocks until one coalesced prompt is resolved or times out. */
ch_status ch_prompt_manager_await(ch_prompt_manager *manager,
                                  const ch_prompt_request *request,
                                  bool *out_allow, bool *out_gated,
                                  ch_error *error);
void ch_prompt_manager_cancel_all(ch_prompt_manager *manager);

char *ch_prompt_manager_pending_json(ch_prompt_manager *manager,
                                     ch_error *error);
char *ch_prompt_manager_silent_json(ch_prompt_manager *manager,
                                    ch_error *error);
ch_status ch_prompt_manager_resolve(ch_prompt_manager *manager,
                                    const char *id, bool allow,
                                    ch_prompt_snapshot *out_snapshot,
                                    ch_error *error);
ch_status ch_prompt_manager_silent_decision(ch_prompt_manager *manager,
                                            const char *id,
                                            ch_prompt_snapshot *out_snapshot,
                                            bool *out_allow,
                                            ch_error *error);
void ch_prompt_snapshot_clear(ch_prompt_snapshot *snapshot);

/* Parses resolve/promote JSON. Resolve requests require allow/block action;
 * promote requests inherit the recorded action. */
ch_status ch_prompt_action_options_parse(const char *request_json,
                                         bool require_action,
                                         ch_prompt_action_options *options,
                                         ch_error *error);
void ch_prompt_action_options_clear(ch_prompt_action_options *options);

/* Builds the persisted-rule mutation used by prompt remember actions. */
char *ch_prompt_rule_request_json(const ch_prompt_snapshot *snapshot,
                                  const struct ch_config *config,
                                  bool allow, bool match_host,
                                  bool match_port, bool match_protocol,
                                  ch_error *error);

#ifdef __cplusplus
}
#endif

#endif
