#include "test.h"

#include <stdint.h>
#include <dirent.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

#include "clambhook/config.h"
#include "internal.h"

static const char *valid_toml =
    "active = \"default\"\n"
    "[prompt]\n"
    "timeout_seconds = 30\n"
    "silent_mode = \"allow\"\n"
    "[[profile]]\n"
    "name = \"default\"\n"
    "[profile.conditioner]\n"
    "enabled = true\n"
    "latency = \"40ms\"\n"
    "jitter = \"10ms\"\n"
    "loss_percent = 2.5\n"
    "[profile.listen]\n"
    "socks5 = \"127.0.0.1:1080\"\n"
    "socks5_chain = \"main\"\n"
    "[profile.listen.tun]\n"
    "enabled = true\n"
    "chain = \"main\"\n"
    "mtu = 1400\n"
    "addresses = [\"198.18.0.1/30\"]\n"
    "routes = [\"0.0.0.0/1\", \"128.0.0.0/1\"]\n"
    "[[profile.chain]]\n"
    "name = \"main\"\n"
    "[[profile.chain.server]]\n"
    "name = \"exit\"\n"
    "address = \"203.0.113.10:443\"\n"
    "protocol = \"trojan\"\n"
    "[profile.chain.server.settings]\n"
    "password = \"secret\"\n"
    "[[profile.rule]]\n"
    "name = \"ads\"\n"
    "action = \"block\"\n"
    "ports = [80, 443]\n";

static void test_load_and_query(void) {
    ch_config *config = NULL;
    ch_error error;
    const ch_config_table *profile;
    const ch_config_table *conditioner;
    const ch_config_table *listen;
    const ch_config_table *tun;
    const ch_config_array *routes;
    char *name = NULL;
    char *route = NULL;
    char *resolved = NULL;
    char *json = NULL;
    bool enabled = false;
    int64_t mtu = 0;
    double loss = 0.0;

    CH_TEST_ASSERT(ch_config_parse(valid_toml, "/tmp/config.toml", &config, &error) == CH_OK);
    CH_TEST_ASSERT(config != NULL);
    CH_TEST_ASSERT_STRING("/tmp/config.toml", ch_config_source_path(config));
    CH_TEST_ASSERT(ch_config_profile_count(config) == 1U);
    profile = ch_config_active_profile(config);
    CH_TEST_ASSERT(profile != NULL);
    CH_TEST_ASSERT(ch_config_table_get_string(profile, "name", &name, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("default", name);
    free(name);

    conditioner = ch_config_table_get_table(profile, "conditioner");
    CH_TEST_ASSERT(conditioner != NULL);
    CH_TEST_ASSERT(ch_config_table_get_bool(conditioner, "enabled", &enabled, &error) == CH_OK);
    CH_TEST_ASSERT(enabled);
    CH_TEST_ASSERT(ch_config_table_get_double(conditioner, "loss_percent", &loss, &error) == CH_OK);
    CH_TEST_ASSERT(loss == 2.5);

    listen = ch_config_table_get_table(profile, "listen");
    tun = ch_config_table_get_table(listen, "tun");
    CH_TEST_ASSERT(tun != NULL);
    CH_TEST_ASSERT(ch_config_table_get_int(tun, "mtu", &mtu, &error) == CH_OK);
    CH_TEST_ASSERT(mtu == 1400);
    routes = ch_config_table_get_array(tun, "routes");
    CH_TEST_ASSERT(ch_config_array_count(routes) == 2U);
    CH_TEST_ASSERT(ch_config_array_get_kind(routes) == CH_CONFIG_ARRAY_VALUES);
    CH_TEST_ASSERT(ch_config_array_get_string(routes, 1U, &route, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("128.0.0.0/1", route);
    free(route);
    CH_TEST_ASSERT(ch_config_resolve_path(config, "certs/../ca.pem", &resolved, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("/tmp/ca.pem", resolved);
    free(resolved);
    CH_TEST_ASSERT(ch_config_table_json(tun, &json, &error) == CH_OK);
    CH_TEST_ASSERT(strstr(json, "\"enabled\":true") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"mtu\":1400") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"routes\":[\"0.0.0.0/1\",\"128.0.0.0/1\"]") != NULL);
    free(json);
    ch_config_free(config);
}

static void test_duration_contract(void) {
    int64_t duration = 0;
    ch_error error;
    CH_TEST_ASSERT(ch_config_parse_duration_ns("1h2m3.5s", &duration, &error) == CH_OK);
    CH_TEST_ASSERT(duration == INT64_C(3723500000000));
    CH_TEST_ASSERT(ch_config_parse_duration_ns("40ms", &duration, &error) == CH_OK);
    CH_TEST_ASSERT(duration == INT64_C(40000000));
    CH_TEST_ASSERT(ch_config_parse_duration_ns("3fortnights", &duration, &error) == CH_ERROR_PARSE);
}

static void test_validation(void) {
    ch_config *config = NULL;
    ch_error error;
    const char *bad_active =
        "active = \"missing\"\n"
        "[[profile]]\n"
        "name = \"default\"\n";
    const char *bad_cidr =
        "[[profile]]\n"
        "name = \"default\"\n"
        "[profile.listen.tun]\n"
        "enabled = true\n"
        "chain = \"main\"\n"
        "addresses = [\"not-a-cidr\"]\n"
        "[[profile.chain]]\n"
        "name = \"main\"\n"
        "[[profile.chain.server]]\n"
        "protocol = \"direct\"\n";
    CH_TEST_ASSERT(ch_config_parse(bad_active, NULL, &config, &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(config == NULL);
    CH_TEST_ASSERT(strstr(error.message, "active profile \"missing\" not found") != NULL);
    CH_TEST_ASSERT(ch_config_parse(bad_cidr, NULL, &config, &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(config == NULL);
    CH_TEST_ASSERT(strstr(error.message, "invalid value") != NULL);
}

static void test_repository_config_contracts(void) {
    ch_config *config = NULL;
    ch_error error;
    CH_TEST_ASSERT(ch_config_load(CLAMBHOOK_SOURCE_DIR "/configs/example.toml",
                                  &config, &error) == CH_OK);
    CH_TEST_ASSERT(config != NULL);
    CH_TEST_ASSERT(ch_config_profile_count(config) == 1U);
    ch_config_free(config);
    config = NULL;
    CH_TEST_ASSERT(ch_config_load(CLAMBHOOK_SOURCE_DIR "/packaging/config/config.toml",
                                  &config, &error) == CH_OK);
    CH_TEST_ASSERT(config != NULL);
    ch_config_free(config);
}

static void test_replace_active_profile_document(void) {
    const char *document =
        "# retained header\n"
        "active = \"one\" # selected profile\n"
        "[prompt]\ntimeout_seconds = 30\n"
        "[[profile]]\nname = \"one\"\n"
        "[[profile]]\nname = \"qa\\\"lab\"\n";
    ch_config *config = NULL;
    ch_config *updated = NULL;
    char *toml = NULL;
    char *name = NULL;
    ch_error error;
    CH_TEST_ASSERT(ch_config_parse(document, "/tmp/profile.toml", &config,
                                   &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_document_set_active(
        config, "qa\"lab", &toml, &error) == CH_OK);
    CH_TEST_ASSERT(strstr(toml, "active = \"qa\\\"lab\"\n") != NULL);
    CH_TEST_ASSERT(strstr(toml, "# retained header\n") != NULL);
    CH_TEST_ASSERT(strstr(toml, "[prompt]\ntimeout_seconds = 30\n") != NULL);
    CH_TEST_ASSERT(ch_config_parse(toml, NULL, &updated, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_table_get_string(
        ch_config_active_profile(updated), "name", &name, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("qa\"lab", name);
    free(name);
    free(toml);
    ch_config_free(updated);
    ch_config_free(config);

    const char *without_active =
        "# no root selection\n[[profile]]\nname = \"only\"\n";
    config = NULL;
    toml = NULL;
    CH_TEST_ASSERT(ch_config_parse(without_active, NULL, &config, &error) ==
                   CH_OK);
    CH_TEST_ASSERT(ch_config_document_set_active(config, "only", &toml,
                                                 &error) == CH_OK);
    CH_TEST_ASSERT(strncmp(toml, "active = \"only\"\n", 16U) == 0);
    free(toml);
    ch_config_free(config);
}

static void test_structured_document_mutations(void) {
    ch_config *config = NULL;
    ch_config *updated = NULL;
    char *toml = NULL;
    char *value = NULL;
    int64_t integer = 0;
    double number = 0.0;
    bool enabled = false;
    ch_error error;
    CH_TEST_ASSERT(ch_config_parse(valid_toml, "/tmp/config.toml", &config,
                                   &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "update_conditioner",
        "{\"enabled\":true,\"download_kbps\":2048,"
        "\"upload_kbps\":1024,\"latency\":\"50ms\","
        "\"jitter\":\"5ms\",\"loss_percent\":1.5}",
        &toml, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_parse(toml, "/tmp/config.toml", &updated,
                                   &error) == CH_OK);
    const ch_config_table *profile = ch_config_active_profile(updated);
    const ch_config_table *conditioner = ch_config_table_get_table(
        profile, "conditioner");
    CH_TEST_ASSERT(ch_config_table_get_bool(conditioner, "enabled", &enabled,
                                            &error) == CH_OK && enabled);
    CH_TEST_ASSERT(ch_config_table_get_int(
        conditioner, "download_kbps", &integer, &error) == CH_OK &&
        integer == 2048);
    CH_TEST_ASSERT(ch_config_table_get_double(
        conditioner, "loss_percent", &number, &error) == CH_OK &&
        number == 1.5);
    CH_TEST_ASSERT(ch_config_table_get_string(
        conditioner, "latency", &value, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("50ms", value);
    free(value);
    CH_TEST_ASSERT(ch_config_array_count(
        ch_config_table_get_array(profile, "rule")) == 1U);
    ch_config_free(config);
    config = updated;
    updated = NULL;
    free(toml);
    toml = NULL;

    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "update_dns",
        "{\"enabled\":true,\"timeout\":\"4s\",\"upstreams\":[{"
        "\"name\":\"cloudflare\",\"protocol\":\"dot\","
        "\"address\":\"1.1.1.1:853\","
        "\"server_name\":\"cloudflare-dns.com\"}]}",
        &toml, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_parse(toml, "/tmp/config.toml", &updated,
                                   &error) == CH_OK);
    profile = ch_config_active_profile(updated);
    const ch_config_table *dns = ch_config_table_get_table(profile, "dns");
    CH_TEST_ASSERT(ch_config_table_get_bool(dns, "enabled", &enabled,
                                            &error) == CH_OK && enabled);
    CH_TEST_ASSERT(ch_config_array_count(
        ch_config_table_get_array(dns, "upstream")) == 1U);
    ch_config_free(config);
    config = updated;
    updated = NULL;
    free(toml);
    toml = NULL;

    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "update_config_settings",
        "{\"listen\":{\"http\":\" 127.0.0.1:8081 \","
        "\"tun\":{\"enabled\":true,\"name\":\"clamb0\","
        "\"chain\":\"main\",\"mtu\":1300,"
        "\"addresses\":[\"198.18.0.1/30\"],"
        "\"routes\":[\"0.0.0.0/0\"]}},"
        "\"network_triggers\":[{\"interface\":\"en0\"}],"
        "\"prompt\":{\"enabled\":true,\"silent_mode\":\"deny\"}}",
        &toml, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_parse(toml, "/tmp/config.toml", &updated,
                                   &error) == CH_OK);
    profile = ch_config_active_profile(updated);
    const ch_config_table *listen = ch_config_table_get_table(profile,
                                                               "listen");
    CH_TEST_ASSERT(ch_config_table_get_string(listen, "http", &value,
                                              &error) == CH_OK);
    CH_TEST_ASSERT_STRING("127.0.0.1:8081", value);
    free(value);
    const ch_config_table *tun = ch_config_table_get_table(listen, "tun");
    CH_TEST_ASSERT(ch_config_table_get_int(tun, "mtu", &integer, &error) ==
                   CH_OK && integer == 1300);
    CH_TEST_ASSERT(ch_config_array_count(
        ch_config_table_get_array(profile, "network_trigger")) == 1U);
    const ch_config_table *prompt = ch_config_table_get_table(
        ch_config_root(updated), "prompt");
    CH_TEST_ASSERT(ch_config_table_get_string(prompt, "silent_mode", &value,
                                              &error) == CH_OK);
    CH_TEST_ASSERT_STRING("deny", value);
    free(value);
    ch_config_free(config);
    config = updated;
    updated = NULL;
    free(toml);
    toml = NULL;

    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "replace_rule_sets",
        "{\"rule_sets\":[{\"name\":\"local\","
        "\"domains\":[\"set.example\"]}]}",
        &toml, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_parse(toml, "/tmp/config.toml", &updated,
                                   &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_array_count(ch_config_table_get_array(
        ch_config_active_profile(updated), "rule_set")) == 1U);
    ch_config_free(config);
    config = updated;
    updated = NULL;
    free(toml);
    toml = NULL;

    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "replace_policy_groups",
        "{\"policy_groups\":[{\"name\":\"manual\","
        "\"type\":\"select\",\"chains\":[\"main\"],"
        "\"selected\":\"main\"}]}",
        &toml, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_parse(toml, "/tmp/config.toml", &updated,
                                   &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_array_count(ch_config_table_get_array(
        ch_config_active_profile(updated), "policy_group")) == 1U);
    ch_config_free(config);
    config = updated;
    updated = NULL;
    free(toml);
    toml = NULL;

    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "select_policy_group",
        "{\"group\":\" manual \",\"chain\":\" main \"}",
        &toml, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_parse(toml, "/tmp/config.toml", &updated,
                                   &error) == CH_OK);
    const ch_config_table *selected_group = ch_config_array_get_table(
        ch_config_table_get_array(ch_config_active_profile(updated),
                                  "policy_group"), 0U);
    CH_TEST_ASSERT(ch_config_table_get_string(
        selected_group, "selected", &value, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("main", value);
    free(value);
    ch_config_free(config);
    config = updated;
    updated = NULL;
    free(toml);
    toml = NULL;
    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "select_policy_group",
        "{\"group\":\"manual\",\"chain\":\"missing\"}",
        &toml, &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(toml == NULL);

    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "replace_rules",
        "{\"rules\":[{\"name\":\"set-rule\","
        "\"action\":\"group:manual\",\"rule_sets\":[\"local\"]}]}",
        &toml, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_parse(toml, "/tmp/config.toml", &updated,
                                   &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_array_count(ch_config_table_get_array(
        ch_config_active_profile(updated), "rule")) == 1U);
    ch_config_free(config);
    config = updated;
    updated = NULL;
    free(toml);
    toml = NULL;

    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "create_rule",
        "{\"position\":\"append\",\"rule\":{\"name\":\"second\","
        "\"action\":\"block\",\"domains\":[\"blocked.example\"]}}",
        &toml, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_parse(toml, "/tmp/config.toml", &updated,
                                   &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_array_count(ch_config_table_get_array(
        ch_config_active_profile(updated), "rule")) == 2U);
    ch_config_free(config);
    config = updated;
    updated = NULL;
    free(toml);
    toml = NULL;

    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "replace_rule_subscriptions",
        "{\"subscriptions\":[{\"name\":\"ads\","
        "\"url\":\"https://lists.example/ads.txt\","
        "\"format\":\"auto\",\"action\":\"reject\","
        "\"networks\":[\"tcp\"],\"disabled\":true}]}",
        &toml, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_parse(toml, "/tmp/config.toml", &updated,
                                   &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_array_count(ch_config_table_get_array(
        ch_config_active_profile(updated), "rule_subscription")) == 1U);
    ch_config_free(config);
    config = updated;
    updated = NULL;
    free(toml);
    toml = NULL;

    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "replace_rules", "{\"rules\":[]}",
        &toml, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_parse(toml, "/tmp/config.toml", &updated,
                                   &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_table_get_array(
        ch_config_active_profile(updated), "rule") == NULL);
    free(toml);
    ch_config_free(updated);
    ch_config_free(config);

    config = NULL;
    toml = NULL;
    CH_TEST_ASSERT(ch_config_parse(valid_toml, NULL, &config, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "replace_rules", "{\"rules\":{}}",
        &toml, &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(toml == NULL);
    ch_config_free(config);

    config = NULL;
    toml = NULL;
    CH_TEST_ASSERT(ch_config_parse(valid_toml, NULL, &config, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "update_conditioner",
        "{\"loss_percent\":101}", &toml, &error) ==
        CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(toml == NULL);
    ch_config_free(config);

    config = NULL;
    updated = NULL;
    toml = NULL;
    CH_TEST_ASSERT(ch_config_parse(valid_toml, NULL, &config, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "update_developer_settings",
        "{\"enabled\":true,\"mitm_enabled\":true,"
        "\"https_capture_ack\":true,\"no_cache_enabled\":true,"
        "\"capture_limit\":321,\"body_limit_bytes\":32768,"
        "\"header_value_limit_bytes\":4096,"
        "\"redact_query_params\":[\" Access_Token \",\"SECRET\"],"
        "\"ssl_decrypt_hosts\":[\" *.Example.com \"]}",
        &toml, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_parse(toml, NULL, &updated, &error) == CH_OK);
    const ch_config_table *developer = ch_config_table_get_table(
        ch_config_root(updated), "developer");
    CH_TEST_ASSERT(ch_config_table_get_bool(
        developer, "enabled", &enabled, &error) == CH_OK && enabled);
    CH_TEST_ASSERT(ch_config_table_get_bool(
        developer, "mitm_enabled", &enabled, &error) == CH_OK && enabled);
    CH_TEST_ASSERT(ch_config_table_get_int(
        developer, "capture_limit", &integer, &error) == CH_OK &&
        integer == 321);
    CH_TEST_ASSERT(ch_config_array_get_string(
        ch_config_table_get_array(developer, "redact_query_params"), 0U,
        &value, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("access_token", value);
    free(value);
    CH_TEST_ASSERT(ch_config_array_get_string(
        ch_config_table_get_array(developer, "ssl_decrypt_hosts"), 0U,
        &value, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("*.example.com", value);
    free(value);
    ch_config_free(config);
    config = updated;
    updated = NULL;
    free(toml);
    toml = NULL;

    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "replace_developer_map_rules",
        "{\"rules\":[{\"id\":\"map-1\",\"enabled\":true,"
        "\"match\":{\"methods\":[\"GET\"],\"host\":\"api.example\"},"
        "\"kind\":\"local\",\"local_path\":\"/tmp/reply.json\"}]}",
        &toml, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_parse(toml, NULL, &updated, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_array_count(ch_config_table_get_array(
        ch_config_table_get_table(ch_config_root(updated), "developer"),
        "map_rule")) == 1U);
    ch_config_free(config);
    config = updated;
    updated = NULL;
    free(toml);
    toml = NULL;

    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "replace_developer_breakpoint_rules",
        "{\"rules\":[{\"id\":\"bp-1\",\"enabled\":true,"
        "\"match\":{\"path_prefix\":\"/v1\"},"
        "\"stage\":\"request\"}]}",
        &toml, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_parse(toml, NULL, &updated, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_array_count(ch_config_table_get_array(
        ch_config_table_get_table(ch_config_root(updated), "developer"),
        "breakpoint_rule")) == 1U);
    ch_config_free(config);
    config = updated;
    updated = NULL;
    free(toml);
    toml = NULL;

    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "replace_developer_rewrite_rules",
        "{\"rules\":[{\"id\":\"rw-1\",\"enabled\":true,"
        "\"match\":{\"host\":\"api.example\"},\"stage\":\"request\","
        "\"ops\":[{\"target\":\"header\",\"action\":\"set\","
        "\"field\":\"X-Test\",\"value\":\"native\"}]}]}",
        &toml, &error) == CH_OK);
    CH_TEST_ASSERT(strstr(toml, "[[developer.rewrite_rule.op]]") != NULL);
    CH_TEST_ASSERT(strstr(toml, "[[developer.rewrite_rule.ops]]") == NULL);
    CH_TEST_ASSERT(ch_config_parse(toml, NULL, &updated, &error) == CH_OK);
    developer = ch_config_table_get_table(ch_config_root(updated), "developer");
    CH_TEST_ASSERT(ch_config_array_count(ch_config_table_get_array(
        developer, "rewrite_rule")) == 1U);
    ch_config_free(config);
    config = updated;
    updated = NULL;
    free(toml);
    toml = NULL;

    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "delete_developer_rewrite_rule",
        "{\"id\":\" rw-1 \"}", &toml, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_parse(toml, NULL, &updated, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_table_get_array(
        ch_config_table_get_table(ch_config_root(updated), "developer"),
        "rewrite_rule") == NULL);
    ch_config_free(updated);
    free(toml);
    ch_config_free(config);

    config = NULL;
    toml = NULL;
    CH_TEST_ASSERT(ch_config_parse(valid_toml, NULL, &config, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "default", "update_developer_settings",
        "{\"enabled\":true,\"mitm_enabled\":true}",
        &toml, &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(toml == NULL);
    ch_config_free(config);

    const char *two_profiles =
        "active = \"one\"\n"
        "[[profile]]\nname = \"one\"\n"
        "[[profile]]\nname = \"two\"\n";
    config = NULL;
    updated = NULL;
    CH_TEST_ASSERT(ch_config_parse(two_profiles, NULL, &config, &error) ==
                   CH_OK);
    CH_TEST_ASSERT(ch_config_mutate_document_json(
        config, "two", "update_conditioner",
        "{\"profile\":\"one\",\"enabled\":true}", &toml,
        &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_parse(toml, NULL, &updated, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_table_get_string(
        ch_config_active_profile(updated), "name", &value, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("two", value);
    free(value);
    free(toml);
    ch_config_free(updated);
    ch_config_free(config);
}

static void test_atomic_write_and_backup_retention(void) {
    char directory[160];
    char path[200];
    char document[160];
    char *backup = NULL;
    ch_config *config = NULL;
    ch_error error;
    struct stat info;
    (void)snprintf(directory, sizeof(directory), "/tmp/clambhook-config-write-%ld", (long)getpid());
    (void)snprintf(path, sizeof(path), "%s/config.toml", directory);
    for (int index = 0; index < 7; ++index) {
        (void)snprintf(document, sizeof(document),
                       "active = \"p%d\"\n[[profile]]\nname = \"p%d\"\n", index, index);
        backup = NULL;
        CH_TEST_ASSERT(ch_config_write_atomic_document(path, document, &backup, &error) == CH_OK);
        if (index == 0) CH_TEST_ASSERT(backup == NULL);
        else CH_TEST_ASSERT(backup != NULL);
        free(backup);
    }
    CH_TEST_ASSERT(ch_config_load(path, &config, &error) == CH_OK);
    CH_TEST_ASSERT_STRING(document, ch_config_document(config));
    ch_config_free(config);
    CH_TEST_ASSERT(stat(path, &info) == 0);
    CH_TEST_ASSERT((info.st_mode & 0777) == 0600);
    {
        DIR *stream = opendir(directory);
        struct dirent *entry;
        size_t backup_count = 0U;
        CH_TEST_ASSERT(stream != NULL);
        while ((entry = readdir(stream)) != NULL) {
            if (strstr(entry->d_name, ".bak") != NULL) {
                char backup_path[240];
                ++backup_count;
                (void)snprintf(backup_path, sizeof(backup_path), "%s/%s", directory, entry->d_name);
                (void)unlink(backup_path);
            }
        }
        (void)closedir(stream);
        CH_TEST_ASSERT(backup_count == CH_CONFIG_MAX_BACKUPS);
    }
    CH_TEST_ASSERT(unlink(path) == 0);
    CH_TEST_ASSERT(rmdir(directory) == 0);
}

void ch_test_config(void) {
    test_load_and_query();
    test_duration_contract();
    test_validation();
    test_repository_config_contracts();
    test_replace_active_profile_document();
    test_structured_document_mutations();
    test_atomic_write_and_backup_retention();
}
