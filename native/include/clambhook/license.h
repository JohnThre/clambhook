// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_LICENSE_H
#define CLAMBHOOK_LICENSE_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#include "clambhook/error.h"

#ifdef __cplusplus
extern "C" {
#endif

#define CH_ANNUAL_SUBSCRIPTION_PRICE_USD "79.99"
#define CH_INCLUDED_UPDATE_YEARS 1
#define CH_MAX_ACTIVE_DEVICES 6
#define CH_NEW_TRIAL_DAYS 7
#define CH_LEGACY_TRIAL_MONTHS 1
#define CH_OFFLINE_GRACE_DAYS 7
#define CH_LIFETIME_UNLOCK_PRODUCT_ID "org.jpfchang.clambhook.unlock.lifetime"
#define CH_FEATURE_UPDATE_PRODUCT_ID "org.jpfchang.clambhook.feature_update"
#define CH_LICENSE_VALIDATION_BASE_URL "https://store.swiphtgroup.com/clambhook/license"
#define CH_LICENSE_PORTAL_URL "https://store.swiphtgroup.com/clambhook/portal"

#define CH_FEATURE_TUNNEL_ROUTING "tunnel.routing"
#define CH_FEATURE_PROFILE_MANAGEMENT "profile.management"
#define CH_FEATURE_ROUTING_RULES "routing.rules"
#define CH_FEATURE_ACTIVITY_INSPECTION "activity.inspection"
#define CH_FEATURE_HTTP_METADATA "http.metadata"
#define CH_FEATURE_WIDGETS "widgets"

/* Nanoseconds since the Unix epoch, matching the frozen JSON precision. */
typedef int64_t ch_timestamp;

typedef struct ch_license_transaction {
    const char *product_id;
    ch_timestamp purchase_date;
    bool has_revocation_date;
    ch_timestamp revocation_date;
} ch_license_transaction;

typedef struct ch_license_snapshot {
    bool has_trial_start_date;
    ch_timestamp trial_start_date;
    bool has_trial_duration_days;
    int trial_duration_days;
    const ch_license_transaction *transactions;
    size_t transaction_count;
    bool has_last_verified_at;
    ch_timestamp last_verified_at;
    bool has_last_verification_failed_at;
    ch_timestamp last_verification_failed_at;
    ch_timestamp cached_at;
} ch_license_snapshot;

typedef enum ch_license_supporter_tier {
    CH_LICENSE_SUPPORTER_NONE = 0,
    CH_LICENSE_SUPPORTER_COPPER = 1,
    CH_LICENSE_SUPPORTER_SILVER = 2,
    CH_LICENSE_SUPPORTER_GOLD = 3
} ch_license_supporter_tier;

typedef struct ch_license_feature {
    const char *id;
    const char *display_name;
    ch_timestamp release_date;
} ch_license_feature;

typedef enum ch_license_access_reason {
    CH_LICENSE_REASON_TRIAL,
    CH_LICENSE_REASON_LIFETIME,
    CH_LICENSE_REASON_OFFLINE_GRACE,
    CH_LICENSE_REASON_LOCKED
} ch_license_access_reason;

typedef struct ch_license_decision {
    ch_license_access_reason reason;
    bool has_trial_start_date;
    ch_timestamp trial_start_date;
    bool has_trial_ends_at;
    ch_timestamp trial_ends_at;
    int trial_days_remaining;
    bool has_lifetime_unlock;
    bool has_update_cutoff_date;
    ch_timestamp update_cutoff_date;
    ch_license_supporter_tier supporter_tier;
    bool supporter_active;
    bool has_offline_grace_ends_at;
    ch_timestamp offline_grace_ends_at;
    char **unlocked_feature_ids;
    size_t unlocked_feature_count;
} ch_license_decision;

/* Creates midnight UTC without depending on the process time zone. */
ch_timestamp ch_license_utc_date(int year, unsigned month, unsigned day);
ch_timestamp ch_license_trial_end_date(ch_timestamp start);
ch_status ch_license_parse_rfc3339(const char *text, ch_timestamp *timestamp, ch_error *error);
char *ch_license_format_rfc3339(ch_timestamp timestamp);

ch_status ch_license_evaluate(
    const ch_license_snapshot *snapshot,
    const ch_license_feature *features,
    size_t feature_count,
    ch_timestamp now,
    ch_license_decision *decision,
    ch_error *error
);

void ch_license_decision_clear(ch_license_decision *decision);
const char *ch_license_reason_name(ch_license_access_reason reason);
const char *ch_license_supporter_tier_name(ch_license_supporter_tier tier);
bool ch_license_can_use_app(const ch_license_decision *decision);
bool ch_license_can_use_feature(const ch_license_decision *decision, const char *feature_id);
bool ch_license_can_install_update(
    const ch_license_decision *decision,
    bool has_published_at,
    ch_timestamp published_at,
    ch_timestamp now
);

#ifdef __cplusplus
}
#endif

#endif
