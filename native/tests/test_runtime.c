#include "test.h"

#include <stdint.h>

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

    CH_TEST_ASSERT(ch_runtime_start(runtime, "/tmp/config.toml", &error) == CH_OK);
    CH_TEST_ASSERT(ch_runtime_is_running(runtime));
    CH_TEST_ASSERT(ch_runtime_start(runtime, "/tmp/config.toml", &error) == CH_ERROR_INVALID_STATE);
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
}
