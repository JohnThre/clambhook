// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

#include "clambhook/config.h"
#include "clambhook/json.h"
#include "clambhook/traffic.h"

static const ch_json_value *traffic_test_member(const ch_json_value *object,
                                                const char *name) {
    const ch_json_value *value = ch_json_object_get(object, name);
    return value;
}

typedef struct traffic_test_events {
    unsigned int count;
    uint64_t shard_id;
    uint64_t lamport;
    char type[48];
    char data[512];
} traffic_test_events;

static void traffic_test_event_writer(uint64_t shard_id, uint64_t lamport,
                                      const char *event_type,
                                      const char *data_json,
                                      void *context) {
    traffic_test_events *events = context;
    ++events->count;
    events->shard_id = shard_id;
    events->lamport = lamport;
    (void)snprintf(events->type, sizeof(events->type), "%s", event_type);
    (void)snprintf(events->data, sizeof(events->data), "%s", data_json);
}

void ch_test_traffic(void) {
    ch_error error;
    ch_traffic_store *store = ch_traffic_store_create(4U, &error);
    CH_TEST_ASSERT(store != NULL);
    traffic_test_events events = {0};
    ch_traffic_store_set_event_writer(store, traffic_test_event_writer,
                                      &events);
    ch_traffic_open_info direct = {
        .profile = "Work",
        .listener_protocol = "tun",
        .client_address = "198.18.0.2:41000",
        .rule_name = "Local",
        .rule_action = "direct",
        .target = "203.0.113.4:443",
        .target_host = "api.example",
        .target_port = "443",
        .network = "tcp",
        .source = "198.18.0.2:41000"
    };
    uint64_t direct_id = ch_traffic_open(store, &direct, &error);
    CH_TEST_ASSERT(direct_id != 0U);
    CH_TEST_ASSERT(events.count == 2U);
    CH_TEST_ASSERT(events.shard_id == direct_id && events.lamport == 2U);
    CH_TEST_ASSERT_STRING("rule.direct", events.type);
    CH_TEST_ASSERT(strstr(events.data, "\"conn_id\":\"native-1\"") !=
                   NULL);
    ch_traffic_bytes(store, direct_id, 12U, 34U);
    CH_TEST_ASSERT(events.count == 3U);
    CH_TEST_ASSERT_STRING("connection.bytes", events.type);

    ch_traffic_open_info blocked = direct;
    blocked.rule_name = "Deny ads";
    blocked.rule_action = "block";
    blocked.target = "203.0.113.5:80";
    blocked.target_host = "ads.example";
    blocked.target_port = "80";
    blocked.network = "udp";
    uint64_t blocked_id = ch_traffic_open(store, &blocked, &error);
    CH_TEST_ASSERT(blocked_id != 0U);
    ch_traffic_close(store, blocked_id, "blocked by rule");
    CH_TEST_ASSERT(events.count == 6U);
    CH_TEST_ASSERT(events.shard_id == blocked_id && events.lamport == 3U);
    CH_TEST_ASSERT_STRING("connection.closed", events.type);

    char *json = ch_traffic_snapshot_json(
        store, NULL, "Work", "{\"action\":\"block\",\"limit\":10}",
        "[]", &error);
    CH_TEST_ASSERT(json != NULL);
    ch_json_value *root = ch_json_parse(json, strlen(json), &error);
    CH_TEST_ASSERT(root != NULL);
    CH_TEST_ASSERT(ch_json_number_value(traffic_test_member(root, "total"),
                                        -1.0) == 1.0);
    const ch_json_value *connections = traffic_test_member(root,
                                                            "connections");
    CH_TEST_ASSERT(ch_json_array_size(connections) == 1U);
    const ch_json_value *connection = ch_json_array_get(connections, 0U);
    CH_TEST_ASSERT_STRING("ads.example", ch_json_string_value(
        traffic_test_member(connection, "target_host")));
    CH_TEST_ASSERT_STRING("closed", ch_json_string_value(
        traffic_test_member(connection, "state")));
    const ch_json_value *blocks = traffic_test_member(root,
                                                       "block_decisions");
    CH_TEST_ASSERT(ch_json_array_size(blocks) == 1U);
    ch_json_value_destroy(root);
    free(json);

    json = ch_traffic_decisions_json(store, "{\"limit\":1}", &error);
    CH_TEST_ASSERT(json != NULL);
    root = ch_json_parse(json, strlen(json), &error);
    CH_TEST_ASSERT(root != NULL);
    const ch_json_value *decisions = traffic_test_member(root, "decisions");
    CH_TEST_ASSERT(ch_json_array_size(decisions) == 1U);
    CH_TEST_ASSERT_STRING("ads.example", ch_json_string_value(
        traffic_test_member(ch_json_array_get(decisions, 0U),
                            "target_host")));
    ch_json_value_destroy(root);
    free(json);
    CH_TEST_ASSERT(ch_traffic_decisions_json(
        store, "{\"limit\":-1}", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);

    ch_traffic_connection copy;
    CH_TEST_ASSERT(ch_traffic_connection_copy(
        store, "native-1", &copy, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("api.example", copy.target_host);
    CH_TEST_ASSERT_STRING("direct", copy.rule_action);
    ch_traffic_connection_clear(&copy);

    static const char config_toml[] =
        "active = \"Work\"\n"
        "[[profile]]\nname = \"Work\"\n"
        "[profile.listen.tun]\nenabled = true\nchain = \"main\"\n"
        "addresses = [\"198.18.0.1/30\"]\nroutes = [\"0.0.0.0/0\"]\n"
        "[[profile.chain]]\nname = \"main\"\n"
        "[[profile.chain.server]]\nname = \"direct\"\nprotocol = \"direct\"\n"
        "[[profile.rule]]\nname = \"allow-api-example\"\naction = \"direct\"\n"
        "domains = [\"old.example\"]\n";
    ch_config *config = NULL;
    CH_TEST_ASSERT(ch_config_parse(config_toml, NULL, &config, &error) ==
                   CH_OK);
    char *rule = ch_traffic_rule_request_json(
        store, config, "Work",
        "{\"conn_id\":\"native-1\",\"profile\":\"Work\","
        "\"name\":\"allow-api-example\",\"action\":\"direct\","
        "\"scope\":\"auto\"}", &error);
    CH_TEST_ASSERT(rule != NULL);
    root = ch_json_parse(rule, strlen(rule), &error);
    CH_TEST_ASSERT(root != NULL);
    const ch_json_value *rule_object = traffic_test_member(root, "rule");
    CH_TEST_ASSERT_STRING("allow-api-example-2", ch_json_string_value(
        traffic_test_member(rule_object, "name")));
    CH_TEST_ASSERT_STRING("direct", ch_json_string_value(
        traffic_test_member(rule_object, "action")));
    const ch_json_value *domains = traffic_test_member(rule_object,
                                                        "domains");
    CH_TEST_ASSERT(ch_json_array_size(domains) == 1U);
    CH_TEST_ASSERT_STRING("api.example", ch_json_string_value(
        ch_json_array_get(domains, 0U)));
    ch_json_value_destroy(root);
    free(rule);

    json = ch_traffic_snapshot_json(
        store, config, "Work", "{}",
        "[{\"id\":\"temporary-1\",\"created_ts_ns\":1787793200754630700,"
        "\"expires_ts_ns\":1787793260754630700}]", &error);
    CH_TEST_ASSERT(json != NULL);
    CH_TEST_ASSERT(strstr(json,
        "\"created_ts_ns\":1787793200754630700") != NULL);
    CH_TEST_ASSERT(strstr(json, "1.7877932007546307e+18") == NULL);
    root = ch_json_parse(json, strlen(json), &error);
    CH_TEST_ASSERT(root != NULL);
    const ch_json_value *cleanup = traffic_test_member(
        root, "cleanup_suggestions");
    CH_TEST_ASSERT(ch_json_array_size(cleanup) == 1U);
    CH_TEST_ASSERT_STRING("unused_in_history", ch_json_string_value(
        traffic_test_member(ch_json_array_get(cleanup, 0U), "kind")));
    ch_json_value_destroy(root);
    free(json);
    char *cleanup_request = ch_traffic_cleanup_request_json(
        store, config, "Work",
        "{\"profile\":\"Work\",\"kind\":\"unused_in_history\","
        "\"rule_name\":\"allow-api-example\","
        "\"target_rule_name\":\"allow-api-example\","
        "\"operation\":\"delete_rule\"}", &error);
    CH_TEST_ASSERT(cleanup_request != NULL);
    root = ch_json_parse(cleanup_request, strlen(cleanup_request), &error);
    CH_TEST_ASSERT(root != NULL);
    CH_TEST_ASSERT(ch_json_array_size(traffic_test_member(root, "rules")) ==
                   0U);
    ch_json_value_destroy(root);
    free(cleanup_request);
    ch_config_free(config);

    ch_traffic_close_all(store, "test shutdown");
    json = ch_traffic_snapshot_json(store, NULL, "Work",
                                    "{\"state\":\"active\"}", "[]",
                                    &error);
    CH_TEST_ASSERT(json != NULL);
    root = ch_json_parse(json, strlen(json), &error);
    CH_TEST_ASSERT(root != NULL);
    CH_TEST_ASSERT(ch_json_number_value(traffic_test_member(root, "total"),
                                        -1.0) == 0.0);
    ch_json_value_destroy(root);
    free(json);
    ch_traffic_store_destroy(store);

    char history_path[160];
    (void)snprintf(history_path, sizeof(history_path),
                   "/tmp/clambhook-native-traffic-%ld.json", (long)getpid());
    (void)unlink(history_path);
    char persistence_toml[1024];
    (void)snprintf(
        persistence_toml, sizeof(persistence_toml),
        "active = \"Work\"\n"
        "[traffic]\nenabled = true\nhistory_limit = 2\n"
        "history_max_age = \"168h\"\nhistory_path = \"%s\"\n"
        "[[profile]]\nname = \"Work\"\n",
        history_path);
    config = NULL;
    CH_TEST_ASSERT(ch_config_parse(persistence_toml, NULL, &config, &error) ==
                   CH_OK);
    store = ch_traffic_store_create(4U, &error);
    CH_TEST_ASSERT(store != NULL);
    CH_TEST_ASSERT(ch_traffic_store_configure(store, config, &error) == CH_OK);
    uint64_t persisted_id = ch_traffic_open(store, &direct, &error);
    CH_TEST_ASSERT(persisted_id != 0U);
    ch_traffic_bytes(store, persisted_id, 101U, 202U);
    ch_traffic_close(store, persisted_id, "persisted close");
    json = ch_traffic_snapshot_json(store, config, "Work", "{}", "[]",
                                    &error);
    CH_TEST_ASSERT(json != NULL);
    root = ch_json_parse(json, strlen(json), &error);
    CH_TEST_ASSERT(root != NULL);
    const ch_json_value *persisted_connections = traffic_test_member(
        root, "connections");
    CH_TEST_ASSERT(ch_json_array_size(persisted_connections) == 1U);
    int64_t original_start = 0;
    CH_TEST_ASSERT(ch_json_int64_value(traffic_test_member(
        ch_json_array_get(persisted_connections, 0U), "start_ts_ns"),
        &original_start));
    const ch_json_value *summary = traffic_test_member(root, "summary");
    CH_TEST_ASSERT(ch_json_bool_value(
        traffic_test_member(summary, "history_persisted"), false));
    CH_TEST_ASSERT_STRING(history_path, ch_json_string_value(
        traffic_test_member(summary, "history_path")));
    ch_json_value_destroy(root);
    free(json);
    ch_traffic_store_destroy(store);

    struct stat history_stat;
    CH_TEST_ASSERT(stat(history_path, &history_stat) == 0);
    CH_TEST_ASSERT((history_stat.st_mode & 0777) == 0600);
    store = ch_traffic_store_create(4U, &error);
    CH_TEST_ASSERT(store != NULL);
    CH_TEST_ASSERT(ch_traffic_store_configure(store, config, &error) == CH_OK);
    json = ch_traffic_snapshot_json(store, config, "Work", "{}", "[]",
                                    &error);
    CH_TEST_ASSERT(json != NULL);
    root = ch_json_parse(json, strlen(json), &error);
    CH_TEST_ASSERT(root != NULL);
    persisted_connections = traffic_test_member(root, "connections");
    CH_TEST_ASSERT(ch_json_array_size(persisted_connections) == 1U);
    int64_t loaded_start = 0;
    CH_TEST_ASSERT(ch_json_int64_value(traffic_test_member(
        ch_json_array_get(persisted_connections, 0U), "start_ts_ns"),
        &loaded_start));
    CH_TEST_ASSERT(loaded_start == original_start);
    CH_TEST_ASSERT(ch_json_number_value(traffic_test_member(
        ch_json_array_get(persisted_connections, 0U), "rx_total"), 0.0) ==
        101.0);
    ch_json_value_destroy(root);
    free(json);
    ch_traffic_store_destroy(store);
    ch_config_free(config);
    CH_TEST_ASSERT(unlink(history_path) == 0);
}
