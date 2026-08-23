#include "test.h"

#include "api_server.h"

void ch_test_api_server(void) {
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
}
