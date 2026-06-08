namespace Clambhook.Tests {
    public void add_api_client_tests() {
        Test.add_func("/linux/api-client/normalizes-urls-and-builds-event-url", () => {
            var client = new ClambhookApiClient("http://127.0.0.1:9090/", () => "");

            assert_cmpstr(client.build_uri("/api/v1/status"), CompareOperator.EQ, "http://127.0.0.1:9090/api/v1/status");
            assert_cmpstr(client.events_uri(), CompareOperator.EQ, "ws://127.0.0.1:9090/api/v1/events?types=connection.*,rule.*,hop.*,log.*");

            var tls = new ClambhookApiClient("https://proxy.example.test/", () => "");
            assert_cmpstr(tls.events_uri(), CompareOperator.EQ, "wss://proxy.example.test/api/v1/events?types=connection.*,rule.*,hop.*,log.*");
        });

        Test.add_func("/linux/api-client/trims-bearer-token", () => {
            var client = new ClambhookApiClient("http://127.0.0.1:9090", () => " secret-token ");
            assert_cmpstr(client.authorization_header(), CompareOperator.EQ, "Bearer secret-token");
        });

        Test.add_func("/linux/api-client/encodes-active-profile-body", () => {
            assert_cmpstr(ClambhookApiClient.active_profile_body("work"), CompareOperator.EQ, "{\"name\":\"work\"}");
        });

        Test.add_func("/linux/api-client/encodes-create-rule-body", () => {
            var rule = new RulePayload();
            rule.name = "block-example-com";
            rule.action = "block";
            rule.domain_suffixes.add("example.com");
            rule.ports.add(443);
            rule.networks.add("tcp");
            assert_cmpstr(
                ClambhookApiClient.create_rule_body(rule),
                CompareOperator.EQ,
                "{\"rule\":{\"name\":\"block-example-com\",\"action\":\"block\",\"domain_suffixes\":[\"example.com\"],\"ports\":[443],\"networks\":[\"tcp\"]},\"position\":\"append\"}"
            );
        });

        Test.add_func("/linux/api-client/encodes-create-rule-from-connection-body", () => {
            var connection = new TrafficConnectionPayload();
            connection.conn_id = "c1";
            connection.profile = "Work";
            var rule = new RulePayload();
            rule.name = "api";
            rule.action = "chain:proxy";
            assert_cmpstr(
                ClambhookApiClient.create_rule_from_connection_body(connection, rule),
                CompareOperator.EQ,
                "{\"conn_id\":\"c1\",\"profile\":\"Work\",\"name\":\"api\",\"action\":\"chain:proxy\",\"scope\":\"auto\",\"position\":\"append\"}"
            );
        });

        Test.add_func("/linux/api-client/encodes-cleanup-rule-body", () => {
            var suggestion = new TrafficCleanupSuggestionPayload();
            suggestion.profile = "Work";
            suggestion.kind = "unused_in_history";
            suggestion.rule_name = "old";
            suggestion.target_rule_name = "old";
            suggestion.operation = "delete_rule";
            assert_cmpstr(
                ClambhookApiClient.cleanup_rule_body(suggestion),
                CompareOperator.EQ,
                "{\"profile\":\"Work\",\"kind\":\"unused_in_history\",\"rule_name\":\"old\",\"target_rule_name\":\"old\",\"operation\":\"delete_rule\"}"
            );
        });
    }
}
