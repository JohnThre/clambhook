#ifndef CLAMBHOOK_PROTOCOL_TOR_H
#define CLAMBHOOK_PROTOCOL_TOR_H

#include "clambhook/config.h"
#include "clambhook/error.h"

ch_status ch_protocol_tor_dial(const ch_config_table *server,
                               int underlying_descriptor,
                               const char *target,
                               int *out_descriptor,
                               ch_error *error);

#endif
