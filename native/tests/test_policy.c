#include "test.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "clambhook/config.h"
#include "policy.h"

typedef struct policy_probe_fixture {
    int slow_latency_ms;
} policy_probe_fixture;

static ch_status policy_test_probe(
    const ch_config_table *chain, const char *test_url,
    unsigned int timeout_milliseconds, ch_policy_probe_result *out_result,
    void *context, ch_error *error) {
    (void)test_url;
    (void)timeout_milliseconds;
    (void)error;
    policy_probe_fixture *fixture = context;
    char *name = NULL;
    ch_error ignored;
    if (ch_config_table_get_string(chain, "name", &name, &ignored) != CH_OK) {
        return CH_ERROR_PARSE;
    }
    memset(out_result, 0, sizeof(*out_result));
    out_result->last_test_ts_ns = INT64_C(123456789);
    out_result->status_code = strcmp(name, "bad") == 0 ? 503 : 204;
    out_result->healthy = strcmp(name, "bad") != 0 &&
        strcmp(name, "tcp-only") != 0;
    if (strcmp(name, "fast") == 0) {
        out_result->latency_ns = INT64_C(100000000);
    } else if (strcmp(name, "slow") == 0) {
        out_result->latency_ns = (int64_t)fixture->slow_latency_ms *
            INT64_C(1000000);
    } else {
        out_result->latency_ns = INT64_C(400000000);
    }
    if (!out_result->healthy) {
        (void)snprintf(out_result->error, sizeof(out_result->error),
                       "fixture unhealthy");
    }
    free(name);
    return CH_OK;
}

void ch_test_policy(void) {
    const char *document =
        "[[profile]]\n"
        "name = \"default\"\n"
        "[[profile.chain]]\n"
        "name = \"fast\"\n"
        "[[profile.chain.server]]\n"
        "protocol = \"direct\"\n"
        "[[profile.chain]]\n"
        "name = \"slow\"\n"
        "[[profile.chain.server]]\n"
        "protocol = \"direct\"\n"
        "[[profile.chain]]\n"
        "name = \"bad\"\n"
        "[[profile.chain.server]]\n"
        "protocol = \"direct\"\n"
        "[[profile.chain]]\n"
        "name = \"tcp-only\"\n"
        "[[profile.chain.server]]\n"
        "protocol = \"wireguard\"\n"
        "[[profile.policy_group]]\n"
        "name = \"latency\"\n"
        "type = \"url-test\"\n"
        "chains = [\"slow\", \"fast\", \"bad\", \"tcp-only\"]\n"
        "interval = \"1m\"\n"
        "timeout = \"2s\"\n"
        "[[profile.policy_group]]\n"
        "name = \"fallback\"\n"
        "type = \"fallback\"\n"
        "chains = [\"bad\", \"slow\", \"fast\"]\n"
        "[[profile.policy_group]]\n"
        "name = \"balanced\"\n"
        "type = \"load-balance\"\n"
        "chains = [\"fast\", \"slow\", \"bad\"]\n"
        "[[profile.policy_group]]\n"
        "name = \"smart\"\n"
        "type = \"smart\"\n"
        "chains = [\"fast\", \"slow\"]\n"
        "selected = \"slow\"\n"
        "[[profile.policy_group]]\n"
        "name = \"manual\"\n"
        "type = \"select\"\n"
        "chains = [\"tcp-only\", \"fast\"]\n"
        "selected = \"tcp-only\"\n";
    ch_config *config = NULL;
    ch_error error;
    CH_TEST_ASSERT(ch_config_parse(document, NULL, &config, &error) == CH_OK);
    policy_probe_fixture fixture = {.slow_latency_ms = 120};
    ch_policy_options options = {
        .probe = policy_test_probe,
        .probe_context = &fixture
    };
    ch_policy_manager *manager = ch_policy_manager_create(
        config, "default", &options, &error);
    CH_TEST_ASSERT(manager != NULL);
    CH_TEST_ASSERT(ch_policy_manager_refresh(manager, "", &error) == CH_OK);

    char *selected = NULL;
    CH_TEST_ASSERT(ch_policy_manager_select(
        manager, "latency", "tcp", "example.com:443", "127.0.0.1:1",
        &selected, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("fast", selected);
    free(selected);

    CH_TEST_ASSERT(ch_policy_manager_select(
        manager, "fallback", "tcp", "example.com:443", "127.0.0.1:2",
        &selected, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("slow", selected);
    free(selected);

    CH_TEST_ASSERT(ch_policy_manager_select(
        manager, "smart", "tcp", "example.com:443", "127.0.0.1:3",
        &selected, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("slow", selected);
    free(selected);
    fixture.slow_latency_ms = 300;
    CH_TEST_ASSERT(ch_policy_manager_refresh(manager, "smart", &error) == CH_OK);
    CH_TEST_ASSERT(ch_policy_manager_select(
        manager, "smart", "tcp", "example.com:443", "127.0.0.1:3",
        &selected, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("fast", selected);
    free(selected);

    char *first_hash = NULL;
    char *second_hash = NULL;
    CH_TEST_ASSERT(ch_policy_manager_select(
        manager, "balanced", "tcp", "example.com:443", "127.0.0.1:4",
        &first_hash, &error) == CH_OK);
    CH_TEST_ASSERT(ch_policy_manager_select(
        manager, "balanced", "tcp", "example.com:443", "127.0.0.1:4",
        &second_hash, &error) == CH_OK);
    CH_TEST_ASSERT_STRING(first_hash, second_hash);
    free(first_hash);
    free(second_hash);

    CH_TEST_ASSERT(ch_policy_manager_select(
        manager, "manual", "udp", "1.1.1.1:53", "198.18.0.2:5",
        &selected, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("tcp-only", selected);
    free(selected);

    char *snapshot = ch_policy_manager_snapshot_json(
        manager, "default", &error);
    CH_TEST_ASSERT(snapshot != NULL);
    CH_TEST_ASSERT(strstr(snapshot, "\"selection_reason\":\"sticky_healthy\"") !=
                   NULL);
    CH_TEST_ASSERT(strstr(snapshot, "\"interval\":\"1m\"") != NULL);
    CH_TEST_ASSERT(strstr(snapshot, "\"timeout\":\"2s\"") != NULL);
    CH_TEST_ASSERT(strstr(snapshot, "\"status_code\":503") != NULL);
    CH_TEST_ASSERT(strstr(snapshot, "\"udp_capable\":true") != NULL);
    free(snapshot);

    ch_policy_manager_destroy(manager);
    ch_config_free(config);
}
