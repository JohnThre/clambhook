#include "test.h"

#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "clambhook/config.h"
#include "clambhook/json.h"
#include "clambhook/prompt.h"

typedef struct prompt_wait_test {
    ch_prompt_manager *manager;
    ch_prompt_request request;
    bool allow;
    bool gated;
    ch_status status;
    ch_error error;
} prompt_wait_test;

static void *prompt_wait_main(void *opaque) {
    prompt_wait_test *test = opaque;
    test->status = ch_prompt_manager_await(
        test->manager, &test->request, &test->allow, &test->gated,
        &test->error);
    return NULL;
}

static char *prompt_wait_for_id(ch_prompt_manager *manager, size_t waiters) {
    for (int attempt = 0; attempt < 100; ++attempt) {
        ch_error error;
        char *json = ch_prompt_manager_pending_json(manager, &error);
        if (json != NULL) {
            ch_json_value *root = ch_json_parse(json, strlen(json), &error);
            free(json);
            const ch_json_value *prompts = ch_json_object_get(root, "prompts");
            const ch_json_value *prompt = ch_json_array_get(prompts, 0U);
            int64_t count = 0;
            const ch_json_value *count_value = ch_json_object_get(prompt,
                                                                  "waiters");
            const char *id = ch_json_string_value(ch_json_object_get(prompt,
                                                                      "id"));
            if (id != NULL && ch_json_int64_value(count_value, &count) &&
                count >= (int64_t)waiters) {
                char *copy = strdup(id);
                ch_json_value_destroy(root);
                return copy;
            }
            ch_json_value_destroy(root);
        }
        struct timespec delay = {.tv_nsec = 10000000L};
        (void)nanosleep(&delay, NULL);
    }
    return NULL;
}

void ch_test_prompt(void) {
    static const char config_toml[] =
        "active = \"Work\"\n"
        "[prompt]\nenabled = true\ntimeout_seconds = 2\n"
        "default_allow = false\n"
        "[[profile]]\nname = \"Work\"\n"
        "[[profile.chain]]\nname = \"main\"\n"
        "[[profile.chain.server]]\nprotocol = \"direct\"\n";
    ch_error error;
    ch_config *config = NULL;
    CH_TEST_ASSERT(ch_config_parse(config_toml, NULL, &config, &error) ==
                   CH_OK);
    ch_prompt_manager *manager = ch_prompt_manager_create(&error);
    CH_TEST_ASSERT(manager != NULL);
    CH_TEST_ASSERT(ch_prompt_manager_configure(manager, config, &error) ==
                   CH_OK);
    CH_TEST_ASSERT(ch_prompt_manager_enabled(manager));

    prompt_wait_test first = {
        .manager = manager,
        .request = {
            .conn_id = "native-42",
            .profile = "Work",
            .network = "tcp",
            .target = "api.example:443",
            .target_host = "api.example",
            .target_port = "443",
            .process_pid = 42,
            .process_name = "curl",
            .process_path = "/usr/bin/curl",
            .would_use_chain = "main",
            .source = "127.0.0.1:50000"
        }
    };
    prompt_wait_test second = first;
    pthread_t first_thread;
    pthread_t second_thread;
    CH_TEST_ASSERT(pthread_create(&first_thread, NULL, prompt_wait_main,
                                  &first) == 0);
    CH_TEST_ASSERT(pthread_create(&second_thread, NULL, prompt_wait_main,
                                  &second) == 0);
    char *prompt_id = prompt_wait_for_id(manager, 2U);
    CH_TEST_ASSERT(prompt_id != NULL);
    ch_prompt_snapshot snapshot;
    CH_TEST_ASSERT(ch_prompt_manager_resolve(manager, prompt_id, true,
                                             &snapshot, &error) == CH_OK);
    CH_TEST_ASSERT(snapshot.waiters == 2U);
    CH_TEST_ASSERT_STRING("curl", snapshot.process_name);
    CH_TEST_ASSERT(pthread_join(first_thread, NULL) == 0);
    CH_TEST_ASSERT(pthread_join(second_thread, NULL) == 0);
    CH_TEST_ASSERT(first.status == CH_OK && first.gated && first.allow);
    CH_TEST_ASSERT(second.status == CH_OK && second.gated && second.allow);
    char *rule = ch_prompt_rule_request_json(
        &snapshot, config, true, true, true, true, &error);
    CH_TEST_ASSERT(rule != NULL);
    CH_TEST_ASSERT(strstr(rule, "\"action\":\"chain:main\"") != NULL);
    CH_TEST_ASSERT(strstr(rule, "\"processes\":[\"/usr/bin/curl\"]") !=
                   NULL);
    CH_TEST_ASSERT(strstr(rule, "\"domains\":[\"api.example\"]") != NULL);
    CH_TEST_ASSERT(strstr(rule, "\"ports\":[443]") != NULL);
    CH_TEST_ASSERT(strstr(rule, "\"networks\":[\"tcp\"]") != NULL);
    free(rule);
    ch_prompt_snapshot_clear(&snapshot);
    free(prompt_id);

    char *pending = ch_prompt_manager_pending_json(manager, &error);
    CH_TEST_ASSERT_STRING("{\"prompts\":[]}", pending);
    free(pending);

    static const char silent_toml[] =
        "active = \"Work\"\n"
        "[prompt]\nenabled = true\nsilent_mode = \"deny\"\n"
        "[[profile]]\nname = \"Work\"\n";
    ch_config *silent_config = NULL;
    CH_TEST_ASSERT(ch_config_parse(silent_toml, NULL, &silent_config,
                                   &error) == CH_OK);
    CH_TEST_ASSERT(ch_prompt_manager_configure(manager, silent_config,
                                               &error) == CH_OK);
    bool allow = true;
    bool gated = false;
    CH_TEST_ASSERT(ch_prompt_manager_await(manager, &first.request, &allow,
                                           &gated, &error) == CH_OK);
    CH_TEST_ASSERT(gated && !allow);
    char *silent = ch_prompt_manager_silent_json(manager, &error);
    CH_TEST_ASSERT(silent != NULL);
    CH_TEST_ASSERT(strstr(silent, "\"action\":\"deny\"") != NULL);
    CH_TEST_ASSERT(strstr(silent, "\"process_name\":\"curl\"") != NULL);
    free(silent);

    ch_prompt_action_options options;
    CH_TEST_ASSERT(ch_prompt_action_options_parse(
        "{\"id\":\"prompt-9\",\"action\":\"allow\","
        "\"scope\":\"until_quit\",\"match_host\":true,"
        "\"match_port\":true,\"match_protocol\":true,"
        "\"ttl_seconds\":42}", true, &options, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("prompt-9", options.id);
    CH_TEST_ASSERT_STRING("until_quit", options.scope);
    CH_TEST_ASSERT(options.allow && options.match_host &&
                   options.match_port && options.match_protocol);
    CH_TEST_ASSERT(options.ttl_seconds == 42);
    ch_prompt_action_options_clear(&options);
    CH_TEST_ASSERT(ch_prompt_action_options_parse(
        "{\"id\":\"prompt-9\",\"action\":\"skip\"}", true,
        &options, &error) == CH_ERROR_INVALID_ARGUMENT);

    ch_config_free(silent_config);
    ch_prompt_manager_destroy(manager);
    ch_config_free(config);
}
