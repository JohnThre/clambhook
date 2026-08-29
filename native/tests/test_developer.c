// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

#include "clambhook/config.h"
#include "clambhook/developer.h"
#include "clambhook/developer_curl.h"
#include "developer_internal.h"

typedef struct developer_breakpoint_test {
    ch_developer_manager *manager;
    int resolved;
} developer_breakpoint_test;

static void *developer_breakpoint_resolver(void *context) {
    developer_breakpoint_test *test = context;
    struct timespec delay = {.tv_nsec = 10000000L};
    for (int attempt = 0; attempt < 200; ++attempt) {
        ch_error error;
        char *pending = ch_developer_pending_breakpoints_json(test->manager,
                                                              &error);
        if (pending != NULL && strstr(pending, "\"id\":\"bp-1\"") !=
                                   NULL) {
            char *resolved = ch_developer_resolve_breakpoint_json(
                test->manager, "bp-1", "{\"action\":\"drop\"}", &error);
            test->resolved = resolved != NULL;
            free(resolved);
            free(pending);
            return NULL;
        }
        free(pending);
        (void)nanosleep(&delay, NULL);
    }
    return NULL;
}

void ch_test_developer(void) {
    ch_error error;
    char *json = ch_developer_curl_import_json(
        "{\"curl\":\"curl https://api.example.com -H 'X-Test: yes' "
        "-A 'ClambHook Test' -e https://ref.example -d '{\\\"a\\\":1}' "
        "--data-raw second\"}",
        &error);
    CH_TEST_ASSERT(json != NULL);
    CH_TEST_ASSERT_STRING(
        "{\"method\":\"POST\",\"url\":\"https://api.example.com\","
        "\"headers\":[{\"name\":\"X-Test\",\"value\":\"yes\"},"
        "{\"name\":\"User-Agent\",\"value\":\"ClambHook Test\"},"
        "{\"name\":\"Referer\",\"value\":\"https://ref.example\"}],"
        "\"body\":\"{\\\"a\\\":1}&second\"}",
        json);
    free(json);
    json = ch_developer_curl_import_json(
        "{\"curl\":\"/usr/bin/curl -X patch --url "
        "https://api.example.com --connect-timeout 2 --compressed\"}",
        &error);
    CH_TEST_ASSERT_STRING(
        "{\"method\":\"PATCH\",\"url\":\"https://api.example.com\","
        "\"headers\":[],\"body\":\"\"}",
        json);
    free(json);
    json = ch_developer_curl_import_json(
        "{\"curl\":\"curl https://api.example.com -d '' -d second\"}",
        &error);
    CH_TEST_ASSERT(json != NULL);
    CH_TEST_ASSERT(strstr(json, "\"method\":\"POST\"") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"body\":\"&second\"") != NULL);
    free(json);
    CH_TEST_ASSERT(ch_developer_curl_import_json(
        "{\"curl\":\"curl -H 'X: y\"}", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(ch_developer_curl_import_json(
        "{\"curl\":\"curl https://example.com -d @secret.txt\"}",
        &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(ch_developer_curl_import_json(
        "{\"curl\":\"curl -H\"}", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);

    static const char document[] =
        "active = \"default\"\n"
        "[developer]\n"
        "enabled = true\n"
        "no_cache_enabled = true\n"
        "capture_limit = 2\n"
        "body_limit_bytes = 8\n"
        "header_value_limit_bytes = 12\n"
        "redact_headers = [\"authorization\", \"set-cookie\"]\n"
        "redact_query_params = [\"token\"]\n"
        "[[profile]]\n"
        "name = \"default\"\n";
    ch_config *config = NULL;
    CH_TEST_ASSERT(ch_config_parse(document, "/tmp/developer.toml", &config,
                                   &error) == CH_OK);
    ch_developer_manager *manager = ch_developer_manager_create(&error);
    CH_TEST_ASSERT(manager != NULL);
    CH_TEST_ASSERT(ch_developer_manager_configure(manager, config, &error) ==
                   CH_OK);
    CH_TEST_ASSERT(ch_developer_send_json(
        manager, "{\"url\":\"http://localhost/private\"}", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "not public") != NULL);
    CH_TEST_ASSERT(ch_developer_send_json(
        manager, "{\"url\":\"http://user:secret@example.com/\"}",
        &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "credentials") != NULL);
    CH_TEST_ASSERT(ch_developer_send_json(
        manager, "{\"url\":\"http://@example.com/\"}", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "credentials") != NULL);
    CH_TEST_ASSERT(ch_developer_send_json(
        manager, "{\"url\":\"http://[::ffff:127.0.0.1]/private\"}",
        &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(ch_developer_send_json(
        manager, "{\"url\":\"http://0.0.0.1/private\"}", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(ch_developer_send_json(
        manager, "{\"url\":\"http://192.0.2.1/private\"}", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(ch_developer_send_json(
        manager, "{\"url\":\"http://[2001:db8::1]/private\"}",
        &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(ch_developer_send_json(
        manager, "{\"method\":\"GET\\nInjected\","
                 "\"url\":\"https://example.com\"}", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(ch_developer_send_json(
        manager, "{\"url\":\"https://example.com\",\"headers\":[{"
                 "\"name\":\"Host\",\"value\":\"internal\"}]}",
        &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(ch_developer_repeat_json(
        manager, "{\"entry_id\":\"missing\"}", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_NOT_FOUND);

    static const char request_headers[] =
        "Host: api.example.com\r\n"
        "Authorization: Bearer top-secret\r\n"
        "Content-Type: application/json\r\n";
    ch_developer_capture_metadata metadata = {
        .flow_id = 42U,
        .profile = "default",
        .client_address = "127.0.0.1:54321",
        .chain_name = "main",
        .method = "POST",
        .url = "http://api.example.com/items?token=secret&keep=yes",
        .scheme = "http",
        .host = "api.example.com",
        .request_headers = request_headers,
        .request_headers_length = sizeof(request_headers) - 1U
    };
    ch_developer_capture *capture = ch_developer_capture_begin(
        manager, &metadata, &error);
    CH_TEST_ASSERT(capture != NULL);
    static const uint8_t request_body[] = "{\"x\":12345}";
    ch_developer_capture_request_body(capture, request_body,
                                      sizeof(request_body) - 1U);
    static const uint8_t response_a[] = "HTTP/2 201 Cr";
    static const uint8_t response_b[] =
        "eated\r\nContent-Type: application/json\r\n"
        "Set-Cookie: session=secret\r\n\r\n{\"ok\":true}";
    ch_developer_capture_response(capture, response_a,
                                  sizeof(response_a) - 1U);
    ch_developer_capture_response(capture, response_b,
                                  sizeof(response_b) - 1U);
    ch_developer_capture_finish(capture, NULL);

    json = ch_developer_status_json(manager, &error);
    CH_TEST_ASSERT(json != NULL);
    CH_TEST_ASSERT(strstr(json, "\"enabled\":true") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"mitm_enabled\":false") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"capture_count\":1") != NULL);
    free(json);

    json = ch_developer_entries_json(
        manager,
        "{\"methods\":[\"post\"],\"status_min\":200,"
        "\"status_max\":299,\"host\":\"EXAMPLE\","
        "\"content_type\":\"application\",\"query\":\"application/\","
        "\"limit\":1}",
        &error);
    CH_TEST_ASSERT(json != NULL);
    CH_TEST_ASSERT(strstr(json, "\"id\":\"dev-1\"") != NULL);
    CH_TEST_ASSERT(strstr(json, "token=%5Bredacted%5D&keep=yes") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"conn_id\":\"conn-42\"") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"status\":201") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"value\":\"[redacted]\"") != NULL);
    CH_TEST_ASSERT(strstr(json, "Bearer top-secret") == NULL);
    CH_TEST_ASSERT(strstr(json, "session=secret") == NULL);
    CH_TEST_ASSERT(strstr(json, "\"preview\":\"{\\\"x\\\":123\"") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"truncated\":true") != NULL);
    free(json);

    json = ch_developer_entry_json(manager, "dev-1", &error);
    CH_TEST_ASSERT(json != NULL);
    CH_TEST_ASSERT(strstr(json, "\"profile\":\"default\"") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"chain_name\":\"main\"") != NULL);
    free(json);
    CH_TEST_ASSERT(ch_developer_entry_json(manager, "missing", &error) ==
                   NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_NOT_FOUND);

    json = ch_developer_entry_curl_json(manager, "dev-1", &error);
    CH_TEST_ASSERT(json != NULL);
    CH_TEST_ASSERT(strstr(json, "curl -X 'POST'") != NULL);
    CH_TEST_ASSERT(strstr(json, "Authorization: Bearer top-secret") == NULL);
    CH_TEST_ASSERT(strstr(json, "redacted headers omitted: Authorization") !=
                   NULL);
    CH_TEST_ASSERT(strstr(json, "captured request body was truncated") !=
                   NULL);
    free(json);
    CH_TEST_ASSERT(ch_developer_repeat_json(
        manager, "{\"entry_id\":\"dev-1\"}", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "truncated") != NULL);
    CH_TEST_ASSERT(ch_developer_repeat_json(
        manager, "{\"entry_id\":\"dev-1\",\"body\":\"\","
                 "\"url\":\"http://127.0.0.1/private\"}", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "non-public") != NULL);

    json = ch_developer_har_json(manager, &error);
    CH_TEST_ASSERT(json != NULL);
    CH_TEST_ASSERT(strstr(json, "\"version\":\"1.2\"") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"statusText\":\"Created\"") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"_clambhook_redacted\":true") != NULL);
    free(json);

    json = ch_developer_clear_json(manager, &error);
    CH_TEST_ASSERT_STRING("{\"cleared\":true}", json);
    free(json);
    json = ch_developer_status_json(manager, &error);
    CH_TEST_ASSERT(strstr(json, "\"capture_count\":0") != NULL);
    free(json);

    static const char disabled_document[] =
        "active = \"default\"\n"
        "[developer]\n"
        "enabled = false\n"
        "[[profile]]\n"
        "name = \"default\"\n";
    ch_config *disabled = NULL;
    CH_TEST_ASSERT(ch_config_parse(disabled_document, NULL, &disabled,
                                   &error) == CH_OK);
    CH_TEST_ASSERT(ch_developer_manager_configure(manager, disabled, &error) ==
                   CH_OK);
    CH_TEST_ASSERT(ch_developer_capture_begin(manager, &metadata, &error) ==
                   NULL);
    CH_TEST_ASSERT(ch_developer_send_json(
        manager, "{\"url\":\"https://example.com\"}", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_STATE);

    ch_config_free(disabled);
    ch_developer_manager_destroy(manager);
    ch_config_free(config);

    char ca_cert[256];
    char ca_key[256];
    char local_path[256];
    (void)snprintf(ca_cert, sizeof(ca_cert),
                   "/tmp/clambhook-native-developer-ca-%ld.pem",
                   (long)getpid());
    (void)snprintf(ca_key, sizeof(ca_key),
                   "/tmp/clambhook-native-developer-ca-%ld-key.pem",
                   (long)getpid());
    (void)snprintf(local_path, sizeof(local_path),
                   "/tmp/clambhook-native-developer-map-%ld.txt",
                   (long)getpid());
    FILE *local = fopen(local_path, "wb");
    CH_TEST_ASSERT(local != NULL);
    CH_TEST_ASSERT(fwrite("local body", 1U, 10U, local) == 10U);
    CH_TEST_ASSERT(fclose(local) == 0);
    char tooling_document[4096];
    int tooling_length = snprintf(
        tooling_document, sizeof(tooling_document),
        "active = \"default\"\n"
        "[developer]\n"
        "enabled = true\n"
        "mitm_enabled = true\n"
        "no_cache_enabled = true\n"
        "ca_cert_path = \"%s\"\n"
        "ca_key_path = \"%s\"\n"
        "ssl_decrypt_hosts = [\"example.com\", \"*.allowed.test\"]\n"
        "[[developer.map_rule]]\n"
        "id = \"local\"\n"
        "enabled = true\n"
        "kind = \"local\"\n"
        "local_path = \"%s\"\n"
        "[developer.map_rule.match]\n"
        "methods = [\"GET\"]\n"
        "host = \"local.example\"\n"
        "[[developer.map_rule]]\n"
        "id = \"remote\"\n"
        "enabled = true\n"
        "kind = \"remote\"\n"
        "remote_url = \"https://mirror.example/v2\"\n"
        "[developer.map_rule.match]\n"
        "path_prefix = \"/api\"\n"
        "[[developer.breakpoint_rule]]\n"
        "id = \"bp\"\n"
        "enabled = true\n"
        "stage = \"request\"\n"
        "[developer.breakpoint_rule.match]\n"
        "host = \"break.example\"\n"
        "[[developer.rewrite_rule]]\n"
        "id = \"rw\"\n"
        "enabled = true\n"
        "stage = \"both\"\n"
        "[developer.rewrite_rule.match]\n"
        "host = \"rewrite.example\"\n"
        "[[developer.rewrite_rule.op]]\n"
        "target = \"header\"\n"
        "action = \"set\"\n"
        "field = \"X-Rewritten\"\n"
        "value = \"yes\"\n"
        "[[developer.rewrite_rule.op]]\n"
        "target = \"body\"\n"
        "action = \"replace\"\n"
        "value = \"old\"\n"
        "replace = \"new\"\n"
        "[[profile]]\n"
        "name = \"default\"\n",
        ca_cert, ca_key, local_path);
    CH_TEST_ASSERT(tooling_length > 0 &&
                   (size_t)tooling_length < sizeof(tooling_document));
    ch_config *tooling_config = NULL;
    CH_TEST_ASSERT(ch_config_parse(tooling_document, NULL, &tooling_config,
                                   &error) == CH_OK);
    ch_developer_manager *tooling = ch_developer_manager_create(&error);
    CH_TEST_ASSERT(tooling != NULL);
    CH_TEST_ASSERT(ch_developer_manager_configure(
                       tooling, tooling_config, &error) == CH_OK);
    json = ch_developer_status_json(tooling, &error);
    CH_TEST_ASSERT(json != NULL);
    CH_TEST_ASSERT(strstr(json, "\"mitm_enabled\":true") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"ca_fingerprint_sha256\":") != NULL);
    free(json);
    size_t ca_length = 0U;
    char *ca = ch_developer_ca_pem(tooling, &ca_length, &error);
    CH_TEST_ASSERT(ca != NULL && ca_length > 0U);
    CH_TEST_ASSERT(strstr(ca, "BEGIN CERTIFICATE") != NULL);
    free(ca);
    CH_TEST_ASSERT(ch_developer_should_mitm(tooling, "example.com"));
    CH_TEST_ASSERT(ch_developer_should_mitm(tooling, "api.allowed.test"));
    CH_TEST_ASSERT(!ch_developer_should_mitm(tooling, "other.example"));
    SSL_CTX *tls_context = NULL;
    CH_TEST_ASSERT(ch_developer_tls_server_context(
                       tooling, "example.com", &tls_context, &error) ==
                   CH_OK);
    CH_TEST_ASSERT(tls_context != NULL);
    SSL_CTX_free(tls_context);
    json = ch_developer_regenerate_ca_json(tooling, &error);
    CH_TEST_ASSERT(json != NULL);
    CH_TEST_ASSERT(strstr(json, "\"ca_fingerprint_sha256\":") != NULL);
    free(json);

    ch_developer_http_header rewrite_headers[] = {
        {.name = "X-Rewritten", .value = "no"},
        {.name = "If-None-Match", .value = "stale"}
    };
    static const uint8_t rewrite_body[] = "old body old";
    ch_developer_http_message rewrite_request = {
        .method = "POST",
        .url = "http://rewrite.example/api",
        .host = "rewrite.example",
        .path = "/api",
        .headers = rewrite_headers,
        .header_count = 2U,
        .body = (uint8_t *)rewrite_body,
        .body_length = sizeof(rewrite_body) - 1U,
        .body_set = true
    };
    ch_developer_http_result transformed;
    CH_TEST_ASSERT(ch_developer_process_request(
                       tooling, &rewrite_request, &transformed, &error) ==
                   CH_OK);
    CH_TEST_ASSERT(transformed.message.body_length == 12U);
    CH_TEST_ASSERT(memcmp(transformed.message.body, "new body new", 12U) == 0);
    CH_TEST_ASSERT(transformed.message.header_count >= 3U);
    ch_developer_http_result_clear(&transformed);

    ch_developer_http_message local_request = {
        .method = "GET",
        .url = "http://local.example/file",
        .host = "local.example",
        .path = "/file"
    };
    CH_TEST_ASSERT(ch_developer_process_request(
                       tooling, &local_request, &transformed, &error) ==
                   CH_OK);
    CH_TEST_ASSERT(transformed.local_response);
    CH_TEST_ASSERT(transformed.message.status == 200);
    CH_TEST_ASSERT(transformed.message.body_length == 10U);
    CH_TEST_ASSERT(memcmp(transformed.message.body, "local body", 10U) == 0);
    ch_developer_http_result_clear(&transformed);

    ch_developer_http_message remote_request = {
        .method = "POST",
        .url = "http://origin.example/api/users?q=1",
        .host = "origin.example",
        .path = "/api/users?q=1"
    };
    CH_TEST_ASSERT(ch_developer_process_request(
                       tooling, &remote_request, &transformed, &error) ==
                   CH_OK);
    CH_TEST_ASSERT(transformed.matched);
    CH_TEST_ASSERT_STRING("https://mirror.example/v2/users?q=1",
                          transformed.message.url);
    ch_developer_http_result_clear(&transformed);

    ch_developer_http_message breakpoint_request = {
        .method = "GET",
        .url = "http://break.example/api",
        .host = "break.example",
        .path = "/api"
    };
    developer_breakpoint_test breakpoint_test = {.manager = tooling};
    pthread_t resolver;
    CH_TEST_ASSERT(pthread_create(&resolver, NULL,
                                  developer_breakpoint_resolver,
                                  &breakpoint_test) == 0);
    CH_TEST_ASSERT(ch_developer_process_request(
                       tooling, &breakpoint_request, &transformed, &error) ==
                   CH_OK);
    CH_TEST_ASSERT(pthread_join(resolver, NULL) == 0);
    CH_TEST_ASSERT(breakpoint_test.resolved);
    CH_TEST_ASSERT(transformed.drop);
    ch_developer_http_result_clear(&transformed);

    ch_developer_manager_destroy(tooling);
    ch_config_free(tooling_config);
    (void)unlink(ca_cert);
    (void)unlink(ca_key);
    (void)unlink(local_path);
}
