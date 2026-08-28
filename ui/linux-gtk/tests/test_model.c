#include "model.h"

#include <glib.h>
#include <string.h>

static void test_status_and_profiles(void) {
    static const guint8 status_json[] =
        "{\"running\":true,\"profile\":\"work\","
        "\"tunnel_mode\":\"tun\",\"listeners\":[{"
        "\"protocol\":\"http\",\"addr\":\"127.0.0.1:8080\","
        "\"active_conns\":2}]}";
    ch_gtk_status_model status;
    g_autoptr(GError) error = NULL;
    g_assert_true(ch_gtk_parse_status(status_json, sizeof(status_json) - 1U,
                                      &status, &error));
    g_assert_true(status.running);
    g_assert_cmpstr(status.profile, ==, "work");
    g_assert_cmpstr(status.mode, ==, "tun");
    g_assert_cmpuint(status.listeners->len, ==, 1U);
    g_assert_nonnull(strstr(g_ptr_array_index(status.listeners, 0U),
                            "2 active"));
    ch_gtk_status_model_clear(&status);

    static const guint8 profiles_json[] =
        "{\"profiles\":[\"home\",\"work\"],\"active\":\"work\"}";
    ch_gtk_profiles_model profiles;
    g_assert_true(ch_gtk_parse_profiles(
        profiles_json, sizeof(profiles_json) - 1U, &profiles, &error));
    g_assert_cmpstr(profiles.active, ==, "work");
    g_assert_cmpuint(profiles.names->len, ==, 2U);
    g_assert_cmpstr(g_ptr_array_index(profiles.names, 0U), ==, "home");
    ch_gtk_profiles_model_clear(&profiles);
}

static void test_traffic(void) {
    static const guint8 json[] =
        "{\"summary\":{\"active_connections\":1,\"rx_bps\":2048,"
        "\"tx_bps\":1024,\"rx_total\":4096,\"tx_total\":512},"
        "\"connections\":[{\"conn_id\":\"conn-1\","
        "\"profile\":\"work\",\"state\":\"active\","
        "\"rule_action\":\"proxy\",\"target\":\"example.com:443\","
        "\"network\":\"tcp\",\"chain_name\":\"secure\","
        "\"rule_name\":\"web\",\"rx_total\":4096,"
        "\"tx_total\":512}]}";
    ch_gtk_traffic_model traffic;
    g_autoptr(GError) error = NULL;
    g_assert_true(ch_gtk_parse_traffic(json, sizeof(json) - 1U,
                                       &traffic, &error));
    g_assert_cmpuint(traffic.active_connections, ==, 1U);
    g_assert_cmpfloat(traffic.rx_bps, ==, 2048.0);
    g_assert_cmpuint(traffic.rows->len, ==, 1U);
    ch_gtk_row *row = g_ptr_array_index(traffic.rows, 0U);
    g_assert_nonnull(strstr(row->title, "PROXY"));
    g_assert_nonnull(strstr(row->detail, "secure"));
    g_assert_cmpstr(row->identifier, ==, "conn-1");
    ch_gtk_traffic_model_clear(&traffic);
}

static void assert_page(ch_gtk_page_model_kind kind, const char *json,
                        const char *title_fragment) {
    GPtrArray *rows = NULL;
    char *summary = NULL;
    g_autoptr(GError) error = NULL;
    g_assert_true(ch_gtk_parse_page_rows(
        kind, (const guint8 *)json, strlen(json), &rows, &summary, &error));
    g_assert_cmpuint(rows->len, ==, 1U);
    ch_gtk_row *row = g_ptr_array_index(rows, 0U);
    g_assert_nonnull(strstr(row->title, title_fragment));
    g_assert_nonnull(summary);
    g_ptr_array_unref(rows);
    g_free(summary);
}

static void test_page_rows(void) {
    assert_page(CH_GTK_PAGE_SERVERS,
        "{\"chains\":[{\"name\":\"main\",\"servers\":[{"
        "\"name\":\"edge\",\"protocol\":\"trojan\","
        "\"address\":\"edge.example:443\"}]}]}", "edge");
    assert_page(CH_GTK_PAGE_RULES,
        "{\"profile\":\"work\",\"rules\":[{\"name\":\"private\","
        "\"action\":\"direct\",\"domain_suffixes\":[\"internal\"],"
        "\"networks\":[\"tcp\",\"udp\"]}]}", "DIRECT");
    assert_page(CH_GTK_PAGE_POLICIES,
        "{\"groups\":[{\"name\":\"fast\",\"type\":\"url-test\","
        "\"active_chain\":\"edge\",\"chains\":[\"edge\"]}]}",
        "fast");
    assert_page(CH_GTK_PAGE_PROMPTS,
        "{\"prompts\":[{\"id\":\"prompt-1\","
        "\"process_name\":\"Browser\",\"target\":\"example.com:443\","
        "\"network\":\"tcp\"}]}", "Browser");
    assert_page(CH_GTK_PAGE_SILENT_DECISIONS,
        "{\"decisions\":[{\"id\":\"silent-1\",\"action\":\"deny\","
        "\"process_name\":\"Updater\",\"target\":\"example.com:443\","
        "\"network\":\"tcp\",\"profile\":\"work\"}]}", "Denied");
    assert_page(CH_GTK_PAGE_DNS,
        "{\"upstreams\":[{\"name\":\"Control D\","
        "\"protocol\":\"doh\",\"url\":\"https://dns.example/dns-query\"}]}",
        "Control D");
    assert_page(CH_GTK_PAGE_CAPTURES,
        "{\"entries\":[{\"id\":\"dev-1\",\"method\":\"GET\","
        "\"url\":\"http://example.com/\",\"status\":200}]}", "GET");
    assert_page(CH_GTK_PAGE_CONDITIONER,
        "{\"enabled\":true,\"latency\":\"20ms\","
        "\"jitter\":\"3ms\",\"loss_percent\":1.5,"
        "\"download_kbps\":2048,\"upload_kbps\":512}", "Enabled");

    GPtrArray *rows = NULL;
    char *summary = NULL;
    g_autoptr(GError) error = NULL;
    const char *conditioner =
        "{\"enabled\":true,\"latency\":\"20ms\","
        "\"jitter\":\"3ms\",\"loss_percent\":1.5,"
        "\"download_kbps\":2048,\"upload_kbps\":512}";
    g_assert_true(ch_gtk_parse_page_rows(
        CH_GTK_PAGE_CONDITIONER, (const guint8 *)conditioner,
        strlen(conditioner), &rows, &summary, &error));
    ch_gtk_row *conditioner_row = g_ptr_array_index(rows, 0U);
    g_assert_nonnull(strstr(conditioner_row->detail, "2048 Kbps down"));
    g_assert_nonnull(strstr(conditioner_row->detail, "512 Kbps up"));
    g_ptr_array_unref(rows);
    g_free(summary);

    rows = NULL;
    summary = NULL;
    const char *policies =
        "{\"groups\":[{\"name\":\"manual\",\"type\":\"select\","
        "\"selected\":\"edge\",\"chains\":[\"direct\",\"edge\"]}]}";
    g_assert_true(ch_gtk_parse_page_rows(
        CH_GTK_PAGE_POLICIES, (const guint8 *)policies, strlen(policies),
        &rows, &summary, &error));
    ch_gtk_row *policy = g_ptr_array_index(rows, 0U);
    g_assert_true(policy->selectable);
    g_assert_cmpstr(policy->selected, ==, "edge");
    g_assert_cmpuint(policy->options->len, ==, 2U);
    g_assert_cmpstr(g_ptr_array_index(policy->options, 0U), ==, "direct");
    g_ptr_array_unref(rows);
    g_free(summary);
}

static void test_invalid_payload(void) {
    ch_gtk_status_model status;
    g_autoptr(GError) error = NULL;
    g_assert_false(ch_gtk_parse_status((const guint8 *)"[]", 2U,
                                       &status, &error));
    g_assert_error(error, g_quark_from_static_string(
        "clambhook-linux-gtk-model"), 1);
}

static void test_action_contracts(void) {
    g_autofree char *profile = ch_gtk_profile_body("work\"profile");
    g_assert_cmpstr(profile, ==, "{\"name\":\"work\\\"profile\"}");

    g_autofree char *policy = ch_gtk_policy_selection_body(
        "manual", "secure");
    g_assert_cmpstr(policy, ==,
                    "{\"group\":\"manual\",\"chain\":\"secure\"}");

    g_autofree char *prompt = ch_gtk_prompt_resolution_body(
        "allow", "until_quit", TRUE, FALSE, TRUE);
    g_assert_cmpstr(
        prompt, ==,
        "{\"action\":\"allow\",\"scope\":\"until_quit\","
        "\"match_host\":true,\"match_port\":false,"
        "\"match_protocol\":true}");

    g_autofree char *capture = ch_gtk_capture_enabled_body(TRUE);
    g_assert_cmpstr(capture, ==, "{\"enabled\":true}");

    g_autofree char *path = ch_gtk_prompt_resolution_path("id /?#");
    g_assert_cmpstr(path, ==,
                    "/api/v1/prompts/id%20%2F%3F%23/resolve");
    g_autofree char *promotion = ch_gtk_silent_promotion_body(
        "forever", TRUE, FALSE, TRUE);
    g_assert_cmpstr(
        promotion, ==,
        "{\"scope\":\"forever\",\"match_host\":true,"
        "\"match_port\":false,\"match_protocol\":true}");
    g_autofree char *promotion_path = ch_gtk_silent_promotion_path("id /?#");
    g_assert_cmpstr(
        promotion_path, ==,
        "/api/v1/prompts/decisions/id%20%2F%3F%23/promote");

    g_autofree char *entries = ch_gtk_capture_entries_path(
        "hello world", "GET,post", TRUE, 100U);
    g_assert_cmpstr(
        entries, ==,
        "/api/v1/developer/entries?limit=100&method=GET%2Cpost&"
        "q=hello%20world&error_only=1");
    g_autofree char *detail_path = ch_gtk_capture_detail_path("capture /1");
    g_assert_cmpstr(detail_path, ==,
                    "/api/v1/developer/entries/capture%20%2F1");
    g_autofree char *curl_path = ch_gtk_capture_curl_path("capture /1");
    g_assert_cmpstr(curl_path, ==,
                    "/api/v1/developer/entries/capture%20%2F1/curl");
}

static void test_capture_detail(void) {
    static const guint8 json[] =
        "{\"id\":\"dev-1\",\"method\":\"POST\","
        "\"url\":\"http://example.test/\",\"host\":\"example.test\","
        "\"profile\":\"work\",\"chain_name\":\"secure\","
        "\"started_at\":\"2026-08-27T00:00:00Z\",\"status\":201,"
        "\"request\":{\"headers\":[{\"name\":\"Authorization\","
        "\"value\":\"[redacted]\",\"redacted\":true}],\"body\":{"
        "\"size\":11,\"preview\":\"hello world\","
        "\"preview_bytes\":11,\"truncated\":false,"
        "\"truncated_after\":4096,\"encoding\":\"utf8\"}},"
        "\"response\":{\"headers\":[],\"body\":{\"size\":10,"
        "\"preview_base64\":\"AAEC\",\"preview_bytes\":3,"
        "\"truncated\":true,\"truncated_after\":3,"
        "\"mime_type\":\"application/octet-stream\","
        "\"encoding\":\"base64\"}},\"error\":\"\"}";
    ch_gtk_capture_detail detail;
    g_autoptr(GError) error = NULL;
    g_assert_true(ch_gtk_parse_capture_detail(
        json, sizeof(json) - 1U, &detail, &error));
    g_assert_cmpstr(detail.identifier, ==, "dev-1");
    g_assert_cmpstr(detail.method, ==, "POST");
    g_assert_cmpint(detail.status, ==, 201);
    g_assert_nonnull(strstr(detail.request_headers,
                            "Authorization: [redacted]"));
    g_assert_nonnull(strstr(detail.request_body, "hello world"));
    g_assert_nonnull(strstr(detail.response_body, "AAEC"));
    g_assert_nonnull(strstr(detail.response_body, "preview truncated"));
    ch_gtk_capture_detail_clear(&detail);

    static const guint8 curl_json[] =
        "{\"curl\":\"curl 'http://example.test/'\"}";
    g_autofree char *curl = ch_gtk_parse_curl_export(
        curl_json, sizeof(curl_json) - 1U, &error);
    g_assert_cmpstr(curl, ==, "curl 'http://example.test/'");

    g_autofree char *import_body = ch_gtk_curl_import_body(
        "curl 'https://example.test/' -H 'X-Test: yes'");
    g_assert_cmpstr(
        import_body, ==,
        "{\"curl\":\"curl 'https://example.test/' -H 'X-Test: yes'\"}");
    static const guint8 imported_json[] =
        "{\"method\":\"POST\",\"url\":\"https://example.test/\","
        "\"headers\":[{\"name\":\"X-Test\",\"value\":\"yes\"}],"
        "\"body\":\"hello\"}";
    ch_gtk_curl_import imported;
    g_assert_true(ch_gtk_parse_curl_import(
        imported_json, sizeof(imported_json) - 1U, &imported, &error));
    g_assert_cmpstr(imported.method, ==, "POST");
    g_assert_cmpstr(imported.url, ==, "https://example.test/");
    g_assert_nonnull(strstr(imported.headers, "X-Test: yes"));
    g_assert_cmpstr(imported.body, ==, "hello");
    ch_gtk_curl_import_clear(&imported);
}

static void test_conditioner_contract(void) {
    static const guint8 json[] =
        "{\"profile\":\"work\",\"enabled\":true,"
        "\"download_kbps\":2048,\"upload_kbps\":512,"
        "\"latency\":\"40ms\",\"jitter\":\"5ms\","
        "\"loss_percent\":1.25}";
    ch_gtk_conditioner_model model;
    g_autoptr(GError) error = NULL;
    g_assert_true(ch_gtk_parse_conditioner(
        json, sizeof(json) - 1U, &model, &error));
    g_assert_cmpstr(model.profile, ==, "work");
    g_assert_true(model.enabled);
    g_assert_cmpuint(model.download_kbps, ==, 2048U);
    g_assert_cmpuint(model.upload_kbps, ==, 512U);
    g_assert_cmpstr(model.latency, ==, "40ms");
    g_assert_cmpfloat(model.loss_percent, ==, 1.25);
    ch_gtk_conditioner_model_clear(&model);

    g_autofree char *body = ch_gtk_conditioner_body(
        "work", TRUE, " 2048 ", "512", " 40ms ", " 5ms ", "1.25",
        &error);
    g_assert_cmpstr(
        body, ==,
        "{\"profile\":\"work\",\"enabled\":true,"
        "\"download_kbps\":2048,\"upload_kbps\":512,"
        "\"latency\":\"40ms\",\"jitter\":\"5ms\","
        "\"loss_percent\":1.25}");
    g_autofree char *cleared = ch_gtk_conditioner_body(
        "", FALSE, "", "", "", "", "", &error);
    g_assert_cmpstr(
        cleared, ==,
        "{\"enabled\":false,\"download_kbps\":0,"
        "\"upload_kbps\":0,\"latency\":\"\",\"jitter\":\"\","
        "\"loss_percent\":0.0}");
    g_assert_null(ch_gtk_conditioner_body(
        "work", TRUE, "-1", "0", "0ms", "0ms", "0", &error));
    g_assert_nonnull(error);
    g_clear_error(&error);
    g_assert_null(ch_gtk_conditioner_body(
        "work", TRUE, "0", "0", "0ms", "0ms", "100.01", &error));
    g_assert_nonnull(error);
}

static void test_dns_contract(void) {
    static const guint8 json[] =
        "{\"profile\":\"work\",\"strategy\":\"encrypted\","
        "\"enabled\":true,\"timeout\":\"4s\",\"upstreams\":[{"
        "\"name\":\"cloudflare\",\"protocol\":\"dot\","
        "\"address\":\"1.1.1.1:853\","
        "\"server_name\":\"cloudflare-dns.com\"}]}";
    ch_gtk_dns_model model;
    g_autoptr(GError) error = NULL;
    g_assert_true(ch_gtk_parse_dns(
        json, sizeof(json) - 1U, &model, &error));
    g_assert_cmpstr(model.profile, ==, "work");
    g_assert_true(model.enabled);
    g_assert_cmpstr(model.timeout, ==, "4s");
    g_assert_nonnull(strstr(model.upstreams_json, "cloudflare-dns.com"));

    g_autofree char *body = ch_gtk_dns_body(
        model.profile, model.enabled, " 4s ", model.upstreams_json, &error);
    g_assert_cmpstr(
        body, ==,
        "{\"profile\":\"work\",\"enabled\":true,\"timeout\":\"4s\","
        "\"upstreams\":[{\"name\":\"cloudflare\","
        "\"protocol\":\"dot\",\"address\":\"1.1.1.1:853\","
        "\"server_name\":\"cloudflare-dns.com\"}]}" );
    ch_gtk_dns_model_clear(&model);

    g_assert_null(ch_gtk_dns_body(
        "work", TRUE, "4s", "{}", &error));
    g_assert_nonnull(error);
}

static void test_license_contract(void) {
    static const guint8 persisted_json[] =
        "{\"installId\":\"install-1\",\"email\":\"owner@example.test\","
        "\"snapshotJson\":\"{\\\"trialStartDate\\\":\\\"2026-08-01T00:00:00Z\\\"}\","
        "\"grantJson\":\"\",\"deviceStateJson\":\"{}\"}";
    ch_gtk_license_state state;
    g_autoptr(GError) error = NULL;
    g_assert_true(ch_gtk_parse_license_state(
        persisted_json, sizeof(persisted_json) - 1U, &state, &error));
    g_assert_cmpstr(state.install_id, ==, "install-1");
    g_assert_cmpstr(state.email, ==, "owner@example.test");
    g_assert_nonnull(strstr(state.snapshot_json, "trialStartDate"));

    static const guint8 applied_json[] =
        "{\"snapshot\":{\"reason\":\"lifetime\"},"
        "\"grant\":{\"signature\":\"opaque\"},\"deviceState\":{"
        "\"current_install_id\":\"install-1\","
        "\"current_device_id\":\"device-1\",\"max_active_devices\":3,"
        "\"devices\":[{\"device_id\":\"device-1\","
        "\"display_name\":\"Workstation\",\"platform\":\"linux\","
        "\"architecture\":\"x86_64\",\"deactivated_at\":\"\"}]} }";
    g_assert_true(ch_gtk_license_state_apply(
        applied_json, sizeof(applied_json) - 1U, &state, &error));
    g_assert_cmpstr(state.snapshot_json, ==, "{\"reason\":\"lifetime\"}");
    g_assert_nonnull(strstr(state.device_state_json, "device-1"));
    g_autofree char *serialized = ch_gtk_license_state_json(&state);
    ch_gtk_license_state reparsed;
    g_assert_true(ch_gtk_parse_license_state(
        (const guint8 *)serialized, strlen(serialized), &reparsed, &error));
    g_assert_cmpstr(reparsed.install_id, ==, state.install_id);
    g_assert_cmpstr(reparsed.device_state_json, ==, state.device_state_json);

    static const char status_json[] =
        "{\"decision\":{\"reason\":\"lifetime\","
        "\"updateCutoffDate\":\"2027-08-01T00:00:00Z\"}}";
    ch_gtk_license_view view;
    g_assert_true(ch_gtk_parse_license_view(
        status_json, state.device_state_json, &view, &error));
    g_assert_true(view.can_use_app);
    g_assert_true(view.current_device_active);
    g_assert_cmpstr(view.title, ==, "Licensed");
    g_assert_cmpuint(view.active_devices, ==, 1U);
    g_assert_cmpuint(view.max_active_devices, ==, 3U);
    g_assert_cmpuint(view.devices->len, ==, 1U);
    ch_gtk_row *device = g_ptr_array_index(view.devices, 0U);
    g_assert_cmpstr(device->title, ==, "Workstation");
    g_assert_nonnull(strstr(device->detail, "active"));
    ch_gtk_license_view_clear(&view);

    g_autofree char *registration = ch_gtk_license_registration_body(
        state.install_id, "Workstation", "x86_64", "1.2.3");
    g_assert_cmpstr(
        registration, ==,
        "{\"install_id\":\"install-1\",\"display_name\":\"Workstation\","
        "\"platform\":\"linux\",\"architecture\":\"x86_64\","
        "\"app_version\":\"1.2.3\"}");
    ch_gtk_license_state_clear(&reparsed);
    ch_gtk_license_state_clear(&state);
}

static void test_rule_create_contract(void) {
    g_autoptr(GError) error = NULL;
    g_autofree char *body = ch_gtk_rule_create_body(
        " private ", " direct ", "host.test, api.test", " internal ",
        " telemetry ", "10.0.0.0/8,2001:db8::/32", " 53, 443 ",
        "tcp, udp", TRUE, &error);
    g_assert_cmpstr(
        body, ==,
        "{\"rule\":{\"name\":\"private\",\"action\":\"direct\","
        "\"domains\":[\"host.test\",\"api.test\"],"
        "\"domain_suffixes\":[\"internal\"],"
        "\"domain_keywords\":[\"telemetry\"],"
        "\"cidrs\":[\"10.0.0.0/8\",\"2001:db8::/32\"],"
        "\"ports\":[53,443],\"networks\":[\"tcp\",\"udp\"]},"
        "\"position\":\"prepend\"}");
    g_assert_null(ch_gtk_rule_create_body(
        "bad-port", "block", "", "", "", "", "70000", "", FALSE,
        &error));
    g_assert_nonnull(error);
    g_clear_error(&error);
    g_assert_null(ch_gtk_rule_create_body(
        "", "direct", "", "", "", "", "", "", FALSE, &error));
    g_assert_nonnull(error);
}

int main(int argc, char **argv) {
    g_test_init(&argc, &argv, NULL);
    g_test_add_func("/gtk-model/status-profiles", test_status_and_profiles);
    g_test_add_func("/gtk-model/traffic", test_traffic);
    g_test_add_func("/gtk-model/pages", test_page_rows);
    g_test_add_func("/gtk-model/invalid", test_invalid_payload);
    g_test_add_func("/gtk-model/action-contracts", test_action_contracts);
    g_test_add_func("/gtk-model/capture-detail", test_capture_detail);
    g_test_add_func("/gtk-model/conditioner-contract",
                    test_conditioner_contract);
    g_test_add_func("/gtk-model/dns-contract", test_dns_contract);
    g_test_add_func("/gtk-model/license-contract", test_license_contract);
    g_test_add_func("/gtk-model/rule-create-contract",
                    test_rule_create_contract);
    return g_test_run();
}
