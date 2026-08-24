#include "test.h"

#include <stdint.h>
#include <stdio.h>
#include <unistd.h>

#include "clambhook/runtime.h"

void ch_test_runtime(void) {
    ch_error error;
    ch_runtime *runtime = ch_runtime_create(NULL, &error);
    CH_TEST_ASSERT(runtime != NULL);
    CH_TEST_ASSERT(!ch_runtime_is_running(runtime));

    char *json = NULL;
    CH_TEST_ASSERT(ch_runtime_query(runtime, "status", NULL, &json, &error) == CH_OK);
    CH_TEST_ASSERT_STRING(
        "{\"running\":false,\"profile\":\"default\",\"network_info\":{}}",
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
        "{\"running\":false,\"profile\":\"default\",\"network_info\":{}}",
        json
    );
    ch_string_free(json);
    CH_TEST_ASSERT(!ch_runtime_is_running(runtime));

    CH_TEST_ASSERT(ch_runtime_query(runtime, "profiles", NULL, &json, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("{\"profiles\":[\"default\"],\"active\":\"default\"}", json);
    ch_string_free(json);

    CH_TEST_ASSERT(ch_runtime_query(runtime, "missing", NULL, &json, &error) == CH_ERROR_UNSUPPORTED);
    CH_TEST_ASSERT_STRING("unknown runtime query operation", error.message);
    ch_runtime_destroy(runtime);

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
        CH_TEST_ASSERT(ch_runtime_query(runtime, "profiles", NULL, &json, &error) == CH_OK);
        CH_TEST_ASSERT_STRING("{\"profiles\":[\"home\",\"work\"],\"active\":\"work\"}", json);
        ch_string_free(json);
        CH_TEST_ASSERT(ch_runtime_query(runtime, "rules", NULL, &json, &error) == CH_OK);
        CH_TEST_ASSERT(strstr(json, "\"profile\":\"work\"") != NULL);
        CH_TEST_ASSERT(strstr(json, "\"name\":\"direct-web\"") != NULL);
        ch_string_free(json);
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
            "{\"running\":true,\"profile\":\"home\",\"network_info\":{}}",
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
}
