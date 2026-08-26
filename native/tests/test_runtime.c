#include "test.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

#include "clambhook/config.h"
#include "clambhook/runtime.h"

typedef struct runtime_network_probe_state {
    unsigned int calls;
} runtime_network_probe_state;

typedef struct runtime_packet_output {
    uint8_t packet[64];
    size_t length;
    unsigned int count;
} runtime_packet_output;

static void runtime_packet_writer(const uint8_t *packet, size_t length,
                                  void *context) {
    runtime_packet_output *output = context;
    ++output->count;
    output->length = length > sizeof(output->packet) ? sizeof(output->packet) :
                                                        length;
    memcpy(output->packet, packet, output->length);
}

static ch_status runtime_network_probe(ch_network_info *out_info,
                                       void *context, ch_error *error) {
    runtime_network_probe_state *state = context;
    ch_error_clear(error);
    ++state->calls;
    memset(out_info, 0, sizeof(*out_info));
    (void)snprintf(out_info->interface_name,
                   sizeof(out_info->interface_name), "en0");
    (void)snprintf(out_info->ssid, sizeof(out_info->ssid), "OfficeNet");
    out_info->is_wifi = true;
    return CH_OK;
}

static bool runtime_wait_for_profile(ch_runtime *runtime, const char *profile,
                                     char **out_status, ch_error *error) {
    struct timespec pause = {.tv_sec = 0, .tv_nsec = 10000000L};
    for (unsigned int attempt = 0U; attempt < 200U; ++attempt) {
        char *status = NULL;
        if (ch_runtime_query(runtime, "status", NULL, &status, error) != CH_OK) {
            return false;
        }
        char expected[320];
        (void)snprintf(expected, sizeof(expected), "\"profile\":\"%s\"",
                       profile);
        if (strstr(status, expected) != NULL) {
            *out_status = status;
            return true;
        }
        ch_string_free(status);
        (void)nanosleep(&pause, NULL);
    }
    return false;
}

static void runtime_assert_config_profile(const char *path,
                                          const char *expected) {
    ch_config *config = NULL;
    char *name = NULL;
    ch_error error;
    CH_TEST_ASSERT(ch_config_load(path, &config, &error) == CH_OK);
    CH_TEST_ASSERT(ch_config_table_get_string(
        ch_config_active_profile(config), "name", &name, &error) == CH_OK);
    CH_TEST_ASSERT_STRING(expected, name);
    free(name);
    ch_config_free(config);
}

void ch_test_runtime(void) {
    ch_error error;
    ch_runtime *runtime = ch_runtime_create(NULL, &error);
    CH_TEST_ASSERT(runtime != NULL);
    CH_TEST_ASSERT(!ch_runtime_is_running(runtime));

    char *json = NULL;
    CH_TEST_ASSERT(ch_runtime_query(runtime, "status", NULL, &json, &error) == CH_OK);
    CH_TEST_ASSERT_STRING(
        "{\"running\":false,\"profile\":\"default\",\"network_info\":{},"
        "\"dns\":{\"enabled\":false}}",
        json
    );
    ch_string_free(json);

    CH_TEST_ASSERT(ch_runtime_start(runtime, "", &error) == CH_OK);
    CH_TEST_ASSERT(ch_runtime_is_running(runtime));
    CH_TEST_ASSERT(ch_runtime_start(runtime, "", &error) == CH_ERROR_INVALID_STATE);
    CH_TEST_ASSERT_STRING("engine already running", error.message);

    uint8_t packet[] = {0x45U, 0x00U};
    CH_TEST_ASSERT(
        ch_runtime_inject_packet(runtime, packet, sizeof(packet), &error) == CH_ERROR_UNSUPPORTED
    );

    CH_TEST_ASSERT(ch_runtime_mutate(runtime, "disconnect", NULL, &json, &error) == CH_OK);
    CH_TEST_ASSERT_STRING(
        "{\"running\":false,\"profile\":\"default\",\"network_info\":{},"
        "\"dns\":{\"enabled\":false}}",
        json
    );
    ch_string_free(json);
    CH_TEST_ASSERT(!ch_runtime_is_running(runtime));

    CH_TEST_ASSERT(ch_runtime_query(runtime, "profiles", NULL, &json, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("{\"profiles\":[\"default\"],\"active\":\"default\"}", json);
    ch_string_free(json);

    CH_TEST_ASSERT(ch_runtime_mutate(
        runtime, "persist_active_profile", "{\"name\":\" default \"}",
        &json, &error) == CH_OK);
    CH_TEST_ASSERT(strstr(json, "\"profile\":\"default\"") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"persisted\":false") != NULL);
    ch_string_free(json);

    CH_TEST_ASSERT(ch_runtime_query(
        runtime, "config_export", NULL, &json, &error) ==
        CH_ERROR_INVALID_STATE);
    CH_TEST_ASSERT_STRING("config export requires daemon config path",
                          error.message);
    CH_TEST_ASSERT(ch_runtime_mutate(
        runtime, "config_import", "active = \"default\"\n", &json,
        &error) == CH_ERROR_INVALID_STATE);

    CH_TEST_ASSERT(ch_runtime_query(runtime, "missing", NULL, &json, &error) == CH_ERROR_UNSUPPORTED);
    CH_TEST_ASSERT_STRING("unknown runtime query operation", error.message);
    ch_runtime_destroy(runtime);

    {
        char path[160];
        (void)snprintf(path, sizeof(path),
                       "/tmp/clambhook-packet-runtime-%ld.toml",
                       (long)getpid());
        FILE *file = fopen(path, "wb");
        CH_TEST_ASSERT(file != NULL);
        CH_TEST_ASSERT(fputs(
            "active = \"tunnel\"\n"
            "[[profile]]\nname = \"tunnel\"\n"
            "[profile.listen.tun]\nenabled = true\nmtu = 1400\n"
            "addresses = [\"198.18.0.9/29\", "
            "\"fd7a:636c:616d::9/64\"]\n"
            "[[profile.chain]]\nname = \"direct\"\n"
            "[[profile.chain.server]]\nprotocol = \"direct\"\n"
            "[[profile]]\nname = \"proxy\"\n"
            "[[profile.chain]]\nname = \"direct\"\n"
            "[[profile.chain.server]]\nprotocol = \"direct\"\n",
            file) >= 0);
        CH_TEST_ASSERT(fclose(file) == 0);
        runtime_packet_output output = {0};
        ch_runtime_options options = {
            .packet_writer = runtime_packet_writer,
            .packet_writer_context = &output
        };
        runtime = ch_runtime_create(&options, &error);
        CH_TEST_ASSERT(runtime != NULL);
        CH_TEST_ASSERT(ch_runtime_start(runtime, path, &error) == CH_OK);
        CH_TEST_ASSERT(ch_runtime_query(runtime, "status", NULL, &json,
                                        &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"tunnel_mode\":\"tun\"") != NULL);
        ch_string_free(json);
        const uint8_t echo[] = {
            0x45U, 0x00U, 0x00U, 0x20U, 0x12U, 0x34U, 0x00U, 0x00U,
            64U, 1U, 0xdcU, 0x71U, 198U, 18U, 0U, 10U,
            198U, 18U, 0U, 9U,
            8U, 0U, 0x6dU, 0x60U, 0xabU, 0xcdU, 0U, 1U,
            'p', 'i', 'n', 'g'
        };
        CH_TEST_ASSERT(ch_runtime_inject_packet(
            runtime, echo, sizeof(echo), &error) == CH_OK);
        CH_TEST_ASSERT(output.count == 1U);
        CH_TEST_ASSERT(output.length == sizeof(echo));
        CH_TEST_ASSERT(output.packet[20] == 0U);
        CH_TEST_ASSERT(ch_runtime_mutate(
            runtime, "set_active_profile", "{\"name\":\"proxy\"}",
            &json, &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"tunnel_mode\"") == NULL);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_inject_packet(
            runtime, echo, sizeof(echo), &error) == CH_ERROR_UNSUPPORTED);
        CH_TEST_ASSERT(ch_runtime_mutate(
            runtime, "set_active_profile", "{\"name\":\"tunnel\"}",
            &json, &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"tunnel_mode\":\"tun\"") != NULL);
        ch_string_free(json);
        ch_runtime_destroy(runtime);
        CH_TEST_ASSERT(unlink(path) == 0);
    }

    {
        char path[160];
        (void)snprintf(path, sizeof(path),
                       "/tmp/clambhook-dns-runtime-%ld.toml",
                       (long)getpid());
        FILE *file = fopen(path, "wb");
        CH_TEST_ASSERT(file != NULL);
        CH_TEST_ASSERT(fputs(
            "active = \"secured\"\n"
            "[[profile]]\nname = \"secured\"\n"
            "[profile.dns]\nenabled = true\n"
            "[[profile.dns.upstream]]\nprotocol = \"controld\"\n"
            "resolver = \"abc123\"\n"
            "[[profile.chain]]\nname = \"direct\"\n"
            "[[profile.chain.server]]\nprotocol = \"direct\"\n"
            "[[profile]]\nname = \"plain\"\n"
            "[[profile.chain]]\nname = \"direct\"\n"
            "[[profile.chain.server]]\nprotocol = \"direct\"\n",
            file) >= 0);
        CH_TEST_ASSERT(fclose(file) == 0);
        runtime = ch_runtime_create(NULL, &error);
        CH_TEST_ASSERT(runtime != NULL);
        CH_TEST_ASSERT(ch_runtime_start(runtime, path, &error) == CH_OK);
        CH_TEST_ASSERT(ch_runtime_query(runtime, "status", NULL, &json,
                                        &error) == CH_OK);
        CH_TEST_ASSERT(strstr(
            json,
            "\"dns\":{\"enabled\":true,"
            "\"upstreams\":[\"controld:abc123\"]}") != NULL);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_mutate(
            runtime, "set_active_profile", "{\"name\":\"plain\"}",
            &json, &error) == CH_OK);
        CH_TEST_ASSERT_STRING(
            "{\"running\":true,\"profile\":\"plain\","
            "\"network_info\":{},\"dns\":{\"enabled\":false}}",
            json);
        ch_string_free(json);
        ch_runtime_destroy(runtime);
        CH_TEST_ASSERT(unlink(path) == 0);
    }

    {
        char path[160];
        (void)snprintf(path, sizeof(path),
                       "/tmp/clambhook-netwatch-runtime-%ld.toml",
                       (long)getpid());
        FILE *file = fopen(path, "wb");
        CH_TEST_ASSERT(file != NULL);
        CH_TEST_ASSERT(fputs(
            "active = \"home\"\n"
            "[[profile]]\nname = \"home\"\n"
            "[[profile.chain]]\nname = \"home-default\"\n"
            "[[profile.chain.server]]\nprotocol = \"direct\"\n"
            "[[profile]]\nname = \"office\"\n"
            "[[profile.network_trigger]]\nssid = \"officenet\"\n"
            "interface = \"EN0\"\n"
            "[[profile.chain]]\nname = \"office-default\"\n"
            "[[profile.chain.server]]\nprotocol = \"direct\"\n"
            "[[profile]]\nname = \"later\"\n"
            "[[profile.network_trigger]]\ninterface = \"en0\"\n"
            "[[profile.chain]]\nname = \"later-default\"\n"
            "[[profile.chain.server]]\nprotocol = \"direct\"\n",
            file) >= 0);
        CH_TEST_ASSERT(fclose(file) == 0);
        runtime_network_probe_state probe = {0};
        ch_runtime_options options = {
            .network_probe = runtime_network_probe,
            .network_probe_context = &probe,
            .network_poll_milliseconds = 10U
        };
        runtime = ch_runtime_create(&options, &error);
        CH_TEST_ASSERT(runtime != NULL);
        CH_TEST_ASSERT(ch_runtime_start(runtime, path, &error) == CH_OK);
        json = NULL;
        CH_TEST_ASSERT(runtime_wait_for_profile(runtime, "office", &json,
                                                &error));
        CH_TEST_ASSERT(strstr(
            json,
            "\"network_info\":{\"interface_name\":\"en0\","
            "\"ssid\":\"OfficeNet\",\"is_wifi\":true}") != NULL);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_query(runtime, "profiles", NULL, &json,
                                        &error) == CH_OK);
        CH_TEST_ASSERT_STRING(
            "{\"profiles\":[\"home\",\"office\",\"later\"],"
            "\"active\":\"office\"}",
            json);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_stop(runtime, &error) == CH_OK);
        unsigned int stopped_probe_count = probe.calls;
        struct timespec pause = {.tv_sec = 0, .tv_nsec = 50000000L};
        (void)nanosleep(&pause, NULL);
        CH_TEST_ASSERT(probe.calls == stopped_probe_count);
        ch_runtime_destroy(runtime);
        CH_TEST_ASSERT(unlink(path) == 0);
    }

    {
        char path[128];
        FILE *file;
        (void)snprintf(path, sizeof(path), "/tmp/clambhook-config-%ld.toml", (long)getpid());
        file = fopen(path, "wb");
        CH_TEST_ASSERT(file != NULL);
        CH_TEST_ASSERT(fputs(
            "active = \"work\"\n"
            "[[profile]]\nname = \"home\"\n"
            "[[profile.chain]]\nname = \"home-default\"\n"
            "[[profile.chain.server]]\nprotocol = \"direct\"\n"
            "[[profile]]\nname = \"work\"\n"
            "[profile.listen]\nsocks5 = \"127.0.0.1:0\"\n"
            "[[profile.chain]]\nname = \"work-default\"\n"
            "[[profile.chain.server]]\nprotocol = \"direct\"\n"
            "[[profile.rule]]\nname = \"direct-web\"\naction = \"direct\"\n"
            "ports = [80, 443]\nnetworks = [\"tcp\"]\n",
            file
        ) >= 0);
        CH_TEST_ASSERT(fclose(file) == 0);
        runtime = ch_runtime_create(NULL, &error);
        CH_TEST_ASSERT(runtime != NULL);
        CH_TEST_ASSERT(ch_runtime_start(runtime, path, &error) == CH_OK);
        CH_TEST_ASSERT(ch_runtime_query(runtime, "status", NULL, &json, &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"listeners\":[{\"protocol\":\"socks5\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"active_conns\":0") != NULL);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_query(runtime, "profiles", NULL, &json, &error) == CH_OK);
        CH_TEST_ASSERT_STRING("{\"profiles\":[\"home\",\"work\"],\"active\":\"work\"}", json);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_query(runtime, "rules", NULL, &json, &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"profile\":\"work\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"name\":\"direct-web\"") != NULL);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_query(
            runtime, "rules", "{\"profile\":\"home\"}", &json,
            &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"profile\":\"home\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "direct-web") == NULL);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_query(
            runtime, "rules", "{\"profile\":\"missing\"}", &json,
            &error) == CH_ERROR_NOT_FOUND);
        CH_TEST_ASSERT_STRING("profile missing not found", error.message);
        CH_TEST_ASSERT(ch_runtime_query(runtime, "servers", NULL, &json, &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"name\":\"work-default\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"servers\":[{\"name\":\"\",\"address\":\"\",\"protocol\":\"direct\"") != NULL);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_query(
            runtime, "test_rule",
            "{\"profile\":\"\",\"network\":\"tcp\",\"target\":\"example.com:443\",\"source\":\"\"}",
            &json, &error
        ) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"rule_name\":\"direct-web\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"action\":\"direct\"") != NULL);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_query(runtime, "config", NULL, &json, &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"name\":\"work\"") != NULL);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_mutate(
            runtime, "set_active_profile", "{\"name\":\"home\"}", &json, &error
        ) == CH_OK);
        CH_TEST_ASSERT_STRING(
            "{\"running\":true,\"profile\":\"home\",\"network_info\":{},"
            "\"dns\":{\"enabled\":false}}",
            json
        );
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_query(runtime, "rules", NULL, &json, &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"profile\":\"home\"") != NULL);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_query(runtime, "config", NULL, &json, &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"name\":\"home\"") != NULL);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_mutate(
            runtime, "set_active_profile", "{\"name\":\"missing\"}", &json, &error
        ) == CH_ERROR_NOT_FOUND);
        ch_runtime_destroy(runtime);
        CH_TEST_ASSERT(unlink(path) == 0);
    }

    {
        char path[160];
        char backup_path[256];
        (void)snprintf(path, sizeof(path),
                       "/tmp/clambhook-persist-runtime-%ld.toml",
                       (long)getpid());
        FILE *file = fopen(path, "wb");
        CH_TEST_ASSERT(file != NULL);
        CH_TEST_ASSERT(fputs(
            "active = \"one\"\n"
            "[[profile]]\nname = \"one\"\n"
            "[[profile.chain]]\nname = \"direct\"\n"
            "[[profile.chain.server]]\nprotocol = \"direct\"\n"
            "[[profile]]\nname = \"two\"\n"
            "[[profile.chain]]\nname = \"direct\"\n"
            "[[profile.chain.server]]\nprotocol = \"direct\"\n",
            file) >= 0);
        CH_TEST_ASSERT(fclose(file) == 0);
        runtime = ch_runtime_create(NULL, &error);
        CH_TEST_ASSERT(runtime != NULL);
        CH_TEST_ASSERT(ch_runtime_start(runtime, path, &error) == CH_OK);
        CH_TEST_ASSERT(ch_runtime_mutate(
            runtime, "persist_active_profile", "{\"name\":\" two \"}",
            &json, &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"profile\":\"two\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"persisted\":true") != NULL);
        const char *backup = strstr(json, "\"backup_path\":\"");
        CH_TEST_ASSERT(backup != NULL);
        backup += strlen("\"backup_path\":\"");
        const char *backup_end = strchr(backup, '\"');
        CH_TEST_ASSERT(backup_end != NULL);
        size_t backup_length = (size_t)(backup_end - backup);
        CH_TEST_ASSERT(backup_length > 0U &&
                       backup_length < sizeof(backup_path));
        memcpy(backup_path, backup, backup_length);
        backup_path[backup_length] = '\0';
        ch_string_free(json);
        runtime_assert_config_profile(path, "two");
        runtime_assert_config_profile(backup_path, "one");
        CH_TEST_ASSERT(ch_runtime_mutate(
            runtime, "persist_active_profile", "{\"name\":\"missing\"}",
            &json, &error) == CH_ERROR_NOT_FOUND);
        runtime_assert_config_profile(path, "two");
        ch_runtime_destroy(runtime);

        runtime = ch_runtime_create(NULL, &error);
        CH_TEST_ASSERT(runtime != NULL);
        CH_TEST_ASSERT(ch_runtime_start(runtime, path, &error) == CH_OK);
        CH_TEST_ASSERT(ch_runtime_query(runtime, "status", NULL, &json,
                                        &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"profile\":\"two\"") != NULL);
        ch_string_free(json);
        ch_runtime_destroy(runtime);
        CH_TEST_ASSERT(unlink(path) == 0);
        CH_TEST_ASSERT(unlink(backup_path) == 0);
    }

    {
        static const char original[] =
            "active = \"one\"\n"
            "[[profile]]\nname = \"one\"\n"
            "[[profile.chain]]\nname = \"direct\"\n"
            "[[profile.chain.server]]\nprotocol = \"direct\"\n";
        static const char imported[] =
            "# imported document\n"
            "active = \"two\"\n"
            "[[profile]]\nname = \"one\"\n"
            "[[profile.chain]]\nname = \"direct\"\n"
            "[[profile.chain.server]]\nprotocol = \"direct\"\n"
            "[[profile]]\nname = \"two\"\n"
            "[[profile.chain]]\nname = \"direct\"\n"
            "[[profile.chain.server]]\nprotocol = \"direct\"\n";
        char path[160];
        char backup_path[256];
        (void)snprintf(path, sizeof(path),
                       "/tmp/clambhook-import-runtime-%ld.toml",
                       (long)getpid());
        FILE *file = fopen(path, "wb");
        CH_TEST_ASSERT(file != NULL);
        CH_TEST_ASSERT(fputs(original, file) >= 0);
        CH_TEST_ASSERT(fclose(file) == 0);
        runtime = ch_runtime_create(NULL, &error);
        CH_TEST_ASSERT(runtime != NULL);
        CH_TEST_ASSERT(ch_runtime_start(runtime, path, &error) == CH_OK);
        CH_TEST_ASSERT(ch_runtime_query(
            runtime, "config_export", NULL, &json, &error) == CH_OK);
        CH_TEST_ASSERT_STRING(original, json);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_mutate(
            runtime, "config_import", "not valid = [", &json,
            &error) == CH_ERROR_PARSE);
        runtime_assert_config_profile(path, "one");
        CH_TEST_ASSERT(ch_runtime_mutate(
            runtime, "config_import", imported, &json, &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"profiles\":[\"one\",\"two\"]") !=
                       NULL);
        CH_TEST_ASSERT(strstr(json, "\"active\":\"two\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"message\":\"imported 2 profile(s)\"") !=
                       NULL);
        const char *backup = strstr(json, "\"backup_path\":\"");
        CH_TEST_ASSERT(backup != NULL);
        backup += strlen("\"backup_path\":\"");
        const char *backup_end = strchr(backup, '\"');
        CH_TEST_ASSERT(backup_end != NULL);
        size_t backup_length = (size_t)(backup_end - backup);
        CH_TEST_ASSERT(backup_length > 0U &&
                       backup_length < sizeof(backup_path));
        memcpy(backup_path, backup, backup_length);
        backup_path[backup_length] = '\0';
        ch_string_free(json);
        runtime_assert_config_profile(path, "two");
        runtime_assert_config_profile(backup_path, "one");
        CH_TEST_ASSERT(ch_runtime_query(
            runtime, "config_export", NULL, &json, &error) == CH_OK);
        CH_TEST_ASSERT_STRING(imported, json);
        ch_string_free(json);
        ch_runtime_destroy(runtime);

        runtime = ch_runtime_create(NULL, &error);
        CH_TEST_ASSERT(runtime != NULL);
        CH_TEST_ASSERT(ch_runtime_start(runtime, path, &error) == CH_OK);
        CH_TEST_ASSERT(ch_runtime_query(runtime, "status", NULL, &json,
                                        &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"profile\":\"two\"") != NULL);
        ch_string_free(json);
        ch_runtime_destroy(runtime);
        CH_TEST_ASSERT(unlink(path) == 0);
        CH_TEST_ASSERT(unlink(backup_path) == 0);
    }

    {
        char path[160];
        (void)snprintf(path, sizeof(path),
                       "/tmp/clambhook-settings-runtime-%ld.toml",
                       (long)getpid());
        FILE *file = fopen(path, "wb");
        CH_TEST_ASSERT(file != NULL);
        CH_TEST_ASSERT(fputs(
            "active = \"rich\"\n"
            "[prompt]\nenabled = true\ntimeout_seconds = 20\n"
            "default_allow = true\nsilent_mode = \"allow\"\n"
            "[[profile]]\nname = \"rich\"\n"
            "[[profile.network_trigger]]\nssid = \"Office\"\n"
            "interface = \"wlan0\"\n"
            "[profile.listen]\nsocks5 = \"127.0.0.1:1080\"\n"
            "socks5_chain = \"direct\"\nhttp = \"127.0.0.1:8080\"\n"
            "http_chain = \"direct\"\n"
            "[profile.listen.tun]\nenabled = true\nname = \"clamb0\"\n"
            "chain = \"direct\"\nmtu = 1400\n"
            "addresses = [\"198.18.0.1/30\"]\n"
            "routes = [\"0.0.0.0/0\"]\n"
            "exclude_cidrs = [\"127.0.0.0/8\"]\n"
            "[profile.dns]\nenabled = true\ntimeout = \"4s\"\n"
            "[[profile.dns.upstream]]\nname = \"cloudflare\"\n"
            "protocol = \"dot\"\naddress = \"1.1.1.1:853\"\n"
            "server_name = \"cloudflare-dns.com\"\n"
            "[profile.conditioner]\nenabled = true\n"
            "download_kbps = 2048\nupload_kbps = 1024\n"
            "latency = \"50ms\"\njitter = \"5ms\"\n"
            "loss_percent = 1.5\n"
            "[[profile.rule_subscription]]\nname = \"ads\"\n"
            "url = \"https://lists.example.invalid/ads.txt\"\n"
            "format = \"adblock\"\naction = \"reject\"\n"
            "networks = [\"tcp\"]\ndisabled = true\n"
            "[[profile.chain]]\nname = \"direct\"\n"
            "[[profile.chain.server]]\nprotocol = \"direct\"\n",
            file) >= 0);
        CH_TEST_ASSERT(fclose(file) == 0);
        runtime = ch_runtime_create(NULL, &error);
        CH_TEST_ASSERT(runtime != NULL);
        CH_TEST_ASSERT(ch_runtime_reload(runtime, path, &error) == CH_OK);
        CH_TEST_ASSERT(ch_runtime_query(
            runtime, "dns", "{}", &json, &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"profile\":\"rich\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"strategy\":\"encrypted\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"timeout\":\"4s\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"protocol\":\"dot\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"intercepts_port_53\":true") != NULL);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_query(
            runtime, "config_settings", "{}", &json, &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"socks5\":\"127.0.0.1:1080\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"tun\":{\"enabled\":true") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"network_triggers\":[{\"ssid\":\"Office\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"prompt\":{\"enabled\":true") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"silent_mode\":\"allow\"") != NULL);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_query(
            runtime, "conditioner", "{}", &json, &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"download_kbps\":2048") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"latency\":\"50ms\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"loss_percent\":1.5") != NULL);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_query(
            runtime, "rule_subscriptions", "{}", &json, &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"name\":\"ads\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"format\":\"adblock\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"action\":\"reject\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"cached\":false") != NULL);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_query(
            runtime, "dns", "{\"profile\":\"missing\"}", &json,
            &error) == CH_ERROR_NOT_FOUND);
        ch_runtime_destroy(runtime);
        CH_TEST_ASSERT(unlink(path) == 0);
    }
}
