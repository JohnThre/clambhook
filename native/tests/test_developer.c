#include "test.h"

#include <stdlib.h>
#include <string.h>

#include "clambhook/config.h"
#include "clambhook/developer.h"

void ch_test_developer(void) {
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
    ch_error error;
    ch_config *config = NULL;
    CH_TEST_ASSERT(ch_config_parse(document, "/tmp/developer.toml", &config,
                                   &error) == CH_OK);
    ch_developer_manager *manager = ch_developer_manager_create(&error);
    CH_TEST_ASSERT(manager != NULL);
    CH_TEST_ASSERT(ch_developer_manager_configure(manager, config, &error) ==
                   CH_OK);

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
    static const uint8_t response_a[] = "HTTP/1.1 201 Cr";
    static const uint8_t response_b[] =
        "eated\r\nContent-Type: application/json\r\n"
        "Set-Cookie: session=secret\r\n\r\n{\"ok\":true}";
    ch_developer_capture_response(capture, response_a,
                                  sizeof(response_a) - 1U);
    ch_developer_capture_response(capture, response_b,
                                  sizeof(response_b) - 1U);
    ch_developer_capture_finish(capture, NULL);

    char *json = ch_developer_status_json(manager, &error);
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

    ch_config_free(disabled);
    ch_developer_manager_destroy(manager);
    ch_config_free(config);
}
