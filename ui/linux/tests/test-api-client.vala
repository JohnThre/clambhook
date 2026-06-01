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
            rule.domains.add("example.com");
            assert_cmpstr(
                ClambhookApiClient.create_rule_body(rule),
                CompareOperator.EQ,
                "{\"rule\":{\"name\":\"block-example-com\",\"action\":\"block\",\"domains\":[\"example.com\"]},\"position\":\"append\"}"
            );
        });
    }
}
