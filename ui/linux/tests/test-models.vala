namespace Clambhook.Tests {
    public void add_model_tests() {
        Test.add_func("/linux/models/status-decodes-snake-case-listeners", () => {
            var status = StatusPayload.from_json("""
                {
                  "running": true,
                  "profile": "work",
                  "listeners": [
                    { "protocol": "socks5", "addr": "127.0.0.1:1080", "active_conns": 3 }
                  ]
                }
            """);

            assert_true(status.running);
            assert_cmpstr(status.profile, CompareOperator.EQ, "work");
            assert_cmpint(status.listeners.size, CompareOperator.EQ, 1);
            assert_cmpstr(status.listeners[0].protocol, CompareOperator.EQ, "socks5");
            assert_cmpint(status.listeners[0].active_conns, CompareOperator.EQ, 3);
        });

        Test.add_func("/linux/models/servers-decodes-geo", () => {
            var servers = ServersPayload.from_json("""
                {
                  "profile": "default",
                  "chains": [
                    {
                      "name": "primary",
                      "servers": [
                        {
                          "name": "london",
                          "address": "uk.example:443",
                          "protocol": "clambback",
                          "geo": {
                            "country": "United Kingdom",
                            "country_code": "GB",
                            "city": "London",
                            "latitude": 51.5,
                            "longitude": -0.1
                          }
                        }
                      ]
                    }
                  ]
                }
            """);

            assert_cmpstr(servers.profile, CompareOperator.EQ, "default");
            assert_cmpint(servers.chains.size, CompareOperator.EQ, 1);
            assert_cmpstr(servers.chains[0].servers[0].name, CompareOperator.EQ, "london");
            assert_cmpstr(servers.chains[0].servers[0].geo.country_code, CompareOperator.EQ, "GB");
        });

        Test.add_func("/linux/models/event-values-read-numeric-and-string-data", () => {
            var event = DaemonEvent.from_json("""
                {
                  "shard_id": 1,
                  "lamport": 2,
                  "ts_ns": 3,
                  "type": "connection.bytes",
                  "data": {
                    "rx_delta": 2048,
                    "tx_delta": "1024",
                    "line": "ready"
                  }
                }
            """);

            assert_cmpstr(event.event_type, CompareOperator.EQ, "connection.bytes");
            assert_cmpfloat(event.double_data("rx_delta"), CompareOperator.EQ, 2048);
            assert_cmpfloat(event.double_data("tx_delta"), CompareOperator.EQ, 1024);
            assert_cmpstr(event.string_data("line"), CompareOperator.EQ, "ready");
        });

        Test.add_func("/linux/models/traffic-decodes-rule-suggestions", () => {
            var traffic = TrafficSnapshotPayload.from_json("""
                {
                  "updated_ts_ns": 99,
                  "summary": { "active_connections": 1 },
                  "rule_suggestions": [
                    {
                      "id": "domain_suffix:block:example.com",
                      "kind": "domain_suffix",
                      "profile": "Work",
                      "action": "block",
                      "draft_rule": {
                        "name": "block-example-com",
                        "action": "block",
                        "domain_suffixes": ["example.com"],
                        "ports": [443],
                        "networks": ["tcp"]
                      },
                      "count": 3,
                      "reason": "Observed 3 connections across 2 subdomains."
                    }
                  ],
                  "connections": [
                    { "conn_id": "c1", "state": "closed", "target_host": "api.example.com" }
                  ]
                }
            """);

            assert_cmpint(traffic.rule_suggestions.size, CompareOperator.EQ, 1);
            var suggestion = traffic.rule_suggestions[0];
            assert_cmpstr(suggestion.kind, CompareOperator.EQ, "domain_suffix");
            assert_cmpstr(suggestion.draft_rule.name, CompareOperator.EQ, "block-example-com");
            assert_cmpstr(suggestion.draft_rule.domain_suffixes[0], CompareOperator.EQ, "example.com");
            assert_cmpint(suggestion.draft_rule.ports[0], CompareOperator.EQ, 443);
            assert_cmpstr(suggestion.draft_rule.networks[0], CompareOperator.EQ, "tcp");
        });
    }
}
