#ifndef CLAMBHOOK_TRAFFIC_H
#define CLAMBHOOK_TRAFFIC_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct ch_traffic_store ch_traffic_store;
struct ch_config;

typedef void (*ch_traffic_event_writer)(uint64_t shard_id,
                                        uint64_t lamport,
                                        const char *event_type,
                                        const char *data_json,
                                        void *context);

typedef struct ch_traffic_open_info {
    const char *profile;
    const char *listener_protocol;
    const char *listener_address;
    const char *client_address;
    const char *chain_name;
    const char *group_name;
    const char *rule_name;
    const char *rule_action;
    bool is_default;
    long long decision_ns;
    const char *target;
    const char *target_host;
    const char *target_port;
    const char *network;
    const char *source;
} ch_traffic_open_info;

typedef struct ch_traffic_connection {
    char *conn_id;
    char *profile;
    char *chain_name;
    char *group_name;
    char *rule_name;
    char *rule_action;
    char *target;
    char *target_host;
    char *target_port;
    char *network;
    char *source;
    bool is_default;
} ch_traffic_connection;

/* A bounded metadata-only store. Payload bytes are counted but never retained. */
ch_traffic_store *ch_traffic_store_create(size_t history_limit,
                                          ch_error *error);
void ch_traffic_store_set_event_writer(ch_traffic_store *store,
                                       ch_traffic_event_writer writer,
                                       void *context);
/* Applies root-level [traffic] settings and loads compatible version-1 history. */
ch_status ch_traffic_store_configure(ch_traffic_store *store,
                                     const struct ch_config *config,
                                     ch_error *error);
/* Atomically persists closed metadata history when a history path is set. */
ch_status ch_traffic_store_flush(ch_traffic_store *store, ch_error *error);
void ch_traffic_store_destroy(ch_traffic_store *store);

/* Returns a non-zero flow identifier on success. */
uint64_t ch_traffic_open(ch_traffic_store *store,
                         const ch_traffic_open_info *info,
                         ch_error *error);
void ch_traffic_bytes(ch_traffic_store *store, uint64_t flow_id,
                      uint64_t rx_delta, uint64_t tx_delta);
void ch_traffic_close(ch_traffic_store *store, uint64_t flow_id,
                      const char *reason);
void ch_traffic_close_all(ch_traffic_store *store, const char *reason);

ch_status ch_traffic_connection_copy(ch_traffic_store *store,
                                     const char *conn_id,
                                     ch_traffic_connection *out_connection,
                                     ch_error *error);
void ch_traffic_connection_clear(ch_traffic_connection *connection);

/*
 * Encodes the traffic monitor contract. filter_json accepts the Android/API
 * state/action/profile/rule/port/network/domain/query/limit/offset fields.
 */
char *ch_traffic_snapshot_json(ch_traffic_store *store,
                               const struct ch_config *config,
                               const char *active_profile,
                               const char *filter_json,
                               const char *temporary_rules_json,
                               ch_error *error);

/* Encodes the legacy decision-feed contract from routed connections. */
char *ch_traffic_decisions_json(ch_traffic_store *store,
                                const char *request_json,
                                ch_error *error);

/* Builds the validated create_rule mutation request for one stored flow. */
char *ch_traffic_rule_request_json(ch_traffic_store *store,
                                   const struct ch_config *config,
                                   const char *active_profile,
                                   const char *request_json,
                                   ch_error *error);

/* Verifies a live cleanup suggestion and builds a replace_rules request. */
char *ch_traffic_cleanup_request_json(ch_traffic_store *store,
                                      const struct ch_config *config,
                                      const char *active_profile,
                                      const char *request_json,
                                      ch_error *error);

#ifdef __cplusplus
}
#endif

#endif
