#include "clambhook/license.h"

#include <ctype.h>
#include <limits.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "internal.h"

#define CH_NANOSECONDS_PER_SECOND INT64_C(1000000000)
#define CH_NANOSECONDS_PER_DAY (INT64_C(86400) * CH_NANOSECONDS_PER_SECOND)

typedef struct ch_civil_date {
    int year;
    unsigned month;
    unsigned day;
} ch_civil_date;

static const ch_license_feature ch_default_features[] = {
    {CH_FEATURE_TUNNEL_ROUTING, "Tunnel Routing", 0},
    {CH_FEATURE_PROFILE_MANAGEMENT, "Profile Management", 0},
    {CH_FEATURE_ROUTING_RULES, "Routing Rules", 0},
    {CH_FEATURE_ACTIVITY_INSPECTION, "Activity Inspection", 0},
    {CH_FEATURE_HTTP_METADATA, "HTTP Metadata", 0},
    {CH_FEATURE_WIDGETS, "Widgets", 0}
};

static int64_t ch_floor_div(int64_t numerator, int64_t denominator) {
    int64_t quotient = numerator / denominator;
    int64_t remainder = numerator % denominator;
    if (remainder != 0 && ((remainder < 0) != (denominator < 0))) {
        --quotient;
    }
    return quotient;
}

/* Howard Hinnant's public-domain civil calendar conversion algorithms. */
static int64_t ch_days_from_civil(int year, unsigned month, unsigned day) {
    year -= month <= 2U;
    int era = (year >= 0 ? year : year - 399) / 400;
    unsigned year_of_era = (unsigned)(year - era * 400);
    unsigned adjusted_month = month > 2U ? month - 3U : month + 9U;
    unsigned day_of_year = (153U * adjusted_month + 2U) / 5U + day - 1U;
    unsigned day_of_era = year_of_era * 365U + year_of_era / 4U - year_of_era / 100U + day_of_year;
    return (int64_t)era * 146097 + (int64_t)day_of_era - 719468;
}

static ch_civil_date ch_civil_from_days(int64_t days) {
    days += 719468;
    int64_t era = (days >= 0 ? days : days - 146096) / 146097;
    unsigned day_of_era = (unsigned)(days - era * 146097);
    unsigned year_of_era = (day_of_era - day_of_era / 1460U + day_of_era / 36524U - day_of_era / 146096U) / 365U;
    int year = (int)year_of_era + (int)era * 400;
    unsigned day_of_year = day_of_era - (365U * year_of_era + year_of_era / 4U - year_of_era / 100U);
    unsigned month_prime = (5U * day_of_year + 2U) / 153U;
    unsigned day = day_of_year - (153U * month_prime + 2U) / 5U + 1U;
    int month_value = (int)month_prime + (month_prime < 10U ? 3 : -9);
    unsigned month = (unsigned)month_value;
    year += month <= 2U;
    return (ch_civil_date){year, month, day};
}

static int ch_is_leap_year(int year) {
    return year % 4 == 0 && (year % 100 != 0 || year % 400 == 0);
}

static unsigned ch_days_in_month(int year, unsigned month) {
    static const unsigned days[] = {31U, 28U, 31U, 30U, 31U, 30U, 31U, 31U, 30U, 31U, 30U, 31U};
    if (month == 2U && ch_is_leap_year(year)) {
        return 29U;
    }
    return month >= 1U && month <= 12U ? days[month - 1U] : 0U;
}

ch_timestamp ch_license_utc_date(int year, unsigned month, unsigned day) {
    return ch_days_from_civil(year, month, day) * CH_NANOSECONDS_PER_DAY;
}

static ch_timestamp ch_add_months_clamped(ch_timestamp timestamp, int months) {
    int64_t days = ch_floor_div(timestamp, CH_NANOSECONDS_PER_DAY);
    int64_t nanoseconds_of_day = timestamp - days * CH_NANOSECONDS_PER_DAY;
    ch_civil_date date = ch_civil_from_days(days);
    int total = (int)date.month - 1 + months;
    int year = date.year + total / 12;
    int month_zero = total % 12;
    if (month_zero < 0) {
        month_zero += 12;
        --year;
    }
    unsigned month = (unsigned)month_zero + 1U;
    unsigned last_day = ch_days_in_month(year, month);
    unsigned day = date.day > last_day ? last_day : date.day;
    return ch_license_utc_date(year, month, day) + nanoseconds_of_day;
}

ch_timestamp ch_license_trial_end_date(ch_timestamp start) {
    return ch_add_months_clamped(start, CH_TRIAL_MONTHS);
}

static int ch_decimal_pair(const char *text, unsigned *value) {
    if (!isdigit((unsigned char)text[0]) || !isdigit((unsigned char)text[1])) return 0;
    *value = (unsigned)(text[0] - '0') * 10U + (unsigned)(text[1] - '0');
    return 1;
}

ch_status ch_license_parse_rfc3339(const char *text, ch_timestamp *timestamp, ch_error *error) {
    ch_error_clear(error);
    if (text == NULL || timestamp == NULL) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "RFC3339 timestamp and output are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    size_t length = strlen(text);
    if (length < 20U || text[4] != '-' || text[7] != '-' ||
        (text[10] != 'T' && text[10] != 't') || text[13] != ':' || text[16] != ':') {
        ch_error_set(error, CH_ERROR_PARSE, "invalid RFC3339 timestamp");
        return CH_ERROR_PARSE;
    }
    int year = 0;
    for (size_t index = 0U; index < 4U; ++index) {
        if (!isdigit((unsigned char)text[index])) {
            ch_error_set(error, CH_ERROR_PARSE, "invalid RFC3339 year");
            return CH_ERROR_PARSE;
        }
        year = year * 10 + text[index] - '0';
    }
    unsigned month, day, hour, minute, second;
    if (!ch_decimal_pair(text + 5, &month) || !ch_decimal_pair(text + 8, &day) ||
        !ch_decimal_pair(text + 11, &hour) || !ch_decimal_pair(text + 14, &minute) ||
        !ch_decimal_pair(text + 17, &second) || month < 1U || month > 12U ||
        day < 1U || day > ch_days_in_month(year, month) || hour > 23U || minute > 59U || second > 59U) {
        ch_error_set(error, CH_ERROR_PARSE, "invalid RFC3339 date or time");
        return CH_ERROR_PARSE;
    }
    size_t cursor = 19U;
    int64_t fractional = 0;
    if (cursor < length && text[cursor] == '.') {
        ++cursor;
        size_t digits = 0U;
        while (cursor < length && isdigit((unsigned char)text[cursor])) {
            if (digits < 9U) fractional = fractional * 10 + text[cursor] - '0';
            ++digits;
            ++cursor;
        }
        if (digits == 0U || digits > 9U) {
            ch_error_set(error, CH_ERROR_PARSE, "invalid RFC3339 fractional seconds");
            return CH_ERROR_PARSE;
        }
        for (; digits < 9U; ++digits) fractional *= 10;
    }
    int offset_sign = 0;
    unsigned offset_hour = 0U, offset_minute = 0U;
    if (cursor < length && (text[cursor] == 'Z' || text[cursor] == 'z')) {
        ++cursor;
    } else if (cursor + 6U == length && (text[cursor] == '+' || text[cursor] == '-') &&
               text[cursor + 3U] == ':' && ch_decimal_pair(text + cursor + 1U, &offset_hour) &&
               ch_decimal_pair(text + cursor + 4U, &offset_minute) &&
               offset_hour <= 23U && offset_minute <= 59U) {
        offset_sign = text[cursor] == '+' ? 1 : -1;
        cursor += 6U;
    } else {
        ch_error_set(error, CH_ERROR_PARSE, "RFC3339 timezone is required");
        return CH_ERROR_PARSE;
    }
    if (cursor != length) {
        ch_error_set(error, CH_ERROR_PARSE, "trailing RFC3339 data");
        return CH_ERROR_PARSE;
    }
    int64_t seconds = ch_days_from_civil(year, month, day) * INT64_C(86400) +
        (int64_t)hour * 3600 + (int64_t)minute * 60 + (int64_t)second;
    seconds -= (int64_t)offset_sign * ((int64_t)offset_hour * 3600 + (int64_t)offset_minute * 60);
    if (seconds > INT64_MAX / CH_NANOSECONDS_PER_SECOND ||
        seconds < INT64_MIN / CH_NANOSECONDS_PER_SECOND) {
        ch_error_set(error, CH_ERROR_PARSE, "RFC3339 timestamp is out of range");
        return CH_ERROR_PARSE;
    }
    *timestamp = seconds * CH_NANOSECONDS_PER_SECOND + fractional;
    return CH_OK;
}

char *ch_license_format_rfc3339(ch_timestamp timestamp) {
    int64_t seconds = ch_floor_div(timestamp, CH_NANOSECONDS_PER_SECOND);
    int64_t nanoseconds = timestamp - seconds * CH_NANOSECONDS_PER_SECOND;
    int64_t days = ch_floor_div(seconds, INT64_C(86400));
    int64_t seconds_of_day = seconds - days * INT64_C(86400);
    ch_civil_date date = ch_civil_from_days(days);
    unsigned hour = (unsigned)(seconds_of_day / 3600);
    unsigned minute = (unsigned)((seconds_of_day % 3600) / 60);
    unsigned second = (unsigned)(seconds_of_day % 60);
    char fraction[12] = {0};
    if (nanoseconds > 0) {
        (void)snprintf(fraction, sizeof(fraction), ".%09lld", (long long)nanoseconds);
        size_t end = strlen(fraction);
        while (end > 1U && fraction[end - 1U] == '0') fraction[--end] = '\0';
    }
    int length = snprintf(
        NULL, 0, "%04d-%02u-%02uT%02u:%02u:%02u%sZ",
        date.year, date.month, date.day, hour, minute, second, fraction
    );
    if (length < 0) return NULL;
    char *formatted = malloc((size_t)length + 1U);
    if (formatted != NULL) {
        (void)snprintf(
            formatted, (size_t)length + 1U, "%04d-%02u-%02uT%02u:%02u:%02u%sZ",
            date.year, date.month, date.day, hour, minute, second, fraction
        );
    }
    return formatted;
}

static int ch_is_lifetime_product(const char *product_id) {
    return product_id != NULL && strcmp(product_id, CH_LIFETIME_UNLOCK_PRODUCT_ID) == 0;
}

static int ch_is_paid_update_product(const char *product_id) {
    if (product_id == NULL) return 0;
    size_t base_length = strlen(CH_FEATURE_UPDATE_PRODUCT_ID);
    return strcmp(product_id, CH_FEATURE_UPDATE_PRODUCT_ID) == 0 ||
        (strncmp(product_id, CH_FEATURE_UPDATE_PRODUCT_ID, base_length) == 0 && product_id[base_length] == '.');
}

static int ch_transaction_compare(const void *left, const void *right) {
    const ch_license_transaction *a = left;
    const ch_license_transaction *b = right;
    return a->purchase_date < b->purchase_date ? -1 : a->purchase_date > b->purchase_date ? 1 : 0;
}

static ch_status ch_update_cutoff(
    ch_timestamp purchase,
    const ch_license_snapshot *snapshot,
    ch_timestamp *cutoff,
    ch_error *error
) {
    *cutoff = ch_add_months_clamped(purchase, 12 * CH_INCLUDED_UPDATE_YEARS);
    if (snapshot->transaction_count == 0U) {
        return CH_OK;
    }
    if (snapshot->transaction_count > SIZE_MAX / sizeof(*snapshot->transactions)) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "paid update transaction list is too large");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    ch_license_transaction *updates = malloc(snapshot->transaction_count * sizeof(*updates));
    if (updates == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate paid update transactions");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    size_t update_count = 0U;
    for (size_t index = 0U; index < snapshot->transaction_count; ++index) {
        const ch_license_transaction *transaction = &snapshot->transactions[index];
        if (!transaction->has_revocation_date && ch_is_paid_update_product(transaction->product_id)) {
            updates[update_count++] = *transaction;
        }
    }
    qsort(updates, update_count, sizeof(*updates), ch_transaction_compare);
    for (size_t index = 0U; index < update_count; ++index) {
        ch_timestamp start = updates[index].purchase_date > *cutoff ? updates[index].purchase_date : *cutoff;
        *cutoff = ch_add_months_clamped(start, 12);
    }
    free(updates);
    return CH_OK;
}

static ch_status ch_unlock_feature(ch_license_decision *decision, const char *feature_id, ch_error *error) {
    char *copy = ch_strdup(feature_id == NULL ? "" : feature_id);
    if (copy == NULL) {
        ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "copy unlocked feature identifier");
        return CH_ERROR_OUT_OF_MEMORY;
    }
    decision->unlocked_feature_ids[decision->unlocked_feature_count++] = copy;
    return CH_OK;
}

ch_status ch_license_evaluate(
    const ch_license_snapshot *snapshot,
    const ch_license_feature *features,
    size_t feature_count,
    ch_timestamp now,
    ch_license_decision *decision,
    ch_error *error
) {
    ch_error_clear(error);
    if (snapshot == NULL || decision == NULL ||
        (snapshot->transaction_count > 0U && snapshot->transactions == NULL) ||
        (feature_count > 0U && features == NULL)) {
        ch_error_set(error, CH_ERROR_INVALID_ARGUMENT, "license snapshot and decision are required");
        return CH_ERROR_INVALID_ARGUMENT;
    }
    memset(decision, 0, sizeof(*decision));
    ch_license_feature defaults[sizeof(ch_default_features) / sizeof(ch_default_features[0])];
    if (features == NULL && feature_count == 0U) {
        memcpy(defaults, ch_default_features, sizeof(defaults));
        ch_timestamp release_date = ch_license_utc_date(2026, 6U, 3U);
        for (size_t index = 0U; index < sizeof(defaults) / sizeof(defaults[0]); ++index) {
            defaults[index].release_date = release_date;
        }
        features = defaults;
        feature_count = sizeof(defaults) / sizeof(defaults[0]);
    }
    if (feature_count > 0U) {
        if (feature_count > SIZE_MAX / sizeof(*decision->unlocked_feature_ids)) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "unlocked feature list is too large");
            return CH_ERROR_OUT_OF_MEMORY;
        }
        decision->unlocked_feature_ids = calloc(feature_count, sizeof(*decision->unlocked_feature_ids));
        if (decision->unlocked_feature_ids == NULL) {
            ch_error_set(error, CH_ERROR_OUT_OF_MEMORY, "allocate unlocked feature list");
            return CH_ERROR_OUT_OF_MEMORY;
        }
    }

    bool trial_active = false;
    if (snapshot->has_trial_start_date) {
        decision->has_trial_start_date = true;
        decision->trial_start_date = snapshot->trial_start_date;
        decision->has_trial_ends_at = true;
        decision->trial_ends_at = ch_license_trial_end_date(snapshot->trial_start_date);
        trial_active = now < decision->trial_ends_at;
        int64_t remaining = ch_floor_div(decision->trial_ends_at, CH_NANOSECONDS_PER_DAY) -
            ch_floor_div(now, CH_NANOSECONDS_PER_DAY);
        if (remaining < 0) remaining = 0;
        decision->trial_days_remaining = remaining > INT_MAX ? INT_MAX : (int)remaining;
    }

    bool found_lifetime = false;
    ch_timestamp lifetime_purchase = 0;
    for (size_t index = 0U; index < snapshot->transaction_count; ++index) {
        const ch_license_transaction *transaction = &snapshot->transactions[index];
        if (transaction->has_revocation_date || !ch_is_lifetime_product(transaction->product_id)) continue;
        if (!found_lifetime || transaction->purchase_date < lifetime_purchase) {
            lifetime_purchase = transaction->purchase_date;
            found_lifetime = true;
        }
    }
    decision->has_lifetime_unlock = found_lifetime;
    if (found_lifetime) {
        decision->has_update_cutoff_date = true;
        ch_status status = ch_update_cutoff(lifetime_purchase, snapshot, &decision->update_cutoff_date, error);
        if (status != CH_OK) {
            ch_license_decision_clear(decision);
            return status;
        }
    }

    bool grace_active = false;
    if (snapshot->has_last_verification_failed_at &&
        (!snapshot->has_last_verified_at || snapshot->last_verification_failed_at >= snapshot->last_verified_at)) {
        ch_timestamp grace_end = snapshot->last_verification_failed_at +
            CH_OFFLINE_GRACE_DAYS * CH_NANOSECONDS_PER_DAY;
        if (now < grace_end) {
            grace_active = true;
            decision->has_offline_grace_ends_at = true;
            decision->offline_grace_ends_at = grace_end;
        }
    }
    if (trial_active) decision->reason = CH_LICENSE_REASON_TRIAL;
    else if (found_lifetime && grace_active) decision->reason = CH_LICENSE_REASON_OFFLINE_GRACE;
    else if (found_lifetime) decision->reason = CH_LICENSE_REASON_LIFETIME;
    else decision->reason = CH_LICENSE_REASON_LOCKED;

    if (decision->reason == CH_LICENSE_REASON_TRIAL) {
        for (size_t index = 0U; index < feature_count; ++index) {
            ch_status status = ch_unlock_feature(decision, features[index].id, error);
            if (status != CH_OK) { ch_license_decision_clear(decision); return status; }
        }
    } else if ((decision->reason == CH_LICENSE_REASON_LIFETIME ||
                decision->reason == CH_LICENSE_REASON_OFFLINE_GRACE) &&
               decision->has_update_cutoff_date) {
        for (size_t index = 0U; index < feature_count; ++index) {
            if (features[index].release_date <= decision->update_cutoff_date) {
                ch_status status = ch_unlock_feature(decision, features[index].id, error);
                if (status != CH_OK) { ch_license_decision_clear(decision); return status; }
            }
        }
    }
    return CH_OK;
}

void ch_license_decision_clear(ch_license_decision *decision) {
    if (decision == NULL) return;
    for (size_t index = 0U; index < decision->unlocked_feature_count; ++index) {
        free(decision->unlocked_feature_ids[index]);
    }
    free(decision->unlocked_feature_ids);
    memset(decision, 0, sizeof(*decision));
}

const char *ch_license_reason_name(ch_license_access_reason reason) {
    switch (reason) {
        case CH_LICENSE_REASON_TRIAL: return "trial";
        case CH_LICENSE_REASON_LIFETIME: return "lifetime";
        case CH_LICENSE_REASON_OFFLINE_GRACE: return "offlineGrace";
        case CH_LICENSE_REASON_LOCKED: return "locked";
    }
    return "locked";
}

bool ch_license_can_use_app(const ch_license_decision *decision) {
    return decision != NULL && decision->reason != CH_LICENSE_REASON_LOCKED;
}

bool ch_license_can_use_feature(const ch_license_decision *decision, const char *feature_id) {
    if (!ch_license_can_use_app(decision) || feature_id == NULL) return false;
    for (size_t index = 0U; index < decision->unlocked_feature_count; ++index) {
        if (strcmp(decision->unlocked_feature_ids[index], feature_id) == 0) return true;
    }
    return false;
}

bool ch_license_can_install_update(
    const ch_license_decision *decision,
    bool has_published_at,
    ch_timestamp published_at,
    ch_timestamp now
) {
    if (decision == NULL) return false;
    if (decision->reason == CH_LICENSE_REASON_TRIAL) return true;
    if (decision->reason == CH_LICENSE_REASON_LOCKED || !decision->has_update_cutoff_date) return false;
    return (has_published_at ? published_at : now) <= decision->update_cutoff_date;
}
