#include "test.h"

#include <stdint.h>
#include <dirent.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>

#include "clambhook/config.h"

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
    test_atomic_write_and_backup_retention();
}
