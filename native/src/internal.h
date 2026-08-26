#ifndef CLAMBHOOK_INTERNAL_H
#define CLAMBHOOK_INTERNAL_H

#include <stdarg.h>
#include <stddef.h>

#include "clambhook/dns.h"
#include "clambhook/error.h"

void ch_error_set(ch_error *error, ch_status code, const char *format, ...)
    __attribute__((format(printf, 3, 4)));
char *ch_strdup(const char *value);

typedef struct ch_json_buffer {
    char *data;
    size_t length;
    size_t capacity;
    int failed;
} ch_json_buffer;

void ch_json_init(ch_json_buffer *buffer);
void ch_json_dispose(ch_json_buffer *buffer);
int ch_json_append(ch_json_buffer *buffer, const char *value);
int ch_json_append_format(ch_json_buffer *buffer, const char *format, ...)
    __attribute__((format(printf, 2, 3)));
int ch_json_append_string(ch_json_buffer *buffer, const char *value);
struct ch_json_value;
int ch_json_append_value(ch_json_buffer *buffer, const struct ch_json_value *value);
char *ch_json_take(ch_json_buffer *buffer);

struct ch_runtime_listener_set;
typedef struct ch_runtime_listener_set ch_runtime_listener_set;
struct ch_config;
ch_runtime_listener_set *ch_runtime_listener_set_start(
    const struct ch_config *config,
    const char *profile_name,
    ch_error *error
);
void ch_runtime_listener_set_stop(ch_runtime_listener_set *set);
int ch_runtime_listener_set_append_status(ch_runtime_listener_set *set,
                                          ch_json_buffer *json);
ch_status ch_runtime_listener_set_dns_route(
    ch_runtime_listener_set *set,
    const char *network,
    const char *target,
    ch_dns_route_action *out_action,
    ch_error *error
);
ch_status ch_runtime_listener_set_dns_dial(
    ch_runtime_listener_set *set,
    const char *network,
    const char *target,
    const char *const *bootstrap_ips,
    size_t bootstrap_ip_count,
    int *out_descriptor,
    ch_error *error
);
ch_status ch_runtime_listener_set_tun_tcp_dial(
    ch_runtime_listener_set *set,
    const char *target,
    const char *source,
    int *out_descriptor,
    ch_error *error
);

char *ch_config_collection_payload_json(const struct ch_config *config,
                                        const char *fallback_profile,
                                        const char *config_key,
                                        const char *payload_key,
                                        int include_rule_fields,
                                        int include_statuses,
                                        ch_error *error);
char *ch_config_servers_payload_json(const struct ch_config *config,
                                     const char *fallback_profile,
                                     ch_error *error);
char *ch_config_profile_payload_json(const struct ch_config *config,
                                     const char *profile_name,
                                     ch_error *error);
int ch_config_has_profile(const struct ch_config *config, const char *name);
char *ch_json_request_string(const char *request_json, const char *key,
                             ch_error *error);
ch_status ch_rule_explain_request_json(const struct ch_config *config,
                                       const char *fallback_profile,
                                       const char *request_json,
                                       char **out_json,
                                       ch_error *error);

#endif
