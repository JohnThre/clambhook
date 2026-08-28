// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_POLICY_H
#define CLAMBHOOK_POLICY_H

#include <stdint.h>

#include "clambhook/config.h"
#include "clambhook/error.h"

typedef struct ch_policy_manager ch_policy_manager;

typedef struct ch_policy_probe_result {
    int healthy;
    int64_t latency_ns;
    int status_code;
    int64_t last_test_ts_ns;
    char error[256];
} ch_policy_probe_result;

typedef ch_status (*ch_policy_probe_callback)(
    const ch_config_table *chain,
    const char *test_url,
    unsigned int timeout_milliseconds,
    ch_policy_probe_result *out_result,
    void *context,
    ch_error *error
);

typedef struct ch_policy_options {
    ch_policy_probe_callback probe;
    void *probe_context;
} ch_policy_options;

ch_policy_manager *ch_policy_manager_create(
    const ch_config *config,
    const char *profile_name,
    const ch_policy_options *options,
    ch_error *error
);
ch_status ch_policy_manager_start(ch_policy_manager *manager, ch_error *error);
void ch_policy_manager_stop(ch_policy_manager *manager);
void ch_policy_manager_destroy(ch_policy_manager *manager);

ch_status ch_policy_manager_select(
    ch_policy_manager *manager,
    const char *group_name,
    const char *network,
    const char *target,
    const char *source,
    char **out_chain_name,
    ch_error *error
);

/* Runs one synchronous refresh. An empty group name refreshes every group. */
ch_status ch_policy_manager_refresh(ch_policy_manager *manager,
                                    const char *group_name,
                                    ch_error *error);

char *ch_policy_manager_snapshot_json(ch_policy_manager *manager,
                                      const char *profile_name,
                                      ch_error *error);

#endif
