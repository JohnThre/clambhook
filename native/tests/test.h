#ifndef CLAMBHOOK_TEST_H
#define CLAMBHOOK_TEST_H

#include <stdio.h>
#include <string.h>

extern int ch_test_failures;

#define CH_TEST_ASSERT(condition) do { \
    if (!(condition)) { \
        fprintf(stderr, "%s:%d: assertion failed: %s\n", __FILE__, __LINE__, #condition); \
        ++ch_test_failures; \
        return; \
    } \
} while (0)

#define CH_TEST_ASSERT_STRING(expected, actual) do { \
    const char *ch_test_expected_ = (expected); \
    const char *ch_test_actual_ = (actual); \
    if (ch_test_actual_ == NULL || strcmp(ch_test_expected_, ch_test_actual_) != 0) { \
        fprintf(stderr, "%s:%d: expected [%s], got [%s]\n", __FILE__, __LINE__, \
                ch_test_expected_, ch_test_actual_ == NULL ? "(null)" : ch_test_actual_); \
        ++ch_test_failures; \
        return; \
    } \
} while (0)

void ch_test_json(void);
void ch_test_api_server(void);
void ch_test_config(void);
void ch_test_crypto(void);
void ch_test_dns(void);
void ch_test_events(void);
void ch_test_ip_stack(void);
void ch_test_license(void);
void ch_test_listener(void);
void ch_test_netwatch(void);
void ch_test_procattr(void);
void ch_test_protocol(void);
void ch_test_rule_feed(void);
void ch_test_rules(void);
void ch_test_runtime(void);
void ch_test_runtime_listener(void);
void ch_test_socks(void);
void ch_test_watcher(void);

#endif
