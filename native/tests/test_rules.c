#include "test.h"

#include <stdlib.h>
#include <string.h>

#include "clambhook/config.h"
#include "clambhook/rules.h"

static const char *known_chains[] = {"default", "corp"};
static const char *known_groups[] = {"auto"};

void ch_test_rules(void) {
    const char *ads_suffixes[] = {"ads.example.com"};
    const char *corp_domains[] = {"api.example.com"};
    int https_ports[] = {443};
    ch_rule_spec rules[] = {
        {
            .name = "ads", .action = "block",
            .domain_suffixes = {.items = ads_suffixes, .count = 1U}
        },
        {
            .name = "corp", .action = "chain:corp",
            .domains = {.items = corp_domains, .count = 1U},
            .ports = {.items = https_ports, .count = 1U}
        }
    };
    ch_error error;
    ch_rule_engine *engine = ch_rule_engine_compile(
        rules, 2U, "default",
        (ch_string_list){.items = known_chains, .count = 2U},
        (ch_string_list){.items = known_groups, .count = 1U},
        NULL, 0U, &error
    );
    CH_TEST_ASSERT(engine != NULL);
    CH_TEST_ASSERT(!ch_rule_engine_needs_process(engine));
    ch_rule_decision decision;
    ch_rule_match_context context = {.network = "TCP", .target = "api.example.com:443"};
    CH_TEST_ASSERT(ch_rule_engine_decide(engine, &context, &decision, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("corp", decision.rule_name);
    CH_TEST_ASSERT_STRING("chain", decision.action);
    CH_TEST_ASSERT_STRING("corp", decision.chain_name);
    CH_TEST_ASSERT(decision.rule_number == 2U && !decision.is_default);
    ch_rule_decision_clear(&decision);

    context.target = "cdn.ads.example.com:443";
    CH_TEST_ASSERT(ch_rule_engine_decide(engine, &context, &decision, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("ads", decision.rule_name);
    CH_TEST_ASSERT_STRING("block", decision.action);
    CH_TEST_ASSERT_STRING("domain_suffix", decision.matcher_kind);
    ch_rule_decision_clear(&decision);

    context.target = "example.org:80";
    CH_TEST_ASSERT(ch_rule_engine_decide(engine, &context, &decision, &error) == CH_OK);
    CH_TEST_ASSERT(decision.is_default && decision.rule_number == 3U);
    CH_TEST_ASSERT_STRING("default", decision.chain_name);
    ch_rule_decision_clear(&decision);
    ch_rule_engine_destroy(engine);

    const char *cidrs[] = {"10.0.0.0/8"};
    const char *source_cidrs[] = {"192.168.0.0/16"};
    const char *networks[] = {"udp"};
    int dns_ports[] = {53};
    ch_rule_spec cidr_rule = {
        .name = "local-dns", .action = "direct",
        .cidrs = {.items = cidrs, .count = 1U},
        .source_cidrs = {.items = source_cidrs, .count = 1U},
        .networks = {.items = networks, .count = 1U},
        .ports = {.items = dns_ports, .count = 1U}
    };
    engine = ch_rule_engine_compile(
        &cidr_rule, 1U, "default",
        (ch_string_list){.items = known_chains, .count = 2U},
        (ch_string_list){0}, NULL, 0U, &error
    );
    CH_TEST_ASSERT(engine != NULL);
    CH_TEST_ASSERT(!ch_rule_engine_needs_process(engine));
    context = (ch_rule_match_context){
        .network = "udp", .target = "10.1.2.3:53", .source = "192.168.1.2:1234"
    };
    CH_TEST_ASSERT(ch_rule_engine_decide(engine, &context, &decision, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("direct", decision.action);
    ch_rule_decision_clear(&decision);
    context.source = "172.16.1.2:1234";
    CH_TEST_ASSERT(ch_rule_engine_decide(engine, &context, &decision, &error) == CH_OK);
    CH_TEST_ASSERT(decision.is_default);
    ch_rule_decision_clear(&decision);
    ch_rule_engine_destroy(engine);

    const char *processes[] = {"curl"};
    ch_rule_spec process_rule = {
        .name = "block-curl", .action = "block",
        .processes = {.items = processes, .count = 1U}
    };
    engine = ch_rule_engine_compile(
        &process_rule, 1U, "default",
        (ch_string_list){.items = known_chains, .count = 2U},
        (ch_string_list){0}, NULL, 0U, &error
    );
    CH_TEST_ASSERT(engine != NULL);
    CH_TEST_ASSERT(ch_rule_engine_needs_process(engine));
    context = (ch_rule_match_context){
        .network = "tcp", .target = "example.com:443",
        .process_name = "curl", .process_path = "/usr/bin/curl"
    };
    CH_TEST_ASSERT(ch_rule_engine_decide(engine, &context, &decision, &error) == CH_OK);
    CH_TEST_ASSERT_STRING("block-curl", decision.rule_name);
    ch_rule_decision_clear(&decision);
    ch_rule_engine_destroy(engine);

    ch_rule_spec missing = {.name = "bad", .action = "chain:missing"};
    CH_TEST_ASSERT(ch_rule_engine_compile(
        &missing, 1U, "default", (ch_string_list){.items = known_chains, .count = 2U},
        (ch_string_list){0}, NULL, 0U, &error
    ) == NULL);
    CH_TEST_ASSERT(error.code == CH_ERROR_NOT_FOUND);

    {
        static const char config_toml[] =
            "active = \"work\"\n"
            "[[profile]]\nname = \"work\"\n"
            "[[profile.chain]]\nname = \"default\"\n"
            "[[profile.chain.server]]\nname = \"direct\"\nprotocol = \"direct\"\n"
            "[[profile.chain]]\nname = \"backup\"\n"
            "[[profile.chain.server]]\nname = \"backup-direct\"\nprotocol = \"direct\"\n"
            "[[profile.policy_group]]\nname = \"manual\"\ntype = \"select\"\n"
            "chains = [\"default\", \"backup\"]\nselected = \"backup\"\n"
            "[[profile.rule_set]]\nname = \"tracking\"\n"
            "domain_suffixes = [\"track.example\"]\n"
            "[[profile.rule]]\nname = \"block-trackers\"\naction = \"block\"\n"
            "rule_sets = [\"tracking\"]\nnetworks = [\"tcp\"]\n"
            "[[profile.rule]]\nname = \"manual-web\"\naction = \"group:manual\"\n"
            "ports = [443]\n";
        ch_config *config = NULL;
        CH_TEST_ASSERT(ch_config_parse(config_toml, NULL, &config, &error) == CH_OK);
        engine = ch_rule_engine_compile_config(config, "work", &error);
        CH_TEST_ASSERT(engine != NULL);
        context = (ch_rule_match_context){
            .network = "tcp", .target = "cdn.track.example:443", .source = ""
        };
        CH_TEST_ASSERT(ch_rule_engine_decide(engine, &context, &decision, &error) == CH_OK);
        CH_TEST_ASSERT_STRING("block-trackers", decision.rule_name);
        CH_TEST_ASSERT_STRING("block", decision.action);
        ch_rule_decision_clear(&decision);
        ch_rule_engine_destroy(engine);

        char *explanation = NULL;
        context.target = "example.org:443";
        CH_TEST_ASSERT(ch_rule_explain_config_json(
            config, "work", &context, &explanation, &error
        ) == CH_OK);
        CH_TEST_ASSERT(strstr(explanation, "\"profile\":\"work\"") != NULL);
        CH_TEST_ASSERT(strstr(explanation, "\"rule_name\":\"manual-web\"") != NULL);
        CH_TEST_ASSERT(strstr(explanation, "\"group_name\":\"manual\"") != NULL);
        CH_TEST_ASSERT(strstr(explanation, "\"chain_name\":\"backup\"") != NULL);
        CH_TEST_ASSERT(strstr(explanation, "\"hop_count\":1") != NULL);
        free(explanation);
        CH_TEST_ASSERT(ch_rule_engine_compile_config(config, "missing", &error) == NULL);
        CH_TEST_ASSERT(error.code == CH_ERROR_NOT_FOUND);
        ch_config_free(config);
    }
}
