#include "clambhook/license_json.h"

#include <ctype.h>
#include <limits.h>
#include <math.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <strings.h>
#include <time.h>

#include <openssl/rand.h>

#include "clambhook/json.h"
#include "clambhook/license.h"
#include "internal.h"

typedef struct ch_decoded_snapshot {
    ch_license_snapshot snapshot;
    ch_license_transaction *transactions;
    char **product_ids;
    bool transactions_array;
} ch_decoded_snapshot;

static ch_timestamp ch_license_now(int64_t unix_millis) {
    if (unix_millis > 0 && unix_millis <= INT64_MAX / INT64_C(1000000)) {
        return unix_millis * INT64_C(1000000);
    }
    struct timespec now;
    if (clock_gettime(CLOCK_REALTIME, &now) != 0) return 0;
    return (ch_timestamp)now.tv_sec * INT64_C(1000000000) + (ch_timestamp)now.tv_nsec;
}

static void ch_decoded_snapshot_clear(ch_decoded_snapshot *decoded) {
    if (decoded == NULL) return;
    for (size_t index = 0U; index < decoded->snapshot.transaction_count; ++index) {
        free(decoded->product_ids[index]);
    }
    free(decoded->product_ids);
    free(decoded->transactions);
    memset(decoded, 0, sizeof(*decoded));
}

static ch_status ch_decode_optional_time(
    const ch_json_value *object,
    const char *key,
    bool *present,
    ch_timestamp *timestamp,
    ch_error *error
) {
    const ch_json_value *value = ch_json_object_get(object, key);
    if (value == NULL || ch_json_value_type(value) == CH_JSON_NULL) {
        *present = false;
        *timestamp = 0;
        return CH_OK;
    }
    const char *text = ch_json_string_value(value);
    if (text == NULL) {
        ch_error_set(error, CH_ERROR_PARSE, "license: %s must be an RFC3339 string or null", key);
        return CH_ERROR_PARSE;
    }
    ch_status status = ch_license_parse_rfc3339(text, timestamp, error);
    if (status == CH_OK) *present = true;
    return status;
}

static ch_status ch_decode_snapshot(
    const char *snapshot_json,
    ch_decoded_snapshot *decoded,
    ch_error *error
) {
    memset(decoded, 0, sizeof(*decoded));
    const char *text = snapshot_json == NULL ? "" : snapshot_json;
    while (isspace((unsigned char)*text)) ++text;
    if (*text == '\0') return CH_OK;
    ch_json_value *root = ch_json_parse(text, strlen(text), error);
    if (root == NULL) return error == NULL ? CH_ERROR_PARSE : error->code;
    if (ch_json_value_type(root) != CH_JSON_OBJECT) {
        ch_json_value_destroy(root);
        ch_error_set(error, CH_ERROR_PARSE, "license: snapshot must be a JSON object");
        return CH_ERROR_PARSE;
    }
    ch_status status = ch_decode_optional_time(
        root, "trialStartDate", &decoded->snapshot.has_trial_start_date,
        &decoded->snapshot.trial_start_date, error
    );
    if (status == CH_OK) status = ch_decode_optional_time(
        root, "lastVerifiedAt", &decoded->snapshot.has_last_verified_at,
        &decoded->snapshot.last_verified_at, error
    );
    if (status == CH_OK) status = ch_decode_optional_time(
        root, "lastVerificationFailedAt", &decoded->snapshot.has_last_verification_failed_at,
        &decoded->snapshot.last_verification_failed_at, error
    );
    const ch_json_value *cached = ch_json_object_get(root, "cachedAt");
    if (status == CH_OK && cached != NULL && ch_json_value_type(cached) != CH_JSON_NULL) {
        const char *cached_text = ch_json_string_value(cached);
        if (cached_text == NULL) {
            ch_error_set(error, CH_ERROR_PARSE, "license: cachedAt must be an RFC3339 string");
            status = CH_ERROR_PARSE;
        } else {
            status = ch_license_parse_rfc3339(cached_text, &decoded->snapshot.cached_at, error);
        }
    }
    const ch_json_value *transactions = ch_json_object_get(root, "transactions");
    if (status == CH_OK && transactions != NULL && ch_json_value_type(transactions) != CH_JSON_NULL) {
        if (ch_json_value_type(transactions) != CH_JSON_ARRAY) {
            ch_error_set(error, CH_ERROR_PARSE, "license: transactions must be an array or null");
            status = CH_ERROR_PARSE;
        } else {
            decoded->transactions_array = true;
            size_t count = ch_json_array_size(transactions);
            if (count > SIZE_MAX / sizeof(*decoded->transactions) ||
                count > SIZE_MAX / sizeof(*decoded->product_ids)) {
                status = CH_ERROR_OUT_OF_MEMORY;
                ch_error_set(error, status, "license: transaction list is too large");
            } else if (count > 0U) {
                decoded->transactions = calloc(count, sizeof(*decoded->transactions));
                decoded->product_ids = calloc(count, sizeof(*decoded->product_ids));
                if (decoded->transactions == NULL || decoded->product_ids == NULL) {
                    status = CH_ERROR_OUT_OF_MEMORY;
                    ch_error_set(error, status, "license: allocate transactions");
                }
            }
            for (size_t index = 0U; status == CH_OK && index < count; ++index) {
                const ch_json_value *item = ch_json_array_get(transactions, index);
                if (ch_json_value_type(item) != CH_JSON_OBJECT) {
                    ch_error_set(error, CH_ERROR_PARSE, "license: transaction must be an object");
                    status = CH_ERROR_PARSE;
                    break;
                }
                const char *product = ch_json_string_value(ch_json_object_get(item, "productID"));
                decoded->product_ids[index] = ch_strdup(product == NULL ? "" : product);
                if (decoded->product_ids[index] == NULL) {
                    ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "license: copy product identifier");
                    status = CH_ERROR_OUT_OF_MEMORY;
                    break;
                }
                decoded->transactions[index].product_id = decoded->product_ids[index];
                bool purchase_present = false;
                status = ch_decode_optional_time(
                    item, "purchaseDate", &purchase_present,
                    &decoded->transactions[index].purchase_date, error
                );
                if (status == CH_OK) status = ch_decode_optional_time(
                    item, "revocationDate", &decoded->transactions[index].has_revocation_date,
                    &decoded->transactions[index].revocation_date, error
                );
                decoded->snapshot.transaction_count = index + 1U;
            }
            decoded->snapshot.transactions = decoded->transactions;
        }
    }
    ch_json_value_destroy(root);
    if (status != CH_OK) ch_decoded_snapshot_clear(decoded);
    return status;
}

static ch_status ch_decode_grant_snapshot(
    const ch_json_value *value,
    ch_decoded_snapshot *decoded,
    ch_timestamp now,
    ch_error *error
) {
    memset(decoded, 0, sizeof(*decoded));
    if (ch_json_value_type(value) != CH_JSON_OBJECT) {
        ch_error_set(error, CH_ERROR_PARSE, "license: server snapshot must be an object");
        return CH_ERROR_PARSE;
    }
    ch_status status = ch_decode_optional_time(
        value,
        "trial_start_date",
        &decoded->snapshot.has_trial_start_date,
        &decoded->snapshot.trial_start_date,
        error
    );
    const ch_json_value *transactions = ch_json_object_get(value, "transactions");
    if (status == CH_OK && transactions != NULL && ch_json_value_type(transactions) != CH_JSON_NULL) {
        if (ch_json_value_type(transactions) != CH_JSON_ARRAY) {
            ch_error_set(error, CH_ERROR_PARSE, "license: server transactions must be an array or null");
            status = CH_ERROR_PARSE;
        } else {
            decoded->transactions_array = true;
            size_t count = ch_json_array_size(transactions);
            if (count > SIZE_MAX / sizeof(*decoded->transactions) ||
                count > SIZE_MAX / sizeof(*decoded->product_ids)) {
                ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "license: server transaction list is too large");
                status = CH_ERROR_OUT_OF_MEMORY;
            } else if (count > 0U) {
                decoded->transactions = calloc(count, sizeof(*decoded->transactions));
                decoded->product_ids = calloc(count, sizeof(*decoded->product_ids));
                if (decoded->transactions == NULL || decoded->product_ids == NULL) {
                    ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "license: allocate server transactions");
                    status = CH_ERROR_OUT_OF_MEMORY;
                }
            }
            for (size_t index = 0U; status == CH_OK && index < count; ++index) {
                const ch_json_value *item = ch_json_array_get(transactions, index);
                if (ch_json_value_type(item) != CH_JSON_OBJECT) {
                    ch_error_set(error, CH_ERROR_PARSE, "license: server transaction must be an object");
                    status = CH_ERROR_PARSE;
                    break;
                }
                const char *product = ch_json_string_value(ch_json_object_get(item, "productID"));
                decoded->product_ids[index] = ch_strdup(product == NULL ? "" : product);
                if (decoded->product_ids[index] == NULL) {
                    ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "license: copy server product identifier");
                    status = CH_ERROR_OUT_OF_MEMORY;
                    break;
                }
                decoded->transactions[index].product_id = decoded->product_ids[index];
                bool purchase_present = false;
                status = ch_decode_optional_time(
                    item,
                    "purchaseDate",
                    &purchase_present,
                    &decoded->transactions[index].purchase_date,
                    error
                );
                if (status == CH_OK && !purchase_present) {
                    ch_error_set(error, CH_ERROR_PARSE, "license: server purchaseDate is required");
                    status = CH_ERROR_PARSE;
                }
                if (status == CH_OK) status = ch_decode_optional_time(
                    item,
                    "revocationDate",
                    &decoded->transactions[index].has_revocation_date,
                    &decoded->transactions[index].revocation_date,
                    error
                );
                decoded->snapshot.transaction_count = index + 1U;
            }
            decoded->snapshot.transactions = decoded->transactions;
        }
    }
    decoded->snapshot.has_last_verified_at = true;
    decoded->snapshot.last_verified_at = now;
    decoded->snapshot.cached_at = now;
    if (status != CH_OK) ch_decoded_snapshot_clear(decoded);
    return status;
}

static int ch_append_time(ch_json_buffer *json, ch_timestamp timestamp) {
    char *formatted = ch_license_format_rfc3339(timestamp);
    if (formatted == NULL) return 0;
    int ok = ch_json_append_string(json, formatted);
    free(formatted);
    return ok;
}

static int ch_append_optional_time(ch_json_buffer *json, bool present, ch_timestamp timestamp) {
    return present ? ch_append_time(json, timestamp) : ch_json_append(json, "null");
}

static int ch_append_snapshot(ch_json_buffer *json, const ch_decoded_snapshot *decoded) {
    const ch_license_snapshot *snapshot = &decoded->snapshot;
    if (!ch_json_append(json, "{\"trialStartDate\":" ) ||
        !ch_append_optional_time(json, snapshot->has_trial_start_date, snapshot->trial_start_date) ||
        !ch_json_append(json, ",\"transactions\":")) return 0;
    if (!decoded->transactions_array && snapshot->transaction_count == 0U) {
        if (!ch_json_append(json, "null")) return 0;
    } else {
        if (!ch_json_append(json, "[")) return 0;
        for (size_t index = 0U; index < snapshot->transaction_count; ++index) {
            const ch_license_transaction *transaction = &snapshot->transactions[index];
            if (index > 0U && !ch_json_append(json, ",")) return 0;
            if (!ch_json_append(json, "{\"productID\":") ||
                !ch_json_append_string(json, transaction->product_id) ||
                !ch_json_append(json, ",\"purchaseDate\":") ||
                !ch_append_time(json, transaction->purchase_date)) return 0;
            if (transaction->has_revocation_date &&
                (!ch_json_append(json, ",\"revocationDate\":") ||
                 !ch_append_time(json, transaction->revocation_date))) return 0;
            if (!ch_json_append(json, "}")) return 0;
        }
        if (!ch_json_append(json, "]")) return 0;
    }
    return ch_json_append(json, ",\"lastVerifiedAt\":") &&
        ch_append_optional_time(json, snapshot->has_last_verified_at, snapshot->last_verified_at) &&
        ch_json_append(json, ",\"lastVerificationFailedAt\":") &&
        ch_append_optional_time(
            json, snapshot->has_last_verification_failed_at,
            snapshot->last_verification_failed_at
        ) && ch_json_append(json, ",\"cachedAt\":") &&
        ch_append_time(json, snapshot->cached_at) && ch_json_append(json, "}");
}

static int ch_append_decision(ch_json_buffer *json, const ch_license_decision *decision) {
    if (!ch_json_append(json, "{\"reason\":") ||
        !ch_json_append_string(json, ch_license_reason_name(decision->reason)) ||
        !ch_json_append(json, ",\"trialStartDate\":") ||
        !ch_append_optional_time(json, decision->has_trial_start_date, decision->trial_start_date) ||
        !ch_json_append(json, ",\"trialEndsAt\":") ||
        !ch_append_optional_time(json, decision->has_trial_ends_at, decision->trial_ends_at) ||
        !ch_json_append_format(json, ",\"trialDaysRemaining\":%d,\"hasLifetimeUnlock\":%s,\"updateCutoffDate\":",
            decision->trial_days_remaining, decision->has_lifetime_unlock ? "true" : "false") ||
        !ch_append_optional_time(
            json, decision->has_update_cutoff_date, decision->update_cutoff_date
        ) || !ch_json_append(json, ",\"offlineGraceEndsAt\":") ||
        !ch_append_optional_time(
            json, decision->has_offline_grace_ends_at, decision->offline_grace_ends_at
        ) || !ch_json_append(json, ",\"unlockedFeatureIDs\":[")) return 0;
    for (size_t index = 0U; index < decision->unlocked_feature_count; ++index) {
        if ((index > 0U && !ch_json_append(json, ",")) ||
            !ch_json_append_string(json, decision->unlocked_feature_ids[index])) return 0;
    }
    return ch_json_append(json, "]}");
}

static int ch_format_display_date(ch_timestamp timestamp, char output[32]) {
    static const char *const months[] = {
        "Jan", "Feb", "Mar", "Apr", "May", "Jun",
        "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"
    };
    char *rfc3339 = ch_license_format_rfc3339(timestamp);
    if (rfc3339 == NULL) return 0;
    int year = 0;
    unsigned month = 0U;
    unsigned day = 0U;
    int fields = sscanf(rfc3339, "%4d-%2u-%2u", &year, &month, &day);
    free(rfc3339);
    if (fields != 3 || month < 1U || month > 12U || day < 1U || day > 31U) return 0;
    int count = snprintf(output, 32U, "%s %u, %d", months[month - 1U], day, year);
    return count > 0 && count < 32;
}

static int ch_append_product_state(
    ch_json_buffer *json,
    const char *kind,
    const char *title,
    const char *detail,
    bool active
) {
    return ch_json_append(json, "{\"kind\":") && ch_json_append_string(json, kind) &&
        ch_json_append(json, ",\"title\":") && ch_json_append_string(json, title) &&
        ch_json_append(json, ",\"detail\":") && ch_json_append_string(json, detail) &&
        ch_json_append_format(json, ",\"isActive\":%s}", active ? "true" : "false");
}

static int ch_append_product_states(
    ch_json_buffer *json,
    const ch_license_decision *decision
) {
    char trial_detail[96];
    if (decision->has_trial_ends_at) {
        char date[32];
        if (!ch_format_display_date(decision->trial_ends_at, date)) return 0;
        int count = snprintf(
            trial_detail,
            sizeof(trial_detail),
            decision->reason == CH_LICENSE_REASON_TRIAL ? "Trial ends %s." : "Trial ended %s.",
            date
        );
        if (count < 0 || (size_t)count >= sizeof(trial_detail)) return 0;
    } else {
        (void)snprintf(
            trial_detail,
            sizeof(trial_detail),
            "%s",
            "Trial starts the first time this app records an access date."
        );
    }
    if (!ch_json_append(json, "[") || !ch_append_product_state(
        json,
        "trial",
        "One-calendar-month trial",
        trial_detail,
        decision->reason == CH_LICENSE_REASON_TRIAL
    ) || !ch_json_append(json, ",") || !ch_append_product_state(
        json,
        "lifetimeUnlocked",
        "ClambHook license",
        decision->has_lifetime_unlock
            ? "Versions released during the included update window remain usable."
            : "Buy or activate a ClambHook license to keep using ClambHook after free access.",
        decision->has_lifetime_unlock
    ) || !ch_json_append(json, ",")) return 0;

    if (decision->has_update_cutoff_date) {
        char date[32];
        char title[96];
        if (!ch_format_display_date(decision->update_cutoff_date, date)) return 0;
        int count = snprintf(title, sizeof(title), "Included updates through %s", date);
        if (count < 0 || (size_t)count >= sizeof(title) || !ch_append_product_state(
            json,
            "paidUpdateWindow",
            title,
            "All updates released on or before this date are included, and those app versions remain usable.",
            decision->has_lifetime_unlock
        )) return 0;
    } else if (!ch_append_product_state(
        json,
        "paidUpdateWindow",
        "Included updates through DATE",
        "A ClambHook license includes one year of all updates from the purchase date.",
        false
    )) {
        return 0;
    }

    static const ch_license_feature features[] = {
        {CH_FEATURE_TUNNEL_ROUTING, "Tunnel Routing", 0},
        {CH_FEATURE_PROFILE_MANAGEMENT, "Profile Management", 0},
        {CH_FEATURE_ROUTING_RULES, "Routing Rules", 0},
        {CH_FEATURE_ACTIVITY_INSPECTION, "Activity Inspection", 0},
        {CH_FEATURE_HTTP_METADATA, "HTTP Metadata", 0},
        {CH_FEATURE_WIDGETS, "Widgets", 0}
    };
    size_t locked_count = 0U;
    ch_json_buffer locked_names;
    ch_json_init(&locked_names);
    ch_timestamp release_date = ch_license_utc_date(2026, 6U, 3U);
    if (decision->has_update_cutoff_date) {
        for (size_t index = 0U; index < sizeof(features) / sizeof(features[0]); ++index) {
            if (release_date > decision->update_cutoff_date) {
                if ((locked_count > 0U && !ch_json_append(&locked_names, ", ")) ||
                    !ch_json_append(&locked_names, features[index].display_name)) {
                    ch_json_dispose(&locked_names);
                    return 0;
                }
                ++locked_count;
            }
        }
    }
    const char *default_detail =
        "All updates released after the cutoff, including critical, bug, and security updates, require a USD 9.99 update-year renewal.";
    char *locked_detail = NULL;
    if (locked_count > 0U) {
        ch_json_buffer detail;
        ch_json_init(&detail);
        if (ch_json_append(&detail, "Updates requiring renewal include: ") &&
            ch_json_append(&detail, locked_names.data) && ch_json_append(&detail, ".")) {
            locked_detail = ch_json_take(&detail);
        }
        ch_json_dispose(&detail);
        if (locked_detail == NULL) {
            ch_json_dispose(&locked_names);
            return 0;
        }
    }
    int ok = ch_json_append(json, ",") && ch_append_product_state(
        json,
        "newFeaturesLocked",
        "Later updates require renewal",
        locked_detail == NULL ? default_detail : locked_detail,
        locked_count > 0U
    ) && ch_json_append(json, "]");
    free(locked_detail);
    ch_json_dispose(&locked_names);
    return ok;
}

static int ch_append_recovery(
    ch_json_buffer *json,
    const char *kind,
    const char *title,
    const char *message,
    const char *primary,
    const char *const *secondary,
    size_t secondary_count,
    const char *diagnostic
) {
    if (!ch_json_append(json, "{\"kind\":") || !ch_json_append_string(json, kind) ||
        !ch_json_append(json, ",\"severity\":\"warning\",\"title\":") ||
        !ch_json_append_string(json, title) || !ch_json_append(json, ",\"message\":") ||
        !ch_json_append_string(json, message) || !ch_json_append(json, ",\"primaryAction\":") ||
        !ch_json_append_string(json, primary) ||
        !ch_json_append(json, ",\"secondaryActions\":[")) return 0;
    for (size_t index = 0U; index < secondary_count; ++index) {
        if ((index > 0U && !ch_json_append(json, ",")) ||
            !ch_json_append_string(json, secondary[index])) return 0;
    }
    return ch_json_append(json, "],\"diagnosticText\":") &&
        ch_json_append_string(json, diagnostic == NULL ? "" : diagnostic) &&
        ch_json_append(json, "}");
}

static int ch_append_expired_trial(
    ch_json_buffer *json,
    const ch_license_decision *decision
) {
    static const char *const secondary[] = {
        "activate_license", "open_license_portal", "support"
    };
    const char *default_message = "Buy or activate a ClambHook license to continue.";
    char message[192];
    const char *selected_message = default_message;
    if (decision->has_trial_ends_at) {
        char date[32];
        if (!ch_format_display_date(decision->trial_ends_at, date)) return 0;
        int count = snprintf(
            message,
            sizeof(message),
            "The one-calendar-month trial ended %s. Buy or activate a USD 49.99 one-time ClambHook license to continue.",
            date
        );
        if (count < 0 || (size_t)count >= sizeof(message)) return 0;
        selected_message = message;
    }
    return ch_append_recovery(
        json,
        "expired_trial",
        "Trial ended",
        selected_message,
        "buy_license",
        secondary,
        sizeof(secondary) / sizeof(secondary[0]),
        ""
    );
}

static int ch_append_expired_updates(
    ch_json_buffer *json,
    const ch_license_decision *decision,
    bool has_published_at,
    ch_timestamp published_at
) {
    static const char *const secondary[] = {
        "open_license_portal", "activate_license", "support"
    };
    char cutoff[32];
    char published[32];
    char message[512];
    char diagnostic[384];
    if (!ch_format_display_date(decision->update_cutoff_date, cutoff) ||
        (has_published_at && !ch_format_display_date(published_at, published))) return 0;
    int count = has_published_at
        ? snprintf(
            message,
            sizeof(message),
            "This update was published %s, after your included update window ended %s. Your installed version keeps working; renew updates for USD 9.99 to install releases after the cutoff, including critical, bug, and security updates.",
            published,
            cutoff
        )
        : snprintf(
            message,
            sizeof(message),
            "Your included update window ended %s. Your installed version keeps working; renew updates for USD 9.99 to install releases after the cutoff, including critical, bug, and security updates.",
            cutoff
        );
    if (count < 0 || (size_t)count >= sizeof(message)) return 0;
    count = snprintf(
        diagnostic,
        sizeof(diagnostic),
        "The ClambHook license includes all updates released through %s. Versions released during that window remain usable. Updates released after that date, including critical, bug, and security updates, require a USD 9.99 update-year renewal.",
        cutoff
    );
    if (count < 0 || (size_t)count >= sizeof(diagnostic)) return 0;
    return ch_append_recovery(
        json,
        "license_expired_for_updates",
        "Update window ended",
        message,
        "renew_updates",
        secondary,
        sizeof(secondary) / sizeof(secondary[0]),
        diagnostic
    );
}

char *ch_license_new_install_id(ch_error *error) {
    ch_error_clear(error);
    uint8_t bytes[16];
    if (RAND_bytes(bytes, (int)sizeof(bytes)) != 1) {
        ch_error_set(error, CH_ERROR_INTERNAL,
                     "generate install identifier entropy");
        return NULL;
    }
    bytes[6] = (uint8_t)((bytes[6] & 0x0fU) | 0x40U);
    bytes[8] = (uint8_t)((bytes[8] & 0x3fU) | 0x80U);
    char *identifier = malloc(37U);
    if (identifier == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate install identifier");
        return NULL;
    }
    (void)snprintf(
        identifier, 37U,
        "%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
        bytes[0], bytes[1], bytes[2], bytes[3], bytes[4], bytes[5], bytes[6], bytes[7],
        bytes[8], bytes[9], bytes[10], bytes[11], bytes[12], bytes[13], bytes[14], bytes[15]
    );
    return identifier;
}

char *ch_license_commercial_terms_json(ch_error *error) {
    ch_error_clear(error);
    return ch_strdup(
        "{\"includedUpdateYears\":1,\"licensePriceUSD\":\"49.99\","
        "\"maxActiveDevices\":3,\"paidUpdatePriceUSD\":\"9.99\",\"trialMonths\":1}"
    );
}

ch_status ch_license_ensure_trial_json(
    const char *snapshot_json,
    int64_t now_unix_millis,
    char **result_json,
    ch_error *error
) {
    if (result_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "result JSON is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *result_json = NULL;
    ch_decoded_snapshot decoded;
    ch_status status = ch_decode_snapshot(snapshot_json, &decoded, error);
    if (status != CH_OK) return status;
    ch_timestamp now = ch_license_now(now_unix_millis);
    if (!decoded.snapshot.has_trial_start_date) {
        decoded.snapshot.has_trial_start_date = true;
        decoded.snapshot.trial_start_date = now;
    }
    decoded.snapshot.cached_at = now;
    ch_json_buffer json;
    ch_json_init(&json);
    if (ch_append_snapshot(&json, &decoded)) *result_json = ch_json_take(&json);
    ch_decoded_snapshot_clear(&decoded);
    if (*result_json == NULL) {
        ch_json_dispose(&json);
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode license snapshot");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

ch_status ch_license_evaluate_json(
    const char *snapshot_json,
    int64_t now_unix_millis,
    char **result_json,
    ch_error *error
) {
    if (result_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "result JSON is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *result_json = NULL;
    ch_decoded_snapshot decoded;
    ch_status status = ch_decode_snapshot(snapshot_json, &decoded, error);
    if (status != CH_OK) return status;
    ch_license_decision decision;
    status = ch_license_evaluate(
        &decoded.snapshot, NULL, 0U, ch_license_now(now_unix_millis), &decision, error
    );
    if (status == CH_OK) {
        ch_json_buffer json;
        ch_json_init(&json);
        if (ch_append_decision(&json, &decision)) *result_json = ch_json_take(&json);
        if (*result_json == NULL) {
            ch_json_dispose(&json);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode license decision");
            status = CH_ERROR_OUT_OF_MEMORY;
        }
        ch_license_decision_clear(&decision);
    }
    ch_decoded_snapshot_clear(&decoded);
    return status;
}

ch_status ch_license_status_json(
    const char *snapshot_json,
    int64_t update_published_at_millis,
    int64_t now_unix_millis,
    char **result_json,
    ch_error *error
) {
    ch_error_clear(error);
    if (result_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "result JSON is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *result_json = NULL;
    ch_decoded_snapshot decoded;
    ch_status status = ch_decode_snapshot(snapshot_json, &decoded, error);
    if (status != CH_OK) return status;
    ch_timestamp now = ch_license_now(now_unix_millis);
    ch_license_decision decision;
    status = ch_license_evaluate(&decoded.snapshot, NULL, 0U, now, &decision, error);
    if (status == CH_OK) {
        bool has_published_at = update_published_at_millis > 0;
        ch_timestamp published_at = has_published_at
            ? ch_license_now(update_published_at_millis)
            : now;
        bool expired_trial = !ch_license_can_use_app(&decision);
        bool expired_updates = decision.has_lifetime_unlock &&
            decision.has_update_cutoff_date && published_at > decision.update_cutoff_date;
        ch_json_buffer json;
        ch_json_init(&json);
        int ok = ch_json_append(&json, "{\"decision\":") &&
            ch_append_decision(&json, &decision) &&
            ch_json_append(&json, ",\"productStates\":") &&
            ch_append_product_states(&json, &decision);
        if (ok && expired_trial) {
            ok = ch_json_append(&json, ",\"expiredTrial\":") &&
                ch_append_expired_trial(&json, &decision);
        }
        if (ok && expired_updates) {
            ok = ch_json_append(&json, ",\"licenseExpiredForUpdates\":") &&
                ch_append_expired_updates(
                    &json, &decision, has_published_at, published_at
                );
        }
        if (ok && ch_json_append(&json, "}")) *result_json = ch_json_take(&json);
        if (*result_json == NULL) {
            ch_json_dispose(&json);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode license status");
            status = CH_ERROR_OUT_OF_MEMORY;
        }
        ch_license_decision_clear(&decision);
    }
    ch_decoded_snapshot_clear(&decoded);
    return status;
}

static ch_status ch_append_device_state(
    ch_json_buffer *json,
    const ch_json_value *value,
    const char *install_id,
    ch_error *error
) {
    if (ch_json_value_type(value) != CH_JSON_OBJECT) {
        ch_error_set(error, CH_ERROR_PARSE, "license: device_state must be an object");
        return CH_ERROR_PARSE;
    }
    const ch_json_value *current_device_value = ch_json_object_get(value, "current_device_id");
    const char *current_device = NULL;
    if (current_device_value != NULL && ch_json_value_type(current_device_value) != CH_JSON_NULL) {
        current_device = ch_json_string_value(current_device_value);
        if (current_device == NULL) {
            ch_error_set(error, CH_ERROR_PARSE, "license: current_device_id must be a string");
            return CH_ERROR_PARSE;
        }
    }
    int maximum = CH_MAX_ACTIVE_DEVICES;
    const ch_json_value *maximum_value = ch_json_object_get(value, "max_active_devices");
    if (maximum_value != NULL && ch_json_value_type(maximum_value) != CH_JSON_NULL) {
        if (ch_json_value_type(maximum_value) != CH_JSON_NUMBER) {
            ch_error_set(error, CH_ERROR_PARSE, "license: max_active_devices must be an integer");
            return CH_ERROR_PARSE;
        }
        double number = ch_json_number_value(maximum_value, 0.0);
        if (!isfinite(number) || number < (double)INT_MIN || number > (double)INT_MAX ||
            (double)(int)number != number) {
            ch_error_set(error, CH_ERROR_PARSE, "license: max_active_devices must be an integer");
            return CH_ERROR_PARSE;
        }
        maximum = (int)number;
        if (maximum == 0) maximum = CH_MAX_ACTIVE_DEVICES;
        if (maximum < 0) maximum = 0;
        if (maximum > CH_MAX_ACTIVE_DEVICES) maximum = CH_MAX_ACTIVE_DEVICES;
    }
    const ch_json_value *devices = ch_json_object_get(value, "devices");
    if (devices != NULL && ch_json_value_type(devices) != CH_JSON_NULL &&
        ch_json_value_type(devices) != CH_JSON_ARRAY) {
        ch_error_set(error, CH_ERROR_PARSE, "license: devices must be an array or null");
        return CH_ERROR_PARSE;
    }
    const ch_json_value *provider_value = ch_json_object_get(value, "payment_provider");
    const char *provider = NULL;
    if (provider_value != NULL && ch_json_value_type(provider_value) != CH_JSON_NULL) {
        provider = ch_json_string_value(provider_value);
        if (provider == NULL) {
            ch_error_set(error, CH_ERROR_PARSE, "license: payment_provider must be a string");
            return CH_ERROR_PARSE;
        }
    }

    int ok = ch_json_append(json, "{\"current_install_id\":") &&
        ch_json_append_string(json, install_id == NULL ? "" : install_id);
    if (ok && current_device != NULL && current_device[0] != '\0') {
        ok = ch_json_append(json, ",\"current_device_id\":") &&
            ch_json_append_string(json, current_device);
    }
    ok = ok && ch_json_append_format(json, ",\"max_active_devices\":%d,\"devices\":", maximum) &&
        (devices == NULL ? ch_json_append(json, "null") : ch_json_append_value(json, devices));
    if (ok && provider != NULL) {
        const char *normalized = provider;
        if (strcasecmp(provider, "creem") == 0) normalized = "creem";
        else if (strcasecmp(provider, "nowpayments") == 0) normalized = "nowpayments";
        ok = ch_json_append(json, ",\"payment_provider\":") &&
            ch_json_append_string(json, normalized);
    }
    if (!ok || !ch_json_append(json, "}")) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode license device state");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    return CH_OK;
}

ch_status ch_license_apply_server_response_json(
    const char *server_response_json,
    const char *install_id,
    int64_t now_unix_millis,
    char **result_json,
    ch_error *error
) {
    ch_error_clear(error);
    if (server_response_json == NULL || result_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "server response and result JSON are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *result_json = NULL;
    ch_json_value *root = ch_json_parse(server_response_json, strlen(server_response_json), error);
    if (root == NULL) return error == NULL ? CH_ERROR_PARSE : error->code;
    if (ch_json_value_type(root) != CH_JSON_OBJECT) {
        ch_json_value_destroy(root);
        ch_error_set(error, CH_ERROR_PARSE, "license: server response must be an object");
        return CH_ERROR_PARSE;
    }
    const ch_json_value *grant = ch_json_object_get(root, "grant");
    const ch_json_value *snapshot_value = ch_json_object_get(root, "snapshot");
    const ch_json_value *device_state = ch_json_object_get(root, "device_state");
    if (ch_json_value_type(grant) != CH_JSON_OBJECT) {
        ch_json_value_destroy(root);
        ch_error_set(error, CH_ERROR_PARSE, "license: server grant must be an object");
        return CH_ERROR_PARSE;
    }

    ch_timestamp now = ch_license_now(now_unix_millis);
    ch_decoded_snapshot decoded;
    ch_status status = ch_decode_grant_snapshot(snapshot_value, &decoded, now, error);
    ch_license_decision decision;
    memset(&decision, 0, sizeof(decision));
    if (status == CH_OK) {
        status = ch_license_evaluate(&decoded.snapshot, NULL, 0U, now, &decision, error);
    }
    if (status == CH_OK) {
        ch_json_buffer json;
        ch_json_init(&json);
        int ok = ch_json_append(&json, "{\"grant\":") &&
            ch_json_append_value(&json, grant) &&
            ch_json_append(&json, ",\"snapshot\":") &&
            ch_append_snapshot(&json, &decoded) &&
            ch_json_append(&json, ",\"deviceState\":");
        if (ok) status = ch_append_device_state(&json, device_state, install_id, error);
        if (status == CH_OK) {
            ok = ch_json_append(&json, ",\"decision\":") &&
                ch_append_decision(&json, &decision) && ch_json_append(&json, "}");
            if (ok) *result_json = ch_json_take(&json);
            if (*result_json == NULL) {
                ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode applied license response");
                status = CH_ERROR_OUT_OF_MEMORY;
            }
        }
        ch_json_dispose(&json);
    }
    ch_license_decision_clear(&decision);
    ch_decoded_snapshot_clear(&decoded);
    ch_json_value_destroy(root);
    return status;
}

ch_status ch_license_update_allowed_json(
    const char *snapshot_json,
    int64_t published_at_millis,
    int64_t now_unix_millis,
    bool *allowed,
    ch_error *error
) {
    if (allowed == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "allowed output is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *allowed = false;
    ch_decoded_snapshot decoded;
    ch_status status = ch_decode_snapshot(snapshot_json, &decoded, error);
    if (status != CH_OK) return status;
    ch_license_decision decision;
    ch_timestamp now = ch_license_now(now_unix_millis);
    status = ch_license_evaluate(&decoded.snapshot, NULL, 0U, now, &decision, error);
    if (status == CH_OK) {
        *allowed = ch_license_can_install_update(
            &decision,
            published_at_millis > 0,
            ch_license_now(published_at_millis),
            now
        );
        ch_license_decision_clear(&decision);
    }
    ch_decoded_snapshot_clear(&decoded);
    return status;
}

ch_status ch_license_mark_verification_failure_json(
    const char *snapshot_json,
    int64_t now_unix_millis,
    char **result_json,
    ch_error *error
) {
    if (result_json == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "result JSON is required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    *result_json = NULL;
    ch_decoded_snapshot decoded;
    ch_status status = ch_decode_snapshot(snapshot_json, &decoded, error);
    if (status != CH_OK) return status;
    ch_timestamp now = ch_license_now(now_unix_millis);
    if (!decoded.snapshot.has_last_verification_failed_at ||
        (decoded.snapshot.has_last_verified_at &&
         decoded.snapshot.last_verification_failed_at < decoded.snapshot.last_verified_at)) {
        decoded.snapshot.has_last_verification_failed_at = true;
        decoded.snapshot.last_verification_failed_at = now;
    }
    decoded.snapshot.cached_at = now;
    ch_license_decision decision;
    status = ch_license_evaluate(&decoded.snapshot, NULL, 0U, now, &decision, error);
    if (status == CH_OK) {
        ch_json_buffer json;
        ch_json_init(&json);
        if (ch_json_append(&json, "{\"snapshot\":") && ch_append_snapshot(&json, &decoded) &&
            ch_json_append(&json, ",\"decision\":") && ch_append_decision(&json, &decision) &&
            ch_json_append(&json, "}")) {
            *result_json = ch_json_take(&json);
        }
        if (*result_json == NULL) {
            ch_json_dispose(&json);
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "encode verification failure");
            status = CH_ERROR_OUT_OF_MEMORY;
        }
        ch_license_decision_clear(&decision);
    }
    ch_decoded_snapshot_clear(&decoded);
    return status;
}
