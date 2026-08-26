#include "test.h"

#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/stat.h>
#include <unistd.h>

#include "clambhook/rule_feed.h"
#include "clambhook/config.h"
#include "internal.h"

void ch_test_rule_feed(void) {
    static const char hosts[] =
        "\n# comment\nexample.com\n0.0.0.0 ads.example.com tracker.example.com\n"
        "10.0.0.1/8\n192.0.2.10\nhttps://cdn.example.net/path\n";
    ch_rule_feed feed;
    ch_error error;
    CH_TEST_ASSERT(ch_rule_feed_parse(hosts, strlen(hosts), "auto", &feed,
                                      &error) == CH_OK);
    CH_TEST_ASSERT_STRING("hosts", feed.format);
    CH_TEST_ASSERT(feed.domain_suffix_count == 4U);
    CH_TEST_ASSERT_STRING("ads.example.com", feed.domain_suffixes[0]);
    CH_TEST_ASSERT_STRING("cdn.example.net", feed.domain_suffixes[1]);
    CH_TEST_ASSERT_STRING("10.0.0.0/8", feed.cidrs[0]);
    CH_TEST_ASSERT_STRING("192.0.2.10/32", feed.cidrs[1]);
    ch_rule_feed_clear(&feed);

    static const char adblock[] =
        "\n! comment\n||ads.example.com^\n@@||allowed.example.com^\n"
        "example.org##.ad\n/regex/\n|https://track.example.net/path\n";
    CH_TEST_ASSERT(ch_rule_feed_parse(adblock, strlen(adblock), "auto", &feed,
                                      &error) == CH_OK);
    CH_TEST_ASSERT_STRING("adblock", feed.format);
    CH_TEST_ASSERT(feed.domain_suffix_count == 2U);
    CH_TEST_ASSERT_STRING("ads.example.com", feed.domain_suffixes[0]);
    CH_TEST_ASSERT_STRING("track.example.net", feed.domain_suffixes[1]);
    CH_TEST_ASSERT(feed.skipped == 1U);
    ch_rule_feed_clear(&feed);

    char root[160];
    (void)snprintf(root, sizeof(root), "/tmp/clambhook-feed-%ld",
                   (long)getpid());
    CH_TEST_ASSERT(mkdir(root, 0700) == 0 || errno == EEXIST);
    char directory[220];
    (void)snprintf(directory, sizeof(directory),
                   "%s/.clambhook-subscriptions", root);
    CH_TEST_ASSERT(mkdir(directory, 0700) == 0 || errno == EEXIST);
    char cache_path[300];
    (void)snprintf(cache_path, sizeof(cache_path),
                   "%s/default-ads-legacyhash.json", directory);
    FILE *cache = fopen(cache_path, "wb");
    CH_TEST_ASSERT(cache != NULL);
    CH_TEST_ASSERT(fputs(
        "{\"version\":1,\"profile\":\"default\",\"name\":\"ads\","
        "\"url\":\"https://lists.example/ads.txt\",\"format\":\"plain\","
        "\"action\":\"reject\",\"fetched_ts_ns\":42,"
        "\"domain_suffixes\":[\"Ads.Example.com\",\"ads.example.com\"],"
        "\"cidrs\":[\"192.0.2.9/24\"],\"skipped\":3}", cache) >= 0);
    CH_TEST_ASSERT(fclose(cache) == 0);
    ch_rule_feed_cache decoded;
    char config_path[220];
    (void)snprintf(config_path, sizeof(config_path), "%s/clambhook.toml",
                   root);
    CH_TEST_ASSERT(ch_rule_feed_cache_load(
        config_path, CH_RULE_FEED_SUBSCRIPTION, "default", "ads",
        "https://lists.example/ads.txt", &decoded, &error) == CH_OK);
    CH_TEST_ASSERT(decoded.feed.domain_suffix_count == 1U);
    CH_TEST_ASSERT_STRING("ads.example.com", decoded.feed.domain_suffixes[0]);
    CH_TEST_ASSERT_STRING("192.0.2.0/24", decoded.feed.cidrs[0]);
    CH_TEST_ASSERT(decoded.fetched_ts_ns == 42);
    decoded.fetched_ts_ns = INT64_C(1787728824039829000);
    CH_TEST_ASSERT(ch_rule_feed_cache_write(
        config_path, CH_RULE_FEED_SUBSCRIPTION, &decoded, &error) == CH_OK);
    ch_rule_feed_cache_clear(&decoded);
    CH_TEST_ASSERT(unlink(cache_path) == 0);
    char native_cache_path[320];
    (void)snprintf(native_cache_path, sizeof(native_cache_path),
                   "%s/default-ads-9ed34e8ace78.json", directory);
    FILE *native_cache = fopen(native_cache_path, "rb");
    CH_TEST_ASSERT(native_cache != NULL);
    CH_TEST_ASSERT(fclose(native_cache) == 0);
    CH_TEST_ASSERT(ch_rule_feed_cache_load(
        config_path, CH_RULE_FEED_SUBSCRIPTION, "default", "ads",
        "https://lists.example/ads.txt", &decoded, &error) == CH_OK);
    CH_TEST_ASSERT(decoded.fetched_ts_ns == INT64_C(1787728824039829000));
    ch_rule_feed_cache_clear(&decoded);
    CH_TEST_ASSERT(unlink(native_cache_path) == 0);
    const char *unsafe_urls[] = {
        "http://127.0.0.1/list.txt",
        "http://10.0.0.1/list.txt",
        "http://169.254.169.254/latest/meta-data/",
        "http://metadata.google.internal/list.txt",
        "file:///tmp/list.txt"
    };
    for (size_t index = 0U;
         index < sizeof(unsafe_urls) / sizeof(unsafe_urls[0]); ++index) {
        ch_rule_feed_refresh_options options = {
            .config_path = config_path,
            .kind = CH_RULE_FEED_SUBSCRIPTION,
            .profile = "default",
            .name = "unsafe",
            .url = unsafe_urls[index],
            .format = "plain",
            .action = "block"
        };
        CH_TEST_ASSERT(ch_rule_feed_refresh(&options, &error) ==
                       CH_ERROR_INVALID_ARGUMENT);
    }
    static const char refresh_config[] =
        "active = \"default\"\n"
        "[[profile]]\nname = \"default\"\n"
        "[[profile.chain]]\nname = \"direct\"\n"
        "[[profile.chain.server]]\nprotocol = \"direct\"\n"
        "[[profile.rule_subscription]]\nname = \"unsafe\"\n"
        "url = \"http://127.0.0.1/list.txt\"\n"
        "action = \"block\"\n";
    ch_config *config = NULL;
    CH_TEST_ASSERT(ch_config_parse(refresh_config, config_path, &config,
                                   &error) == CH_OK);
    char *response = ch_config_refresh_rule_feeds_json(
        config, "default", CH_RULE_FEED_SUBSCRIPTION,
        "{\"names\":[\" unsafe \"]}", &error);
    CH_TEST_ASSERT(response != NULL);
    CH_TEST_ASSERT(strstr(response, "\"last_error\":") != NULL);
    CH_TEST_ASSERT(strstr(response, "public") != NULL);
    free(response);
    CH_TEST_ASSERT(ch_config_refresh_rule_feeds_json(
        config, "default", CH_RULE_FEED_SUBSCRIPTION,
        "{\"names\":[\"missing\"]}", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_NOT_FOUND);
    CH_TEST_ASSERT(ch_config_refresh_rule_feeds_json(
        config, "default", CH_RULE_FEED_SUBSCRIPTION,
        "{\"profile\":42}", &error) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_INVALID_ARGUMENT);
    CH_TEST_ASSERT(strstr(error.message, "must be a string") != NULL);
    ch_config_free(config);
    CH_TEST_ASSERT(rmdir(directory) == 0);
    CH_TEST_ASSERT(rmdir(root) == 0);
}
