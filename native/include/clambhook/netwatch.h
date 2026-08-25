#ifndef CLAMBHOOK_NETWATCH_H
#define CLAMBHOOK_NETWATCH_H

#include <stdbool.h>
#include <stddef.h>

#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

typedef struct uv_loop_s uv_loop_t;

#define CH_NETWORK_INTERFACE_CAPACITY 65U
#define CH_NETWORK_SSID_CAPACITY 256U

typedef struct ch_network_info {
    char interface_name[CH_NETWORK_INTERFACE_CAPACITY];
    char ssid[CH_NETWORK_SSID_CAPACITY];
    bool is_wifi;
} ch_network_info;

typedef ch_status (*ch_network_probe_callback)(
    ch_network_info *out_info,
    void *context,
    ch_error *error
);
typedef void (*ch_network_observation_callback)(
    const ch_network_info *info,
    void *context
);
typedef void (*ch_netwatch_log_callback)(
    int level,
    const char *message,
    void *context
);

typedef struct ch_netwatch_options {
    unsigned int poll_milliseconds;
    ch_network_probe_callback probe;
    void *probe_context;
    ch_network_observation_callback observation;
    void *observation_context;
    ch_netwatch_log_callback log;
    void *log_context;
} ch_netwatch_options;

typedef struct ch_netwatch ch_netwatch;

/* Polls immediately, emits changes on loop, and defaults to a 10 second poll. */
ch_netwatch *ch_netwatch_start(
    uv_loop_t *loop,
    const ch_netwatch_options *options,
    ch_error *error
);
/* Idempotently joins the probe worker, then releases the async handle. */
void ch_netwatch_stop(ch_netwatch *watcher);

/* Production platform probe: Darwin libexec tools or Linux kernel state. */
ch_status ch_netwatch_current(ch_network_info *out_info, void *context,
                              ch_error *error);

bool ch_network_info_matches(
    const ch_network_info *info,
    const char *ssid,
    const char *interface_name
);

/* Pure parsers exposed to freeze platform command/file contracts in tests. */
bool ch_netwatch_valid_interface_name(const char *name);
bool ch_netwatch_parse_scutil_interface(const char *text, char *out_interface,
                                        size_t capacity);
void ch_netwatch_parse_airport(const char *text, char *out_ssid,
                               size_t capacity, bool *out_is_wifi,
                               bool *out_associated);
bool ch_netwatch_parse_ipconfig_ssid(const char *text, char *out_ssid,
                                     size_t capacity);
bool ch_netwatch_parse_proc_wireless(const char *text, char *out_interface,
                                     size_t capacity);

#ifdef __cplusplus
}
#endif

#endif
