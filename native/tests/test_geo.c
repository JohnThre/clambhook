// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <stdlib.h>

#include "clambhook/config.h"
#include "geo.h"
#include "internal.h"

static void geo_config(const char *database, ch_config **out_config) {
    const char *prefix = "[geo]\ndatabase = \"";
    const char *suffix = "\"\n[[profile]]\nname = \"default\"\n";
    size_t length = strlen(prefix) + strlen(database) + strlen(suffix) + 1U;
    char *document = malloc(length);
    CH_TEST_ASSERT(document != NULL);
    (void)snprintf(document, length, "%s%s%s", prefix, database, suffix);
    *out_config = NULL;
    ch_error error;
    CH_TEST_ASSERT(ch_config_parse(document, NULL, out_config, &error) ==
                   CH_OK);
    free(document);
}

static void test_geo_disabled(void) {
    ch_config *config = NULL;
    geo_config("", &config);
    CH_TEST_ASSERT(config != NULL);
    ch_geo_reader *reader = (ch_geo_reader *)1;
    ch_error error;
    CH_TEST_ASSERT(ch_geo_reader_open_config(config, &reader, &error) == CH_OK);
    CH_TEST_ASSERT(reader == NULL);
    ch_geo_location location;
    CH_TEST_ASSERT(ch_geo_lookup(NULL, "81.2.69.142", &location, &error) ==
                   CH_OK);
    CH_TEST_ASSERT(location.country == NULL);
    ch_geo_location_clear(&location);
    ch_config_free(config);
}

static void test_geo_city_lookup(void) {
    ch_config *config = NULL;
    geo_config(CLAMBHOOK_SOURCE_DIR
               "/third_party/libmaxminddb/testdata/GeoIP2-City-Test.mmdb", &config);
    CH_TEST_ASSERT(config != NULL);
    ch_geo_reader *reader = NULL;
    ch_error error;
    CH_TEST_ASSERT(ch_geo_reader_open_config(config, &reader, &error) == CH_OK);
    CH_TEST_ASSERT(reader != NULL);
    ch_geo_location location;
    CH_TEST_ASSERT(ch_geo_lookup(reader, "81.2.69.142:443", &location,
                                 &error) == CH_OK);
    CH_TEST_ASSERT_STRING("United Kingdom", location.country);
    CH_TEST_ASSERT_STRING("GB", location.country_code);
    CH_TEST_ASSERT_STRING("London", location.city);
    CH_TEST_ASSERT(location.latitude != 0.0);
    CH_TEST_ASSERT(location.longitude != 0.0);
    ch_geo_location_clear(&location);

    CH_TEST_ASSERT(ch_geo_lookup(reader, "203.0.113.1", &location, &error) ==
                   CH_OK);
    CH_TEST_ASSERT(location.country == NULL);
    CH_TEST_ASSERT(location.country_code == NULL);
    CH_TEST_ASSERT(location.city == NULL);
    ch_geo_location_clear(&location);
    ch_geo_reader_release(reader);
    ch_config_free(config);
}

static void test_geo_country_lookup(void) {
    ch_config *config = NULL;
    geo_config(CLAMBHOOK_SOURCE_DIR
               "/third_party/libmaxminddb/testdata/GeoIP2-Country-Test.mmdb", &config);
    CH_TEST_ASSERT(config != NULL);
    ch_geo_reader *reader = NULL;
    ch_error error;
    CH_TEST_ASSERT(ch_geo_reader_open_config(config, &reader, &error) == CH_OK);
    ch_geo_location location;
    CH_TEST_ASSERT(ch_geo_lookup(reader, "81.2.69.142", &location, &error) ==
                   CH_OK);
    CH_TEST_ASSERT_STRING("United Kingdom", location.country);
    CH_TEST_ASSERT_STRING("GB", location.country_code);
    CH_TEST_ASSERT_STRING("", location.city);
    CH_TEST_ASSERT(location.latitude == 0.0);
    CH_TEST_ASSERT(location.longitude == 0.0);
    ch_geo_location_clear(&location);
    ch_geo_reader_release(reader);
    ch_config_free(config);
}

static void test_geo_invalid_database_and_address(void) {
    ch_config *config = NULL;
    geo_config(CLAMBHOOK_SOURCE_DIR
               "/third_party/libmaxminddb/testdata/does-not-exist.mmdb", &config);
    CH_TEST_ASSERT(config != NULL);
    ch_geo_reader *reader = NULL;
    ch_error error;
    CH_TEST_ASSERT(ch_geo_reader_open_config(config, &reader, &error) ==
                   CH_ERROR_IO);
    CH_TEST_ASSERT(reader == NULL);
    ch_config_free(config);

    geo_config(CLAMBHOOK_SOURCE_DIR
               "/third_party/libmaxminddb/testdata/GeoIP2-City-Test.mmdb", &config);
    CH_TEST_ASSERT(config != NULL);
    CH_TEST_ASSERT(ch_geo_reader_open_config(config, &reader, &error) == CH_OK);
    ch_geo_location location;
    CH_TEST_ASSERT(ch_geo_lookup(reader, "", &location, &error) ==
                   CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "empty address") != NULL);
    ch_geo_reader_release(reader);
    ch_config_free(config);
}

static void test_geo_server_inventory_contract(void) {
    static const char document[] =
        "[geo]\n"
        "database = \"" CLAMBHOOK_SOURCE_DIR
        "/third_party/libmaxminddb/testdata/GeoIP2-City-Test.mmdb\"\n"
        "[[profile]]\n"
        "name = \"default\"\n"
        "[[profile.chain]]\n"
        "name = \"primary\"\n"
        "[[profile.chain.server]]\n"
        "name = \"london\"\n"
        "address = \"81.2.69.142:443\"\n"
        "protocol = \"trojan\"\n";
    ch_config *config = NULL;
    ch_error error;
    CH_TEST_ASSERT(ch_config_parse(document, NULL, &config, &error) == CH_OK);
    char *json = ch_config_servers_payload_json(
        config, "default", &error);
    CH_TEST_ASSERT(json != NULL);
    CH_TEST_ASSERT(strstr(json,
        "\"capabilities\":{\"tcp\":true,\"udp\":true,"
        "\"udp_mode\":\"stream\"}") != NULL);
    CH_TEST_ASSERT(strstr(json,
        "\"geo\":{\"country\":\"United Kingdom\","
        "\"country_code\":\"GB\",\"city\":\"London\"") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"geo_error\"") == NULL);
    free(json);
    ch_config_free(config);
}

void ch_test_geo(void) {
    test_geo_disabled();
    test_geo_city_lookup();
    test_geo_country_lookup();
    test_geo_invalid_database_and_address();
    test_geo_server_inventory_contract();
}
