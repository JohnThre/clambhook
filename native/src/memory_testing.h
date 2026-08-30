// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_MEMORY_TESTING_H
#define CLAMBHOOK_MEMORY_TESTING_H

#include <stddef.h>

#include "clambhook/ip_stack.h"

#ifdef CLAMBHOOK_MEMORY_TESTING
void ch_ip_stack_memory_stats(ch_ip_stack *stack, size_t *buffer_allocations,
                              size_t *buffer_appends,
                              size_t *emitted_packets);
#endif

#endif
