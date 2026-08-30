// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_MEMORY_TESTING_H
#define CLAMBHOOK_MEMORY_TESTING_H

#include <stddef.h>

#include "clambhook/ip_stack.h"
#include "tunnel_stack.h"

#ifdef CLAMBHOOK_MEMORY_TESTING
void ch_ip_stack_memory_stats(ch_ip_stack *stack, size_t *buffer_allocations,
                              size_t *buffer_appends,
                              size_t *emitted_packets);
void ch_tunnel_stack_memory_stats(ch_tunnel_stack *stack,
                                  size_t *injected_packets,
                                  size_t *payload_allocations,
                                  size_t *copied_bytes);
void ch_tunnel_stack_stop_for_testing(ch_tunnel_stack *stack);
#endif

#endif
