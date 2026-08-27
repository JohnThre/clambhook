#include "test.h"

#include <stdlib.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>

#include "clambhook/config.h"
#include "clambhook/json.h"
#include "clambhook/temporary_rules.h"
#include "clambhook/traffic.h"

void ch_test_temporary_rules(void) {
    static const char config_toml[] =
        "active = \"work\"\n"
        "[[profile]]\nname = \"work\"\n"
        "[profile.listen.tun]\nenabled = true\nchain = \"main\"\n"
        "addresses = [\"198.18.0.1/30\"]\nroutes = [\"0.0.0.0/0\"]\n"
        "[[profile.chain]]\nname = \"main\"\n"
        "[[profile.chain.server]]\nname = \"direct\"\nprotocol = \"direct\"\n";
    ch_error error;
    ch_config *config = NULL;
    CH_TEST_ASSERT(ch_config_parse(config_toml, NULL, &config, &error) ==
                   CH_OK);
    ch_traffic_store *traffic = ch_traffic_store_create(8U, &error);
    CH_TEST_ASSERT(traffic != NULL);
    ch_temporary_rules *rules = ch_temporary_rules_create(8U, &error);
    CH_TEST_ASSERT(rules != NULL);
    ch_traffic_open_info info = {
        .profile = "work",
        .listener_protocol = "tun",
        .rule_action = "direct",
        .target = "203.0.113.7:443",
        .target_host = "api.example",
        .target_port = "443",
        .network = "tcp",
        .source = "198.18.0.2:42000"
    };
    CH_TEST_ASSERT(ch_traffic_open(traffic, &info, &error) == 1U);
    char *response = ch_temporary_rules_create_from_connection_json(
        rules, traffic, config, "work",
        "{\"conn_id\":\"native-1\",\"profile\":\"work\","
        "\"name\":\"block-api\",\"action\":\"block\","
        "\"scope\":\"exact_host\",\"ttl_seconds\":60}", &error);
    CH_TEST_ASSERT(response != NULL);
    ch_json_value *json = ch_json_parse(response, strlen(response), &error);
    CH_TEST_ASSERT(json != NULL);
    CH_TEST_ASSERT(ch_json_array_size(ch_json_object_get(
        json, "temporary_rules")) == 1U);
    const ch_json_value *temporary_rule = ch_json_object_get(
        json, "temporary_rule");
    const char *temporary_id = ch_json_string_value(
        ch_json_object_get(temporary_rule, "id"));
    CH_TEST_ASSERT(temporary_id != NULL && temporary_id[0] != '\0');
    char *temporary_id_copy = malloc(strlen(temporary_id) + 1U);
    CH_TEST_ASSERT(temporary_id_copy != NULL);
    strcpy(temporary_id_copy, temporary_id);
    ch_json_value_destroy(json);
    free(response);

    ch_rule_match_context context = {
        .network = "tcp",
        .target = "api.example:443",
        .source = "198.18.0.2:42000",
        .process_name = "",
        .process_path = ""
    };
    ch_rule_decision decision;
    bool matched = false;
    CH_TEST_ASSERT(ch_temporary_rules_decide(
        rules, "work", &context, &decision, &matched, &error) == CH_OK);
    CH_TEST_ASSERT(matched);
    CH_TEST_ASSERT_STRING("block-api", decision.rule_name);
    CH_TEST_ASSERT_STRING("block", decision.action);
    CH_TEST_ASSERT_STRING("domain", decision.matcher_kind);
    ch_rule_decision_clear(&decision);

    context.target = "other.example:443";
    CH_TEST_ASSERT(ch_temporary_rules_decide(
        rules, "work", &context, &decision, &matched, &error) == CH_OK);
    CH_TEST_ASSERT(!matched);
    char *snapshot = ch_temporary_rules_snapshot_json(rules, "work", &error);
    CH_TEST_ASSERT(snapshot != NULL);
    json = ch_json_parse(snapshot, strlen(snapshot), &error);
    CH_TEST_ASSERT(json != NULL && ch_json_array_size(json) == 1U);
    ch_json_value_destroy(json);
    free(snapshot);

    size_t remove_capacity = strlen(temporary_id_copy) + 16U;
    char *remove_request = malloc(remove_capacity);
    CH_TEST_ASSERT(remove_request != NULL);
    (void)snprintf(remove_request, remove_capacity, "{\"id\":\"%s\"}",
                   temporary_id_copy);
    char *removed = ch_temporary_rules_remove_json(
        rules, remove_request, &error);
    CH_TEST_ASSERT(removed != NULL);
    json = ch_json_parse(removed, strlen(removed), &error);
    CH_TEST_ASSERT(json != NULL);
    CH_TEST_ASSERT(ch_json_array_size(ch_json_object_get(
        json, "temporary_rules")) == 0U);
    ch_json_value_destroy(json);
    free(removed);
    CH_TEST_ASSERT(ch_temporary_rules_remove_json(
        rules, remove_request, &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_NOT_FOUND);
    free(remove_request);
    free(temporary_id_copy);

    response = ch_temporary_rules_create_from_rule_json(
        rules,
        "{\"profile\":\"work\",\"rule\":{\"name\":\"prompt block curl\","
        "\"action\":\"block\",\"processes\":[\"/usr/bin/curl\"],"
        "\"domains\":[\"api.example\"],\"ports\":[443],"
        "\"networks\":[\"tcp\"]},\"position\":\"prepend\"}",
        60, (int)getpid(), "native-2", "api.example:443", "api.example",
        &error);
    CH_TEST_ASSERT(response != NULL);
    CH_TEST_ASSERT(strstr(response, "\"until_quit_pid\":") != NULL);
    free(response);
    CH_TEST_ASSERT(ch_temporary_rules_needs_process(rules));
    context.target = "api.example:443";
    context.process_name = "curl";
    context.process_path = "/usr/bin/curl";
    CH_TEST_ASSERT(ch_temporary_rules_decide(
        rules, "work", &context, &decision, &matched, &error) == CH_OK);
    CH_TEST_ASSERT(matched);
    CH_TEST_ASSERT_STRING("prompt block curl", decision.rule_name);
    ch_rule_decision_clear(&decision);
    context.process_path = "/usr/bin/wget";
    context.process_name = "wget";
    CH_TEST_ASSERT(ch_temporary_rules_decide(
        rules, "work", &context, &decision, &matched, &error) == CH_OK);
    CH_TEST_ASSERT(!matched);

    ch_temporary_rules_destroy(rules);
    ch_traffic_store_destroy(traffic);
    ch_config_free(config);
}
