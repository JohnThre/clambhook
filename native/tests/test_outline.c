// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <stdlib.h>
#include <string.h>

#include "clambhook/config.h"
#include "internal.h"
#include "outline.h"

static const char *outline_placeholder_config =
    "active = \"default\"\n"
    "[[profile]]\n"
    "name = \"default\"\n"
    "[[profile.chain]]\n"
    "name = \"main\"\n"
    "[[profile.chain.server]]\n"
    "name = \"replace-me\"\n"
    "address = \"proxy.example.com:443\"\n"
    "protocol = \"shadowsocks\"\n"
    "[profile.chain.server.settings]\n"
    "method = \"aes-256-gcm\"\n"
    "password = \"replace-me\"\n";

static void test_outline_static_review(void) {
    ch_error error;
    char *json = NULL;
    ch_status status = ch_outline_review_request_json(
        "{\"access_key\":\"ss://YWVzLTI1Ni1nY206c2VjcmV0@[2001:db8::1]:8388/?outline=1&prefix=%C3%BF%00#My%20VPN\"}",
        &json, &error);
    if (status != CH_OK) fprintf(stderr, "Outline review: %s\n", error.message);
    CH_TEST_ASSERT(status == CH_OK);
    CH_TEST_ASSERT(strstr(json, "secret") == NULL);
    CH_TEST_ASSERT(strstr(json, "ss://") == NULL);
    CH_TEST_ASSERT(strstr(json, "\"suggested_name\":\"My VPN\"") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"address\":\"[2001:db8::1]:8388\"") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"cipher\":\"aes-256-gcm\"") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"prefix_length\":2") != NULL);
    free(json);

    json = NULL;
    CH_TEST_ASSERT(ch_outline_review_request_json(
        "{\"access_key\":\"ss://YWVzLTI1Ni1nY206c2VjcmV0QGV4YW1wbGUuY29tOjgzODg=#Legacy\"}",
        &json, &error) == CH_OK);
    CH_TEST_ASSERT(strstr(json, "example.com:8388") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"suggested_name\":\"Legacy\"") != NULL);
    free(json);
}

static void test_outline_rejections_are_secret_safe(void) {
    ch_error error;
    char *json = NULL;
    CH_TEST_ASSERT(ch_outline_review_request_json(
        "{\"access_key\":\"ss://aes-256-gcm:do-not-leak@example.com:8388?plugin=v2ray\"}",
        &json, &error) == CH_ERROR_UNSUPPORTED);
    CH_TEST_ASSERT(strstr(error.message, "do-not-leak") == NULL);
    CH_TEST_ASSERT(ch_outline_review_request_json(
        "{\"access_key\":\"ss://rc4-md5:do-not-leak@example.com:8388\"}",
        &json, &error) != CH_OK);
    CH_TEST_ASSERT(strstr(error.message, "do-not-leak") == NULL);
    CH_TEST_ASSERT(ch_outline_review_request_json(
        "{\"access_key\":\"http://example.com/key\"}",
        &json, &error) == CH_ERROR_UNSUPPORTED);
    CH_TEST_ASSERT(ch_outline_review_request_json(
        "{\"access_key\":\"ss://%%%%\"}", &json,
        &error) == CH_ERROR_PARSE);
    CH_TEST_ASSERT(ch_outline_review_request_json(
        "{\"access_key\":\"ssconf://127.0.0.1/private-token\"}",
        &json, &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "private-token") == NULL);
    CH_TEST_ASSERT(ch_outline_review_request_json(
        "{\"access_key\":\"ssconf://user:secret@example.com/key\"}",
        &json, &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "secret") == NULL);

    CH_TEST_ASSERT(ch_outline_review_request_json(
        "{\"access_key\":\"https://invite.example/#ss%3A%2F%2Faes-128-gcm%3Awrapped%40example.com%3A443%23Invite\"}",
        &json, &error) == CH_OK);
    CH_TEST_ASSERT(strstr(json, "wrapped") == NULL);
    CH_TEST_ASSERT(strstr(json, "Invite") != NULL);
    free(json);
}

static void test_outline_dynamic_documents(void) {
    ch_error error;
    char *json = NULL;
    CH_TEST_ASSERT(ch_outline_review_document_json(
        "{\"server\":\"198.51.100.8\",\"server_port\":8443,"
        "\"method\":\"chacha20-ietf-poly1305\",\"password\":\"json-secret\","
        "\"prefix\":\"POST \"}", &json, &error) == CH_OK);
    CH_TEST_ASSERT(strstr(json, "json-secret") == NULL);
    CH_TEST_ASSERT(strstr(json, "198.51.100.8:8443") != NULL);
    CH_TEST_ASSERT(strstr(json, "\"prefix_length\":5") != NULL);
    free(json);

    static const char yaml[] =
        "defaults: &ss\n"
        "  $type: shadowsocks\n"
        "  cipher: aes-256-gcm\n"
        "transport:\n"
        "  $type: tcpudp\n"
        "  tcp:\n"
        "    <<: *ss\n"
        "    endpoint: tcp.example:443\n"
        "    secret: tcp-secret\n"
        "    prefix: 'GET '\n"
        "  udp:\n"
        "    <<: *ss\n"
        "    endpoint: udp.example:8443\n"
        "    secret: udp-secret\n"
        "    prefix: \"\\0\\xFF\"\n";
    CH_TEST_ASSERT(ch_outline_review_document_json(yaml, &json,
                                                    &error) == CH_OK);
    CH_TEST_ASSERT(strstr(json, "tcp-secret") == NULL);
    CH_TEST_ASSERT(strstr(json, "udp-secret") == NULL);
    CH_TEST_ASSERT(strstr(json, "tcp.example:443") != NULL);
    CH_TEST_ASSERT(strstr(json, "udp.example:8443") != NULL);
    free(json);

    CH_TEST_ASSERT(ch_outline_review_document_json(
        "server: one.example\nserver: two.example\nserver_port: 443\n"
        "method: aes-128-gcm\npassword: secret\n", &json,
        &error) == CH_ERROR_PARSE);
    CH_TEST_ASSERT(strstr(error.message, "secret") == NULL);
    CH_TEST_ASSERT(ch_outline_review_document_json(
        "transport:\n  $type: first-supported\n  options: []\n",
        &json, &error) == CH_ERROR_UNSUPPORTED);
}

static void test_outline_import_replaces_placeholder(void) {
    ch_error error;
    ch_config *config = NULL;
    char *mutation = NULL;
    char *toml = NULL;
    CH_TEST_ASSERT(ch_config_parse(outline_placeholder_config,
                                   "/tmp/outline.toml", &config,
                                   &error) == CH_OK);
    CH_TEST_ASSERT(ch_outline_import_mutation_request_json(
        "{\"access_key\":\"ss://aes-128-gcm:static-secret@example.com:443#Office\",\"profile_name\":\"Office\",\"activate\":true}",
        &mutation, &error) == CH_OK);
    ch_status status = ch_config_mutate_document_json(
        config, "default", "import_outline", mutation, &toml,
        &error);
    if (status != CH_OK) fprintf(stderr, "Outline import: %s\n", error.message);
    CH_TEST_ASSERT(status == CH_OK);
    CH_TEST_ASSERT(strstr(toml, "active = \"Office\"") != NULL);
    CH_TEST_ASSERT(strstr(toml, "name = \"replace-me\"") == NULL);
    CH_TEST_ASSERT(strstr(toml, "method = \"aes-128-gcm\"") != NULL);
    CH_TEST_ASSERT(strstr(toml, "password = \"static-secret\"") != NULL);
    CH_TEST_ASSERT(strstr(toml, "outline_dynamic_key") == NULL);
    CH_TEST_ASSERT(strstr(toml, "ss://") == NULL);
    ch_config *imported = NULL;
    CH_TEST_ASSERT(ch_config_parse(toml, "/tmp/outline-imported.toml",
                                   &imported, &error) == CH_OK);
    char *duplicate = NULL;
    CH_TEST_ASSERT(ch_config_mutate_document_json(
        imported, "Office", "import_outline", mutation, &duplicate,
        &error) == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(duplicate == NULL);
    CH_TEST_ASSERT(strstr(error.message, "static-secret") == NULL);
    ch_config_free(imported);
    free(toml);
    free(mutation);
    ch_config_free(config);
}

void ch_test_outline(void) {
    test_outline_static_review();
    test_outline_rejections_are_secret_safe();
    test_outline_dynamic_documents();
    test_outline_import_replaces_placeholder();
}
