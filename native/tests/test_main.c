#include "test.h"

int ch_test_failures = 0;

int main(void) {
    ch_test_api_server();
    ch_test_config();
    ch_test_json();
    ch_test_crypto();
    ch_test_events();
    ch_test_license();
    ch_test_rules();
    ch_test_runtime();
    ch_test_socks();
    ch_test_watcher();
    if (ch_test_failures != 0) {
        fprintf(stderr, "%d native test group(s) failed\n", ch_test_failures);
        return 1;
    }
    puts("native tests passed");
    return 0;
}
