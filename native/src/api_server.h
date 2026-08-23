#ifndef CLAMBHOOK_API_SERVER_H
#define CLAMBHOOK_API_SERVER_H

#include <uv.h>

#include "clambhook/error.h"
#include "clambhook/runtime.h"

typedef struct ch_api_server ch_api_server;

ch_api_server *ch_api_server_start(
    uv_loop_t *loop,
    ch_runtime *runtime,
    const char *address,
    const char *auth_token,
    ch_error *error
);
void ch_api_server_stop(ch_api_server *server);
const char *ch_api_server_address(const ch_api_server *server);

/* Shared by the Host/Origin guard and native security regression tests. */
int ch_api_is_loopback_host(const char *host);

#endif
