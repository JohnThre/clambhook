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

            assert_cmpstr(event.type, CompareOperator.EQ, "connection.bytes");
            assert_cmpfloat(event.double_data("rx_delta"), CompareOperator.EQ, 2048);
            assert_cmpfloat(event.double_data("tx_delta"), CompareOperator.EQ, 1024);
            assert_cmpstr(event.string_data("line"), CompareOperator.EQ, "ready");
        });
    }
}
