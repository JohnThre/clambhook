// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#include "test.h"

#include <stdio.h>
#include <string.h>

#include <uv.h>

#include "clambhook/netwatch.h"

typedef struct netwatch_test_state {
    uv_loop_t loop;
    uv_timer_t timeout;
    ch_netwatch *watcher;
    unsigned int probes;
    unsigned int observations;
    ch_network_info observed[2];
} netwatch_test_state;

static ch_status netwatch_test_probe(ch_network_info *out_info, void *context,
                                     ch_error *error) {
    netwatch_test_state *state = context;
    ch_error_clear(error);
    memset(out_info, 0, sizeof(*out_info));
    ++state->probes;
    (void)snprintf(out_info->interface_name,
                   sizeof(out_info->interface_name), "%s",
                   state->probes < 3U ? "en0" : "en1");
    (void)snprintf(out_info->ssid, sizeof(out_info->ssid), "%s",
                   state->probes < 3U ? "HomeNet" : "OfficeNet");
    out_info->is_wifi = true;
    return CH_OK;
}

static void netwatch_test_observation(const ch_network_info *info,
                                      void *context) {
    netwatch_test_state *state = context;
    if (state->observations < 2U) {
        state->observed[state->observations] = *info;
    }
    ++state->observations;
    if (state->observations == 2U) {
        ch_netwatch_stop(state->watcher);
        state->watcher = NULL;
        (void)uv_timer_stop(&state->timeout);
        uv_close((uv_handle_t *)&state->timeout, NULL);
    }
}

static void netwatch_test_timeout(uv_timer_t *timer) {
    netwatch_test_state *state = timer->data;
    ch_netwatch_stop(state->watcher);
    state->watcher = NULL;
    uv_close((uv_handle_t *)timer, NULL);
}

static void netwatch_test_matching(void) {
    ch_network_info info = {
        .interface_name = "en0",
        .ssid = "HomeNet",
        .is_wifi = true
    };
    CH_TEST_ASSERT(ch_network_info_matches(&info, "HomeNet", ""));
    CH_TEST_ASSERT(ch_network_info_matches(&info, " homenet ", " EN0 "));
    CH_TEST_ASSERT(ch_network_info_matches(&info, "", "en0"));
    CH_TEST_ASSERT(!ch_network_info_matches(&info, "", ""));
    CH_TEST_ASSERT(!ch_network_info_matches(&info, "OtherNet", ""));
    CH_TEST_ASSERT(!ch_network_info_matches(&info, "HomeNet", "en1"));
    info.ssid[0] = '\0';
    CH_TEST_ASSERT(!ch_network_info_matches(&info, "HomeNet", ""));
    CH_TEST_ASSERT(ch_network_info_matches(&info, "", "en0"));
}

static void netwatch_test_parsers(void) {
    char value[256];
    CH_TEST_ASSERT(ch_netwatch_valid_interface_name("en0"));
    CH_TEST_ASSERT(ch_netwatch_valid_interface_name("wlan0.1-test"));
    CH_TEST_ASSERT(!ch_netwatch_valid_interface_name("-en0"));
    CH_TEST_ASSERT(!ch_netwatch_valid_interface_name("en0;touch"));
    CH_TEST_ASSERT(ch_netwatch_parse_scutil_interface(
        "Network information\n   interface[0] : en0\n", value,
        sizeof(value)));
    CH_TEST_ASSERT_STRING("en0", value);
    CH_TEST_ASSERT(!ch_netwatch_parse_scutil_interface(
        "interface[0] : en0;touch /tmp/x\n", value, sizeof(value)));

    bool is_wifi = false;
    bool associated = false;
    ch_netwatch_parse_airport("Current Wi-Fi Network: HomeNet\n", value,
                              sizeof(value), &is_wifi, &associated);
    CH_TEST_ASSERT(is_wifi && associated);
    CH_TEST_ASSERT_STRING("HomeNet", value);
    ch_netwatch_parse_airport("en5 is not a Wi-Fi interface.\n", value,
                              sizeof(value), &is_wifi, &associated);
    CH_TEST_ASSERT(!is_wifi && !associated && value[0] == '\0');
    ch_netwatch_parse_airport(
        "You are not associated with an AirPort network.\n", value,
        sizeof(value), &is_wifi, &associated);
    CH_TEST_ASSERT(is_wifi && !associated && value[0] == '\0');

    CH_TEST_ASSERT(ch_netwatch_parse_ipconfig_ssid(
        "IPv4 : {\n  SSID : Office WiFi\n}\n", value, sizeof(value)));
    CH_TEST_ASSERT_STRING("Office WiFi", value);
    CH_TEST_ASSERT(!ch_netwatch_parse_ipconfig_ssid(
        "SSID : <redacted>\n", value, sizeof(value)));
    CH_TEST_ASSERT(ch_netwatch_parse_proc_wireless(
        "Inter-| sta\n face | quality\n wlan0: 0000 70. 0.\n", value,
        sizeof(value)));
    CH_TEST_ASSERT_STRING("wlan0", value);
}

static void netwatch_test_watcher(void) {
    netwatch_test_state state;
    memset(&state, 0, sizeof(state));
    CH_TEST_ASSERT(uv_loop_init(&state.loop) == 0);
    CH_TEST_ASSERT(uv_timer_init(&state.loop, &state.timeout) == 0);
    state.timeout.data = &state;
    ch_netwatch_options options = {
        .poll_milliseconds = 10U,
        .probe = netwatch_test_probe,
        .probe_context = &state,
        .observation = netwatch_test_observation,
        .observation_context = &state
    };
    ch_error error;
    state.watcher = ch_netwatch_start(&state.loop, &options, &error);
    CH_TEST_ASSERT(state.watcher != NULL);
    CH_TEST_ASSERT(uv_timer_start(&state.timeout, netwatch_test_timeout,
                                  2000U, 0U) == 0);
    (void)uv_run(&state.loop, UV_RUN_DEFAULT);
    CH_TEST_ASSERT(uv_loop_close(&state.loop) == 0);
    CH_TEST_ASSERT(state.observations == 2U);
    CH_TEST_ASSERT(state.probes >= 3U);
    CH_TEST_ASSERT_STRING("en0", state.observed[0].interface_name);
    CH_TEST_ASSERT_STRING("HomeNet", state.observed[0].ssid);
    CH_TEST_ASSERT_STRING("en1", state.observed[1].interface_name);
    CH_TEST_ASSERT_STRING("OfficeNet", state.observed[1].ssid);
}

static void netwatch_test_current(void) {
    ch_network_info info;
    ch_error error;
    CH_TEST_ASSERT(ch_netwatch_current(&info, NULL, &error) == CH_OK);
    CH_TEST_ASSERT(info.interface_name[0] == '\0' ||
                   ch_netwatch_valid_interface_name(info.interface_name));
}

void ch_test_netwatch(void) {
    netwatch_test_matching();
    netwatch_test_parsers();
    netwatch_test_watcher();
    netwatch_test_current();
}
