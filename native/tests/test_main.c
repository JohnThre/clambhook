#include "test.h"

#include <stdlib.h>

int ch_test_failures = 0;

static int ch_test_selected(const char *selected, const char *name) {
    return selected == NULL || selected[0] == '\0' ||
        strcmp(selected, name) == 0;
}

int main(void) {
    const char *selected = getenv("CLAMBHOOK_TEST_GROUP");
    if (ch_test_selected(selected, "api_server")) ch_test_api_server();
    if (ch_test_selected(selected, "config")) ch_test_config();
    if (ch_test_selected(selected, "json")) ch_test_json();
    if (ch_test_selected(selected, "crypto")) ch_test_crypto();
    if (ch_test_selected(selected, "developer")) ch_test_developer();
    if (ch_test_selected(selected, "dns")) ch_test_dns();
    if (ch_test_selected(selected, "events")) ch_test_events();
    if (ch_test_selected(selected, "ip_stack")) ch_test_ip_stack();
    if (ch_test_selected(selected, "license")) ch_test_license();
    if (ch_test_selected(selected, "listener")) ch_test_listener();
    if (ch_test_selected(selected, "netwatch")) ch_test_netwatch();
    if (ch_test_selected(selected, "procattr")) ch_test_procattr();
    if (ch_test_selected(selected, "policy")) ch_test_policy();
    if (ch_test_selected(selected, "prompt")) ch_test_prompt();
    if (ch_test_selected(selected, "protocol")) ch_test_protocol();
    if (ch_test_selected(selected, "rule_feed")) ch_test_rule_feed();
    if (ch_test_selected(selected, "rules")) ch_test_rules();
    if (ch_test_selected(selected, "runtime")) ch_test_runtime();
    if (ch_test_selected(selected, "runtime_listener")) {
        ch_test_runtime_listener();
    }
    if (ch_test_selected(selected, "socks")) ch_test_socks();
    if (ch_test_selected(selected, "temporary_rules")) ch_test_temporary_rules();
    if (ch_test_selected(selected, "traffic")) ch_test_traffic();
    if (ch_test_selected(selected, "watcher")) ch_test_watcher();
    if (ch_test_failures != 0) {
        fprintf(stderr, "%d native test group(s) failed\n", ch_test_failures);
        return 1;
    }
    puts("native tests passed");
    return 0;
}
