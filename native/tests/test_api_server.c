// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <stdlib.h>

#include "api_server.h"

void ch_test_api_server(void) {
    ch_error error;
    CH_TEST_ASSERT(ch_api_is_loopback_host("localhost"));
    CH_TEST_ASSERT(ch_api_is_loopback_host("LOCALHOST:9090"));
    CH_TEST_ASSERT(ch_api_is_loopback_host("127.0.0.1"));
    CH_TEST_ASSERT(ch_api_is_loopback_host("127.255.1.9:9090"));
    CH_TEST_ASSERT(ch_api_is_loopback_host("[::1]"));
    CH_TEST_ASSERT(ch_api_is_loopback_host("[0:0:0:0:0:0:0:1]:9090"));

    CH_TEST_ASSERT(!ch_api_is_loopback_host("127.example.com"));
    CH_TEST_ASSERT(!ch_api_is_loopback_host("127.0.0.1.example.com"));
    CH_TEST_ASSERT(!ch_api_is_loopback_host("localhost.example.com"));
    CH_TEST_ASSERT(!ch_api_is_loopback_host("[::1].example.com"));
    CH_TEST_ASSERT(!ch_api_is_loopback_host("127.0.0.1:bad"));
    CH_TEST_ASSERT(!ch_api_is_loopback_host("127.0.0.1:65536"));
    CH_TEST_ASSERT(!ch_api_is_loopback_host("127.0.0.1/path"));
    CH_TEST_ASSERT(!ch_api_is_loopback_host("[::1]:bad"));
    CH_TEST_ASSERT(!ch_api_is_loopback_host("0.0.0.0:9090"));
    CH_TEST_ASSERT(!ch_api_is_loopback_host("192.168.1.2"));

    char *request = ch_api_profile_request_json(
        "/api/v1/rules?state=active&profile=work%20vpn", &error);
    CH_TEST_ASSERT(request != NULL);
    CH_TEST_ASSERT_STRING("{\"profile\":\"work vpn\"}", request);
    free(request);
    request = ch_api_profile_request_json(
        "/api/v1/rules?profile=first&profile=second", &error);
    CH_TEST_ASSERT(request != NULL);
    CH_TEST_ASSERT_STRING("{\"profile\":\"first\"}", request);
    free(request);
    request = ch_api_profile_request_json("/api/v1/rules", &error);
    CH_TEST_ASSERT(request != NULL);
    CH_TEST_ASSERT_STRING("{}", request);
    free(request);
    CH_TEST_ASSERT(ch_api_profile_request_json(
        "/api/v1/rules?profile=bad%2", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_PARSE);

    request = ch_api_traffic_request_json(
        "/api/v1/traffic?action=block&domain=ads.example&limit=0&offset=2",
        &error);
    CH_TEST_ASSERT(request != NULL);
    CH_TEST_ASSERT_STRING(
        "{\"action\":\"block\",\"domain\":\"ads.example\","
        "\"limit\":0,\"offset\":2}", request);
    free(request);
    request = ch_api_traffic_request_json(
        "/api/v1/traffic?query=hello+world&network=udp&unknown=ignored",
        &error);
    CH_TEST_ASSERT(request != NULL);
    CH_TEST_ASSERT_STRING(
        "{\"query\":\"hello world\",\"network\":\"udp\"}",
        request);
    free(request);
    CH_TEST_ASSERT(ch_api_traffic_request_json(
        "/api/v1/traffic?limit=-1", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);

    request = ch_api_developer_entries_request_json(
        "/api/v1/developer/entries?method=GET,post&status_min=200&"
        "status_max=399&host=api.example&scheme=https&content_type=json&"
        "q=hello+world&error_only=yes&limit=25&ignored=true",
        &error);
    CH_TEST_ASSERT(request != NULL);
    CH_TEST_ASSERT_STRING(
        "{\"methods\":[\"GET\",\"post\"],\"status_min\":200,"
        "\"status_max\":399,\"host\":\"api.example\","
        "\"scheme\":\"https\",\"content_type\":\"json\","
        "\"query\":\"hello world\",\"error_only\":true,\"limit\":25}",
        request);
    free(request);
    CH_TEST_ASSERT(ch_api_developer_entries_request_json(
        "/api/v1/developer/entries?error_only=maybe", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);

    request = ch_api_events_request_json(
        "/api/v1/events?types=connection.*,hop.connected&conn_id=a&"
        "conn_id=b,c&since=1:42,3:7&ignored=yes", &error);
    CH_TEST_ASSERT(request != NULL);
    CH_TEST_ASSERT_STRING(
        "{\"types\":[\"connection.*\",\"hop.connected\"],"
        "\"conn_ids\":[\"a\",\"b\",\"c\"],\"since\":["
        "{\"shard_id\":1,\"lamport\":42},"
        "{\"shard_id\":3,\"lamport\":7}]}", request);
    free(request);
    CH_TEST_ASSERT(ch_api_events_request_json(
        "/api/v1/events?since=bad", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
}
