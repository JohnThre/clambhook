// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <stdlib.h>
#include <string.h>

#include "clambhook/config.h"
#include "clambhook/json.h"
#include "internal.h"
#include "profile_converter.h"

static char *converter_request(const char *format, const char *name,
                               const char *source, const char *sha,
                               int activate) {
    ch_json_buffer json;
    ch_json_init(&json);
    int okay = ch_json_append(&json, "{\"format\":") &&
        ch_json_append_string(&json, format) &&
        ch_json_append(&json, ",\"profile_name\":") &&
        ch_json_append_string(&json, name) &&
        ch_json_append(&json, ",\"source\":") &&
        ch_json_append_string(&json, source);
    if (okay && sha != NULL) {
        okay = ch_json_append(&json, ",\"expected_sha256\":") &&
            ch_json_append_string(&json, sha) &&
            ch_json_append_format(&json, ",\"activate\":%s",
                                  activate != 0 ? "true" : "false");
    }
    if (okay) okay = ch_json_append(&json, "}");
    char *result = okay ? ch_json_take(&json) : NULL;
    ch_json_dispose(&json);
    return result;
}

static void test_mihomo_review_and_merge(void) {
    static const char source[] =
        "proxies:\n"
        "  - name: Office SS\n"
        "    type: ss\n"
        "    server: proxy.example.com\n"
        "    port: 8388\n"
        "    cipher: aes-256-gcm\n"
        "    password: private-value\n"
        "  - name: Unsupported\n"
        "    type: hysteria2\n"
        "    server: unsupported.example\n"
        "    port: 443\n"
        "proxy-groups:\n"
        "  - name: Choose\n"
        "    type: select\n"
        "    proxies: [Office SS, DIRECT]\n"
        "rules:\n"
        "  - DOMAIN-SUFFIX,example.com,Choose\n"
        "  - MATCH,DIRECT\n";
    ch_error error;
    char *request = converter_request("mihomo", "Imported", source, NULL, 0);
    char *review = NULL;
    CH_TEST_ASSERT(ch_profile_converter_review_request_json(
        request, &review, &error) == CH_OK);
    CH_TEST_ASSERT(strstr(review, "\"format\":\"mihomo\"") != NULL);
    CH_TEST_ASSERT(strstr(review, "unsupported") != NULL);
    CH_TEST_ASSERT(strstr(review, "private-value") != NULL); /* TOML export is sensitive. */
    ch_json_value *root = ch_json_parse(review, strlen(review), &error);
    const char *sha = ch_json_string_value(ch_json_object_get(root, "sha256"));
    CH_TEST_ASSERT(sha != NULL && strlen(sha) == 64U);
    char *import_request = converter_request("mihomo", "Imported", source, sha, 1);
    static const char current_toml[] =
        "active = \"existing\"\n[[profile]]\nname = \"existing\"\n"
        "[[profile.chain]]\nname = \"direct\"\n"
        "[[profile.chain.server]]\nname = \"direct\"\nprotocol = \"direct\"\n";
    ch_config *current = NULL;
    CH_TEST_ASSERT(ch_config_parse(current_toml, NULL, &current, &error) == CH_OK);
    char *merged = NULL, *result = NULL;
    CH_TEST_ASSERT(ch_profile_converter_import_request_json(
        current, import_request, &merged, &result, &error) == CH_OK);
    CH_TEST_ASSERT(strstr(merged, "active = \"Imported\"") != NULL);
    CH_TEST_ASSERT(strstr(merged, "name = \"existing\"") != NULL);
    CH_TEST_ASSERT(strstr(merged, "name = \"Imported\"") != NULL);
    ch_config *validated = NULL;
    CH_TEST_ASSERT(ch_config_parse(merged, NULL, &validated, &error) == CH_OK);
    ch_config_free(validated);
    free(result); free(merged); ch_config_free(current);
    free(import_request); ch_json_value_destroy(root); free(review); free(request);
}

static void test_surge_review_is_secret_safe_on_failure(void) {
    static const char source[] =
        "[Proxy]\n"
        "Home = ss, 2001:db8::1, 443, encrypt-method=chacha20-ietf-poly1305, password=surge-private\n"
        "Legacy = snell, legacy.example, 443, psk=must-not-leak\n"
        "[Proxy Group]\n"
        "Auto = smart, Home, Legacy, url=https://www.gstatic.com/generate_204\n"
        "[Rule]\n"
        "PROCESS-NAME,curl,Home\n"
        "FINAL,DIRECT\n";
    ch_error error;
    char *request = converter_request("auto", "Surge Import", source, NULL, 0);
    char *review = NULL;
    CH_TEST_ASSERT(ch_profile_converter_review_request_json(request, &review,
                                                            &error) == CH_OK);
    CH_TEST_ASSERT(strstr(review, "\"format\":\"surge\"") != NULL);
    CH_TEST_ASSERT(strstr(review, "group was converted to url-test") != NULL);
    CH_TEST_ASSERT(strstr(review, "must-not-leak") == NULL);
    free(review); free(request);

    request = converter_request("surge", "Broken",
        "[Proxy]\nBad = snell, host, 443, psk=secret-never-log\n", NULL, 0);
    CH_TEST_ASSERT(ch_profile_converter_review_request_json(
        request, &review, &error) == CH_ERROR_UNSUPPORTED);
    CH_TEST_ASSERT(strstr(error.message, "secret-never-log") == NULL);
    free(request);
}

static void test_mihomo_anchors_wireguard_groups_and_stale_review(void) {
    static const char source[] =
        "defaults: &wg\n"
        "  type: wireguard\n"
        "  server: 2001:db8::5\n"
        "  port: 51820\n"
        "  private-key: AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=\n"
        "  public-key: AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=\n"
        "  ip: 10.10.0.2/32\n"
        "proxies:\n"
        "  - <<: *wg\n"
        "    name: Tunnel\n"
        "proxy-groups:\n"
        "  - name: Inner\n"
        "    type: select\n"
        "    proxies: [Tunnel]\n"
        "  - name: Outer\n"
        "    type: select\n"
        "    proxies: [Inner, DIRECT]\n"
        "rule-providers:\n"
        "  local-domains:\n"
        "    behavior: domain\n"
        "    payload: [+.example.org, exact.example]\n"
        "rules:\n"
        "  - RULE-SET,local-domains,Outer\n"
        "  - IP-CIDR6,2001:db8::/32,Outer\n"
        "  - DST-PORT,443,Tunnel\n";
    ch_error error;
    char *request = converter_request("mihomo", "Anchored", source, NULL, 0);
    char *review = NULL;
    CH_TEST_ASSERT(ch_profile_converter_review_request_json(
        request, &review, &error) == CH_OK);
    CH_TEST_ASSERT(strstr(review, "protocol = \\\"wireguard\\\"") != NULL);
    CH_TEST_ASSERT(strstr(review, "[\\\"Tunnel\\\", \\\"DIRECT\\\"]") != NULL);
    CH_TEST_ASSERT(strstr(review, "example.org") != NULL);
    ch_json_value *root = ch_json_parse(review, strlen(review), &error);
    const char *sha = ch_json_string_value(ch_json_object_get(root, "sha256"));
    ch_config *current = NULL;
    CH_TEST_ASSERT(ch_config_parse(
        "active = \"base\"\n[[profile]]\nname = \"base\"\n"
        "[[profile.chain]]\nname = \"direct\"\n"
        "[[profile.chain.server]]\nprotocol = \"direct\"\n",
        NULL, &current, &error) == CH_OK);
    char stale[65];
    memset(stale, '0', 64U); stale[64] = '\0';
    char *import_request = converter_request(
        "mihomo", "Anchored", source, stale, 0);
    char *merged = NULL, *result = NULL;
    CH_TEST_ASSERT(ch_profile_converter_import_request_json(
        current, import_request, &merged, &result, &error) ==
        CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "changed after review") != NULL);
    CH_TEST_ASSERT(sha != NULL);
    free(import_request); ch_config_free(current);
    ch_json_value_destroy(root); free(review); free(request);

    request = converter_request("mihomo", "Duplicate",
        "proxies:\n  - name: one\n    name: two\n    type: ss\n", NULL, 0);
    CH_TEST_ASSERT(ch_profile_converter_review_request_json(
        request, &review, &error) == CH_ERROR_PARSE);
    CH_TEST_ASSERT(strstr(error.message, "duplicate key") != NULL);
    free(request);
}

void ch_test_profile_converter(void) {
    test_mihomo_review_and_merge();
    test_surge_review_is_secret_safe_on_failure();
    test_mihomo_anchors_wireguard_groups_and_stale_review();
}
