// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <stdlib.h>

#include "clambhook/license.h"
#include "clambhook/license_json.h"

static ch_license_transaction lifetime(ch_timestamp purchase) {
    return (ch_license_transaction){
        .product_id = CH_LIFETIME_UNLOCK_PRODUCT_ID,
        .purchase_date = purchase
    };
}

void ch_test_license(void) {
    ch_error error;
    ch_license_decision decision;
    ch_license_snapshot snapshot = {
        .has_trial_start_date = true,
        .trial_start_date = ch_license_utc_date(2026, 1U, 31U)
    };
    ch_timestamp parsed = 0;
    CH_TEST_ASSERT(ch_license_parse_rfc3339("2026-06-03T12:34:56.123456789+08:00", &parsed, &error) == CH_OK);
    char *formatted = ch_license_format_rfc3339(parsed);
    CH_TEST_ASSERT_STRING("2026-06-03T04:34:56.123456789Z", formatted);
    free(formatted);
    CH_TEST_ASSERT(ch_license_evaluate(
        &snapshot, NULL, 0U, ch_license_utc_date(2026, 2U, 27U), &decision, &error
    ) == CH_OK);
    CH_TEST_ASSERT(decision.reason == CH_LICENSE_REASON_TRIAL);
    CH_TEST_ASSERT(decision.trial_ends_at == ch_license_utc_date(2026, 2U, 28U));
    CH_TEST_ASSERT(ch_license_can_use_feature(&decision, CH_FEATURE_TUNNEL_ROUTING));
    ch_license_decision_clear(&decision);

    CH_TEST_ASSERT(ch_license_evaluate(
        &snapshot, NULL, 0U, ch_license_utc_date(2026, 2U, 28U), &decision, &error
    ) == CH_OK);
    CH_TEST_ASSERT(decision.reason == CH_LICENSE_REASON_LOCKED);
    CH_TEST_ASSERT(!ch_license_can_use_app(&decision));
    ch_license_decision_clear(&decision);

    CH_TEST_ASSERT(
        ch_license_trial_end_date(ch_license_utc_date(2024, 1U, 31U)) ==
        ch_license_utc_date(2024, 2U, 29U)
    );
    CH_TEST_ASSERT(
        ch_license_trial_end_date(ch_license_utc_date(2025, 12U, 31U)) ==
        ch_license_utc_date(2026, 1U, 31U)
    );

    ch_license_transaction transactions[] = {
        lifetime(ch_license_utc_date(2026, 6U, 3U)),
        {
            .product_id = CH_FEATURE_UPDATE_PRODUCT_ID,
            .purchase_date = ch_license_utc_date(2027, 8U, 1U)
        }
    };
    snapshot = (ch_license_snapshot){
        .transactions = transactions,
        .transaction_count = 2U,
        .has_last_verified_at = true,
        .last_verified_at = ch_license_utc_date(2027, 8U, 1U)
    };
    ch_license_feature feature = {
        .id = CH_FEATURE_WIDGETS,
        .display_name = "Future Widgets",
        .release_date = ch_license_utc_date(2028, 7U, 31U)
    };
    CH_TEST_ASSERT(ch_license_evaluate(
        &snapshot, &feature, 1U, ch_license_utc_date(2027, 8U, 2U), &decision, &error
    ) == CH_OK);
    CH_TEST_ASSERT(decision.reason == CH_LICENSE_REASON_LIFETIME);
    CH_TEST_ASSERT(decision.update_cutoff_date == ch_license_utc_date(2028, 8U, 1U));
    CH_TEST_ASSERT(ch_license_can_use_feature(&decision, CH_FEATURE_WIDGETS));
    CH_TEST_ASSERT(ch_license_can_install_update(
        &decision, true, ch_license_utc_date(2028, 8U, 1U), 0
    ));
    CH_TEST_ASSERT(!ch_license_can_install_update(
        &decision, true, ch_license_utc_date(2028, 8U, 2U), 0
    ));
    ch_license_decision_clear(&decision);

    ch_license_transaction grace_transaction = lifetime(ch_license_utc_date(2026, 6U, 3U));
    ch_license_feature grace_features[] = {
        {CH_FEATURE_WIDGETS, "Included", 0},
        {CH_FEATURE_ACTIVITY_INSPECTION, "Later", 0}
    };
    grace_features[0].release_date = ch_license_utc_date(2027, 6U, 3U);
    grace_features[1].release_date = ch_license_utc_date(2027, 6U, 4U);
    snapshot = (ch_license_snapshot){
        .transactions = &grace_transaction,
        .transaction_count = 1U,
        .has_last_verified_at = true,
        .last_verified_at = ch_license_utc_date(2026, 7U, 1U),
        .has_last_verification_failed_at = true,
        .last_verification_failed_at = ch_license_utc_date(2026, 7U, 2U)
    };
    CH_TEST_ASSERT(ch_license_evaluate(
        &snapshot, grace_features, 2U, ch_license_utc_date(2026, 7U, 5U), &decision, &error
    ) == CH_OK);
    CH_TEST_ASSERT(decision.reason == CH_LICENSE_REASON_OFFLINE_GRACE);
    CH_TEST_ASSERT(decision.offline_grace_ends_at == ch_license_utc_date(2026, 7U, 9U));
    CH_TEST_ASSERT(ch_license_can_use_feature(&decision, CH_FEATURE_WIDGETS));
    CH_TEST_ASSERT(!ch_license_can_use_feature(&decision, CH_FEATURE_ACTIVITY_INSPECTION));
    ch_license_decision_clear(&decision);

    ch_license_transaction revoked = lifetime(ch_license_utc_date(2026, 6U, 3U));
    revoked.has_revocation_date = true;
    revoked.revocation_date = ch_license_utc_date(2026, 7U, 1U);
    snapshot = (ch_license_snapshot){.transactions = &revoked, .transaction_count = 1U};
    CH_TEST_ASSERT(ch_license_evaluate(
        &snapshot, NULL, 0U, ch_license_utc_date(2026, 7U, 2U), &decision, &error
    ) == CH_OK);
    CH_TEST_ASSERT(decision.reason == CH_LICENSE_REASON_LOCKED && !decision.has_lifetime_unlock);
    ch_license_decision_clear(&decision);

    CH_TEST_ASSERT_STRING("49.99", CH_LICENSE_PRICE_USD);
    CH_TEST_ASSERT_STRING("9.99", CH_PAID_UPDATE_PRICE_USD);
    CH_TEST_ASSERT(CH_MAX_ACTIVE_DEVICES == 3);

    char *snapshot_json = NULL;
    CH_TEST_ASSERT(ch_license_ensure_trial_json(
        "", 1780444800000LL, &snapshot_json, &error
    ) == CH_OK);
    CH_TEST_ASSERT(strstr(snapshot_json, "\"trialStartDate\":\"2026-06-03T00:00:00Z\"") != NULL);
    char *decision_json = NULL;
    CH_TEST_ASSERT(ch_license_evaluate_json(
        snapshot_json, 1780444800000LL, &decision_json, &error
    ) == CH_OK);
    CH_TEST_ASSERT(strstr(decision_json, "\"reason\":\"trial\"") != NULL);
    free(decision_json);
    bool allowed = false;
    CH_TEST_ASSERT(ch_license_update_allowed_json(
        snapshot_json, 0, 1780444800000LL, &allowed, &error
    ) == CH_OK && allowed);
    free(snapshot_json);

    char *install_id = ch_license_new_install_id(&error);
    CH_TEST_ASSERT(install_id != NULL && strlen(install_id) == 36U);
    CH_TEST_ASSERT(install_id[14] == '4' && (install_id[19] == '8' || install_id[19] == '9' ||
        install_id[19] == 'a' || install_id[19] == 'b'));
    free(install_id);

    static const char server_response[] =
        "{\"grant\":{\"version\":1,\"issued_at\":\"2026-06-03T00:00:00Z\","
        "\"expires_at\":\"2027-06-03T00:00:00Z\",\"reason\":\"lifetime\","
        "\"has_lifetime_unlock\":true,\"transactions\":[{\"productID\":"
        "\"org.jpfchang.clambhook.unlock.lifetime\",\"purchaseDate\":"
        "\"2026-06-03T00:00:00Z\"}],\"signature\":\"sig\"},\"snapshot\":{"
        "\"reason\":\"lifetime\",\"has_lifetime_unlock\":true,\"transactions\":[{"
        "\"productID\":\"org.jpfchang.clambhook.unlock.lifetime\",\"purchaseDate\":"
        "\"2026-06-03T00:00:00Z\"}]},\"device_state\":{\"current_device_id\":"
        "\"device-1\",\"max_active_devices\":12,\"devices\":[],"
        "\"payment_provider\":\"CREEM\"}}";
    char *applied = NULL;
    CH_TEST_ASSERT(ch_license_apply_server_response_json(
        server_response, "install-1", 1781049600000LL, &applied, &error
    ) == CH_OK);
    CH_TEST_ASSERT(strstr(applied, "\"current_install_id\":\"install-1\"") != NULL);
    CH_TEST_ASSERT(strstr(applied, "\"max_active_devices\":3") != NULL);
    CH_TEST_ASSERT(strstr(applied, "\"payment_provider\":\"creem\"") != NULL);
    CH_TEST_ASSERT(strstr(applied, "\"lastVerifiedAt\":\"2026-06-10T00:00:00Z\"") != NULL);
    CH_TEST_ASSERT(strstr(applied, "\"reason\":\"lifetime\"") != NULL);
    free(applied);
}
