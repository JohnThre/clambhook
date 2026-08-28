// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_LWIP_CONTEXT_H
#define CLAMBHOOK_LWIP_CONTEXT_H

/*
 * lwIP's NO_SYS raw API is process-global. Every ClambHook IP interface,
 * including nested VPN transports, enters this recursive lock before touching
 * lwIP state. The recursive property is required because synchronous netif
 * callbacks can re-enter ClambHook while a public stack operation is active.
 */
void ch_lwip_context_initialize(void);
void ch_lwip_context_lock(void);
void ch_lwip_context_unlock(void);

#endif
