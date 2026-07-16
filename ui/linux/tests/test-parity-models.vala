namespace Clambhook.Tests {
    public void add_parity_model_tests() {
        Test.add_func("/linux/parity/parses-policy-groups", () => {
            var payload = PolicyGroupsPayload.from_json("""
            {
              "profile": "A",
              "groups": [
                {
                  "name": "auto",
                  "type": "url-test",
                  "chains": ["primary", "backup"],
                  "selected_chain": "primary",
                  "results": [
                    { "chain_name": "primary", "healthy": true, "latency_ns": 42000000 },
                    { "chain_name": "backup", "healthy": false, "error": "timeout" }
                  ]
                },
                {
                  "name": "manual",
                  "type": "select",
                  "chains": ["primary", "backup"],
                  "selected": "backup"
                }
              ]
            }
            """);

            assert_cmpstr(payload.profile, CompareOperator.EQ, "A");
            assert_cmpint(payload.groups.size, CompareOperator.EQ, 2);

            var auto = payload.groups[0];
            assert_false(auto.is_select());
            assert_cmpstr(auto.active_chain(), CompareOperator.EQ, "primary");
            var primary = auto.result_for("primary");
            assert_true(primary != null && primary.healthy);
            assert_cmpint((int) (primary.latency_ns / 1000000), CompareOperator.EQ, 42);

            var manual = payload.groups[1];
            assert_true(manual.is_select());
            assert_cmpstr(manual.active_chain(), CompareOperator.EQ, "backup");
        });

        Test.add_func("/linux/parity/parses-pending-prompts", () => {
            var payload = PromptsPayload.from_json("""
            {
              "prompts": [
                { "id": "p1", "network": "tcp", "target": "api.example.com:443", "process_name": "curl", "process_path": "/usr/bin/curl", "pid": 42, "waiters": 1 }
              ]
            }
            """);

            assert_cmpint(payload.prompts.size, CompareOperator.EQ, 1);
            assert_cmpstr(payload.prompts[0].id, CompareOperator.EQ, "p1");
            assert_cmpstr(payload.prompts[0].process_name, CompareOperator.EQ, "curl");
            assert_cmpstr(payload.prompts[0].target, CompareOperator.EQ, "api.example.com:443");
        });

        Test.add_func("/linux/parity/parses-dns-and-selects-endpoint", () => {
            var payload = DnsPayload.from_json("""
            {
              "profile": "A",
              "strategy": "encrypted",
              "enabled": true,
              "timeout": "3s",
              "intercepts_port_53": true,
              "upstreams": [
                { "name": "cloudflare", "protocol": "doh", "url": "https://cloudflare-dns.com/dns-query" },
                { "protocol": "dot", "address": "1.1.1.1:853" }
              ],
              "upstream_routes": [
                { "target": "cloudflare-dns.com:443", "action": "chain", "chain_name": "primary" }
              ]
            }
            """);

            assert_true(payload.enabled);
            assert_cmpstr(payload.timeout, CompareOperator.EQ, "3s");
            assert_cmpint(payload.upstreams.size, CompareOperator.EQ, 2);
            assert_cmpstr(payload.upstreams[0].endpoint(), CompareOperator.EQ, "https://cloudflare-dns.com/dns-query");
            assert_cmpstr(payload.upstreams[1].endpoint(), CompareOperator.EQ, "1.1.1.1:853");
            assert_cmpint(payload.upstream_routes.size, CompareOperator.EQ, 1);
            assert_cmpstr(payload.upstream_routes[0].chain_name, CompareOperator.EQ, "primary");
        });

        Test.add_func("/linux/parity/parses-developer-status-and-entries", () => {
            var status = DeveloperStatusPayload.from_json("""
            {
              "enabled": true,
              "mitm_enabled": true,
              "capture_limit": 200,
              "capture_count": 3,
              "ca_cert_path": "/tmp/ca.pem"
            }
            """);
            assert_true(status.enabled);
            assert_true(status.mitm_enabled);
            assert_cmpint(status.capture_count, CompareOperator.EQ, 3);

            var entries = DeveloperEntryPayload.list_from_json("""
            {
              "entries": [
                { "id": "e1", "method": "GET", "url": "https://api.example.com/v1", "status_code": 200, "response_bytes": 1024 }
              ]
            }
            """);
            assert_cmpint(entries.size, CompareOperator.EQ, 1);
            assert_cmpstr(entries[0].method, CompareOperator.EQ, "GET");
            assert_cmpint(entries[0].status_code, CompareOperator.EQ, 200);
        });
    }
}
