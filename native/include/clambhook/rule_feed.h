// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_RULE_FEED_H
#define CLAMBHOOK_RULE_FEED_H

#include <stddef.h>
#include <stdint.h>

#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

#define CH_RULE_FEED_MAX_BYTES (5U * 1024U * 1024U)
#define CH_RULE_FEED_MAX_ENTRIES 200000U

typedef enum ch_rule_feed_kind {
    CH_RULE_FEED_RULE_SET = 1,
    CH_RULE_FEED_SUBSCRIPTION = 2
} ch_rule_feed_kind;

typedef struct ch_rule_feed {
    char *format;
    char **domain_suffixes;
    size_t domain_suffix_count;
    char **cidrs;
    size_t cidr_count;
    size_t skipped;
} ch_rule_feed;

typedef struct ch_rule_feed_cache {
    ch_rule_feed feed;
    char *profile;
    char *name;
    char *url;
    char *action;
    char **networks;
    size_t network_count;
    char *etag;
    char *last_modified;
    int64_t fetched_ts_ns;
} ch_rule_feed_cache;

typedef struct ch_rule_feed_refresh_options {
    const char *config_path;
    ch_rule_feed_kind kind;
    const char *profile;
    const char *name;
    const char *url;
    const char *format;
    const char *action;
    const char *const *networks;
    size_t network_count;
} ch_rule_feed_refresh_options;

/* Parses auto, plain, hosts, or adblock text using the frozen Go limits. */
ch_status ch_rule_feed_parse(const char *body, size_t length,
                             const char *format, ch_rule_feed *out_feed,
                             ch_error *error);
void ch_rule_feed_clear(ch_rule_feed *feed);

/*
 * Loads version-1 cache files written by either the Go or C implementation.
 * Cache identity is verified against profile, name, and URL before use.
 */
ch_status ch_rule_feed_cache_load(const char *config_path,
                                  ch_rule_feed_kind kind,
                                  const char *profile, const char *name,
                                  const char *url,
                                  ch_rule_feed_cache *out_cache,
                                  ch_error *error);
/* Atomically writes the Go-compatible version-1 cache path and JSON shape. */
ch_status ch_rule_feed_cache_write(const char *config_path,
                                   ch_rule_feed_kind kind,
                                   const ch_rule_feed_cache *cache,
                                   ch_error *error);

/* Metadata used by a platform HTTP client for conditional refreshes. */
ch_status ch_rule_feed_cache_metadata_json(const char *config_path,
                                           ch_rule_feed_kind kind,
                                           const char *profile,
                                           const char *name,
                                           const char *url,
                                           char **out_json,
                                           ch_error *error);

/* Parses a platform-fetched response and commits it in the shared cache. */
ch_status ch_rule_feed_cache_store_response(
    const ch_rule_feed_refresh_options *options,
    const char *body,
    size_t length,
    const char *etag,
    const char *last_modified,
    int64_t fetched_ts_ns,
    ch_error *error);

/* Advances freshness after a successful HTTP 304 response. */
ch_status ch_rule_feed_cache_touch(const char *config_path,
                                   ch_rule_feed_kind kind,
                                   const char *profile,
                                   const char *name,
                                   const char *url,
                                   int64_t fetched_ts_ns,
                                   ch_error *error);
/* Fetches with public-address validation, DNS pinning, and safe redirects. */
ch_status ch_rule_feed_refresh(const ch_rule_feed_refresh_options *options,
                               ch_error *error);
void ch_rule_feed_cache_clear(ch_rule_feed_cache *cache);

#ifdef __cplusplus
}
#endif

#endif
