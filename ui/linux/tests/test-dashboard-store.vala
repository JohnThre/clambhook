namespace Clambhook.Tests {
    private class FakeApi : Object, ClambhookApiProviding {
        public StatusPayload status_payload = new StatusPayload();
        public ProfilesPayload profiles_payload = new ProfilesPayload();
        public ServersPayload servers_payload = new ServersPayload();
        public RulesPayload rules_payload = new RulesPayload();
        public TrafficSnapshotPayload traffic_payload = new TrafficSnapshotPayload();
        public Gee.ArrayList<string> actions = new Gee.ArrayList<string>();

        public async StatusPayload status() throws Error {
            return status_payload;
        }

        public async ProfilesPayload profiles() throws Error {
            return profiles_payload;
        }

        public async ServersPayload servers() throws Error {
            return servers_payload;
        }

        public async RulesPayload rules() throws Error {
            return rules_payload;
        }

        public async TrafficSnapshotPayload traffic() throws Error {
            return traffic_payload;
        }

        public new async void connect() throws Error {
            actions.add("connect");
        }

        public new async void disconnect() throws Error {
            actions.add("disconnect");
        }

        public async void set_active_profile(string name) throws Error {
            actions.add("profile:%s".printf(name));
        }

        public async RulesPayload create_rule(RulePayload rule) throws Error {
            actions.add("rule:%s".printf(rule.name));
            rules_payload.rules.add(rule);
            return rules_payload;
        }

        public async RulesPayload create_rule_from_connection(TrafficConnectionPayload connection, RulePayload rule) throws Error {
            actions.add("connection-rule:%s:%s".printf(connection.conn_id, rule.name));
            rules_payload.rules.add(rule);
            return rules_payload;
        }

        public async RulesPayload cleanup_rule(TrafficCleanupSuggestionPayload suggestion) throws Error {
            actions.add("cleanup:%s:%s".printf(suggestion.operation, suggestion.target_rule_name));
            return rules_payload;
        }

        public async PolicyGroupsPayload policy_groups() throws Error {
            return new PolicyGroupsPayload();
        }

        public async PolicyGroupsPayload select_policy_group(string group, string chain) throws Error {
            actions.add("policy-select:%s:%s".printf(group, chain));
            return new PolicyGroupsPayload();
        }

        public async PolicyGroupsPayload test_policy_groups(string group) throws Error {
            actions.add("policy-test:%s".printf(group));
            return new PolicyGroupsPayload();
        }

        public async PromptsPayload pending_prompts() throws Error {
            return new PromptsPayload();
        }

        public async void resolve_prompt(string id, string action, string scope, bool match_host) throws Error {
            actions.add("prompt:%s:%s:%s".printf(id, action, scope));
        }

        public async DnsPayload dns() throws Error {
            return new DnsPayload();
        }

        public async DeveloperStatusPayload developer_status() throws Error {
            return new DeveloperStatusPayload();
        }

        public async DeveloperStatusPayload set_developer_capture(bool enabled) throws Error {
            actions.add("capture:%s".printf(enabled.to_string()));
            return new DeveloperStatusPayload();
        }

        public async Gee.ArrayList<DeveloperEntryPayload> developer_entries() throws Error {
            return new Gee.ArrayList<DeveloperEntryPayload>();
        }
    }

    public void add_dashboard_store_tests() {
        Test.add_func("/linux/dashboard-store/refresh-loads-status-profiles-and-servers", () => {
            var api = new FakeApi();
            api.status_payload = StatusPayload.from_json("""{"running":true,"profile":"A","listeners":[{"protocol":"socks5","addr":"127.0.0.1:1080","active_conns":3}]}""");
            api.profiles_payload = ProfilesPayload.from_json("""{"profiles":["A","B"],"active":"A"}""");
            api.servers_payload = ServersPayload.from_json("""{"profile":"A","chains":[{"name":"default","servers":[{"name":"london","address":"uk.example:443","protocol":"clambback"}]}]}""");
            api.rules_payload = RulesPayload.from_json("""{"profile":"A","rules":[{"name":"ads","action":"block","domains":["ads.example.com"]}]}""");
            api.traffic_payload = TrafficSnapshotPayload.from_json("""{"summary":{"active_connections":1,"rx_bps":2048},"connections":[{"conn_id":"c1","state":"active","target":"example.com:443"}]}""");

            var store = new DashboardStore(api);
            store.refresh_dashboard.begin((obj, res) => {
                store.refresh_dashboard.end(res);
                assert_true(store.status.running);
                assert_cmpint(store.active_connections(), CompareOperator.EQ, 3);
                assert_cmpstr(store.profiles.profiles[1], CompareOperator.EQ, "B");
                assert_cmpstr(store.servers.chains[0].servers[0].name, CompareOperator.EQ, "london");
                assert_cmpstr(store.rules.rules[0].name, CompareOperator.EQ, "ads");
                assert_cmpstr(store.traffic.connections[0].target, CompareOperator.EQ, "example.com:443");
                Test.message("dashboard refresh completed");
            });
            MainContext.default().iteration(true);
        });

        Test.add_func("/linux/dashboard-store/event-rate-and-log-retention", () => {
            var store = new DashboardStore(new FakeApi());

            for (int i = 0; i < 65; i++) {
                store.apply_event(new DaemonEvent.from_values("connection.bytes")
                    .with_number("rx_delta", (i + 1) * 1024)
                    .with_number("tx_delta", (i + 1) * 512)
                    .with_number("interval_ns", 1000000000));
            }

            assert_cmpint(store.bandwidth_samples.size, CompareOperator.EQ, BANDWIDTH_SAMPLE_LIMIT);
            assert_cmpfloat(store.current_bandwidth().rx_bps, CompareOperator.EQ, 65 * 1024);

            var connection = new TrafficConnectionPayload();
            connection.conn_id = "c1";
            store.traffic.connections.add(connection);
            store.apply_event(new DaemonEvent.from_values("connection.bytes")
                .with_string("conn_id", "c1")
                .with_number("rx_delta", 2048)
                .with_number("tx_delta", 1024)
                .with_number("interval_ns", 1000000000));
            assert_cmpfloat(store.traffic.connections[0].rx_bps, CompareOperator.EQ, 2048);
            assert_cmpstr(store.traffic.connections[0].rx_total.to_string(), CompareOperator.EQ, "2048");
            assert_cmpstr(store.traffic.summary.rx_total.to_string(), CompareOperator.EQ, "2048");

            for (int i = 0; i < 205; i++) {
                store.apply_event(new DaemonEvent.from_values("log.line").with_string("line", "line-%d".printf(i)));
            }

            assert_cmpint(store.logs.size, CompareOperator.EQ, MAX_LOG_LINES);
            assert_cmpstr(store.logs[0], CompareOperator.EQ, "line-5");
            assert_cmpstr(store.logs[199], CompareOperator.EQ, "line-204");

            store.set_log_retention(50);
            assert_cmpint(store.logs.size, CompareOperator.EQ, 50);
            assert_cmpstr(store.logs[0], CompareOperator.EQ, "line-155");
        });

        Test.add_func("/linux/dashboard-store/actions-refresh-after-change", () => {
            var api = new FakeApi();
            var store = new DashboardStore(api);

            store.connect.begin((obj, res) => {
                store.connect.end(res);
                store.disconnect.begin((obj2, res2) => {
                    store.disconnect.end(res2);
                    store.set_active_profile.begin("B", (obj3, res3) => {
                        store.set_active_profile.end(res3);
                        assert_cmpstr(api.actions[0], CompareOperator.EQ, "connect");
                        assert_cmpstr(api.actions[1], CompareOperator.EQ, "disconnect");
                        assert_cmpstr(api.actions[2], CompareOperator.EQ, "profile:B");
                    });
                });
            });

            for (int i = 0; i < 6; i++) {
                MainContext.default().iteration(true);
            }
        });

        Test.add_func("/linux/dashboard-store/create-rule-refreshes-dashboard", () => {
            var api = new FakeApi();
            var store = new DashboardStore(api);
            var rule = new RulePayload();
            rule.name = "block-example-com";
            rule.action = "block";
            rule.domains.add("example.com");

            store.create_rule.begin(rule, (obj, res) => {
                store.create_rule.end(res);
                assert_cmpstr(api.actions[0], CompareOperator.EQ, "rule:block-example-com");
                assert_cmpstr(store.rules.rules[0].name, CompareOperator.EQ, "block-example-com");
            });

            for (int i = 0; i < 4; i++) {
                MainContext.default().iteration(true);
            }
        });
    }
}
