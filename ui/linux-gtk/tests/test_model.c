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
    assert_page(CH_GTK_PAGE_POLICIES,
        "{\"groups\":[{\"name\":\"fast\",\"type\":\"url-test\","
        "\"active_chain\":\"edge\",\"chains\":[\"edge\"]}]}",
        "fast");
    assert_page(CH_GTK_PAGE_PROMPTS,
        "{\"prompts\":[{\"id\":\"prompt-1\","
        "\"process_name\":\"Browser\",\"target\":\"example.com:443\","
        "\"network\":\"tcp\"}]}", "Browser");
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
}

int main(int argc, char **argv) {
    g_test_init(&argc, &argv, NULL);
    g_test_add_func("/gtk-model/status-profiles", test_status_and_profiles);
    g_test_add_func("/gtk-model/traffic", test_traffic);
    g_test_add_func("/gtk-model/pages", test_page_rows);
    g_test_add_func("/gtk-model/invalid", test_invalid_payload);
    g_test_add_func("/gtk-model/action-contracts", test_action_contracts);
    return g_test_run();
}
