// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_LWIP_ARCH_CC_H
#define CLAMBHOOK_LWIP_ARCH_CC_H

#include <stdint.h>

#ifdef BYTE_ORDER
#undef BYTE_ORDER
#endif
#if defined(__BYTE_ORDER__) && __BYTE_ORDER__ == __ORDER_BIG_ENDIAN__
#define BYTE_ORDER BIG_ENDIAN
#else
#define BYTE_ORDER LITTLE_ENDIAN
#endif

uint32_t ch_lwip_random(void);
#define LWIP_RAND() ch_lwip_random()

#endif
