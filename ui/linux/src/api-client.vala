namespace Clambhook {
    public delegate string TokenProvider();

    public errordomain ApiClientError {
        INVALID_URL,
        HTTP_STATUS
    }

    public interface ClambhookApiProviding : Object {
        public abstract async StatusPayload status() throws Error;
        public abstract async ProfilesPayload profiles() throws Error;
        public abstract async ServersPayload servers() throws Error;
        public abstract async RulesPayload rules() throws Error;
        public abstract async TrafficSnapshotPayload traffic() throws Error;
        public abstract async void connect() throws Error;
        public abstract async void disconnect() throws Error;
        public abstract async void set_active_profile(string name) throws Error;
        public abstract async RulesPayload create_rule(RulePayload rule) throws Error;
        public abstract async RulesPayload create_rule_from_connection(TrafficConnectionPayload connection, RulePayload rule) throws Error;
        public abstract async RulesPayload cleanup_rule(TrafficCleanupSuggestionPayload suggestion) throws Error;
        public abstract async PolicyGroupsPayload policy_groups() throws Error;
        public abstract async PolicyGroupsPayload select_policy_group(string group, string chain) throws Error;
        public abstract async PolicyGroupsPayload test_policy_groups(string group) throws Error;
        public abstract async PromptsPayload pending_prompts() throws Error;
        public abstract async void resolve_prompt(string id, string action, string scope, bool match_host) throws Error;
        public abstract async DnsPayload dns() throws Error;
        public abstract async DeveloperStatusPayload developer_status() throws Error;
        public abstract async DeveloperStatusPayload set_developer_capture(bool enabled) throws Error;
        public abstract async Gee.ArrayList<DeveloperEntryPayload> developer_entries() throws Error;
        public abstract async DeveloperEntryPayload developer_entry(string id) throws Error;
        public abstract async DeveloperEntryPayload repeat_developer_entry(string id) throws Error;
    }

    public class ClambhookApiClient : Object, ClambhookApiProviding {
        private Soup.Session session;
        private string base_url;
        private TokenProvider token_provider;

        public ClambhookApiClient(string base_url, owned TokenProvider token_provider) {
            session = new Soup.Session();
            this.base_url = normalize_base_url(base_url);
            this.token_provider = (owned) token_provider;
        }

        public void configure_base_url(string base_url) {
            this.base_url = normalize_base_url(base_url);
        }

        public async StatusPayload status() throws Error {
            return StatusPayload.from_json(yield send("GET", "/api/v1/status"));
        }

        public async ProfilesPayload profiles() throws Error {
            return ProfilesPayload.from_json(yield send("GET", "/api/v1/profiles"));
        }

        public async ServersPayload servers() throws Error {
            return ServersPayload.from_json(yield send("GET", "/api/v1/servers"));
        }

        public async RulesPayload rules() throws Error {
            return RulesPayload.from_json(yield send("GET", "/api/v1/rules"));
        }

        public async TrafficSnapshotPayload traffic() throws Error {
            return TrafficSnapshotPayload.from_json(yield send("GET", "/api/v1/traffic?limit=200"));
        }

        public new async void connect() throws Error {
            yield send("POST", "/api/v1/connect");
        }

        public new async void disconnect() throws Error {
            yield send("POST", "/api/v1/disconnect");
        }

        public async void set_active_profile(string name) throws Error {
            yield send("PUT", "/api/v1/profiles/active", active_profile_body(name));
        }

        public async RulesPayload create_rule(RulePayload rule) throws Error {
            return RulesPayload.from_json(yield send("POST", "/api/v1/rules", create_rule_body(rule)));
        }

        public async RulesPayload create_rule_from_connection(TrafficConnectionPayload connection, RulePayload rule) throws Error {
            return RulesPayload.from_json(yield send("POST", "/api/v1/rules/from-connection", create_rule_from_connection_body(connection, rule)));
        }

        public async RulesPayload cleanup_rule(TrafficCleanupSuggestionPayload suggestion) throws Error {
            return RulesPayload.from_json(yield send("POST", "/api/v1/rules/cleanup", cleanup_rule_body(suggestion)));
        }

        public async PolicyGroupsPayload policy_groups() throws Error {
            return PolicyGroupsPayload.from_json(yield send("GET", "/api/v1/policy-groups"));
        }

        public async PolicyGroupsPayload select_policy_group(string group, string chain) throws Error {
            yield send("PUT", "/api/v1/policy-groups/selection", group_selection_body(group, chain));
            return yield policy_groups();
        }

        public async PolicyGroupsPayload test_policy_groups(string group) throws Error {
            return PolicyGroupsPayload.from_json(yield send("POST", "/api/v1/policy-groups/test", group_test_body(group)));
        }

        public async PromptsPayload pending_prompts() throws Error {
            return PromptsPayload.from_json(yield send("GET", "/api/v1/prompts/pending"));
        }

        public async void resolve_prompt(string id, string action, string scope, bool match_host) throws Error {
            yield send("POST", "/api/v1/prompts/%s/resolve".printf(Uri.escape_string(id, null, false)), resolve_prompt_body(action, scope, match_host));
        }

        public async DnsPayload dns() throws Error {
            return DnsPayload.from_json(yield send("GET", "/api/v1/dns"));
        }

        public async DeveloperStatusPayload developer_status() throws Error {
            return DeveloperStatusPayload.from_json(yield send("GET", "/api/v1/developer/status"));
        }

        public async DeveloperStatusPayload set_developer_capture(bool enabled) throws Error {
            yield send("PUT", "/api/v1/developer/settings", developer_capture_body(enabled));
            return yield developer_status();
        }

        public async Gee.ArrayList<DeveloperEntryPayload> developer_entries() throws Error {
            return DeveloperEntryPayload.list_from_json(yield send("GET", "/api/v1/developer/entries"));
        }

        public async DeveloperEntryPayload developer_entry(string id) throws Error {
            var path = "/api/v1/developer/entries/%s".printf(Uri.escape_string(id, null, false));
            return DeveloperEntryPayload.from_json(yield send("GET", path));
        }

        public async DeveloperEntryPayload repeat_developer_entry(string id) throws Error {
            var body = repeat_entry_body(id);
            return DeveloperEntryPayload.from_json(yield send("POST", "/api/v1/developer/repeat", body));
        }

        public static string repeat_entry_body(string id) {
            var builder = new Json.Builder();
            builder.begin_object();
            builder.set_member_name("entry_id");
            builder.add_string_value(id);
            builder.end_object();
            var generator = new Json.Generator();
            generator.set_root(builder.get_root());
            return generator.to_data(null);
        }

        public static string group_selection_body(string group, string chain) {
            var builder = new Json.Builder();
            builder.begin_object();
            builder.set_member_name("group");
            builder.add_string_value(group);
            builder.set_member_name("chain");
            builder.add_string_value(chain);
            builder.end_object();
            return json_to_string(builder);
        }

        public static string group_test_body(string group) {
            var builder = new Json.Builder();
            builder.begin_object();
            builder.set_member_name("group");
            builder.add_string_value(group);
            builder.end_object();
            return json_to_string(builder);
        }

        public static string resolve_prompt_body(string action, string scope, bool match_host) {
            var builder = new Json.Builder();
            builder.begin_object();
            builder.set_member_name("action");
            builder.add_string_value(action);
            builder.set_member_name("scope");
            builder.add_string_value(scope);
            builder.set_member_name("match_host");
            builder.add_boolean_value(match_host);
            builder.end_object();
            return json_to_string(builder);
        }

        public static string developer_capture_body(bool enabled) {
            var builder = new Json.Builder();
            builder.begin_object();
            builder.set_member_name("enabled");
            builder.add_boolean_value(enabled);
            builder.end_object();
            return json_to_string(builder);
        }

        private static string json_to_string(Json.Builder builder) {
            var generator = new Json.Generator();
            generator.set_root(builder.get_root());
            return generator.to_data(null);
        }

        public string build_uri(string path) {
            var normalized_path = path.has_prefix("/") ? path : "/" + path;
            return base_url + normalized_path;
        }

        public string events_uri() {
            var scheme = base_url.has_prefix("https://") ? "wss://" : "ws://";
            var host_and_path = base_url
                .replace("https://", "")
                .replace("http://", "");
            return "%s%s/api/v1/events?types=connection.*,rule.*,hop.*,log.*".printf(scheme, host_and_path);
        }

        public string authorization_header() {
            var token = token_provider().strip();
            return token == "" ? "" : "Bearer " + token;
        }

        public static string active_profile_body(string name) {
            var builder = new Json.Builder();
            builder.begin_object();
            builder.set_member_name("name");
            builder.add_string_value(name);
            builder.end_object();

            var generator = new Json.Generator();
            generator.set_root(builder.get_root());
            return generator.to_data(null);
        }

        public static string create_rule_body(RulePayload rule) {
            var builder = new Json.Builder();
            builder.begin_object();
            builder.set_member_name("rule");
            rule.to_json(builder);
            builder.set_member_name("position");
            builder.add_string_value("append");
            builder.end_object();

            var generator = new Json.Generator();
            generator.set_root(builder.get_root());
            return generator.to_data(null);
        }

        public static string create_rule_from_connection_body(TrafficConnectionPayload connection, RulePayload rule) {
            var builder = new Json.Builder();
            builder.begin_object();
            builder.set_member_name("conn_id");
            builder.add_string_value(connection.conn_id);
            builder.set_member_name("profile");
            builder.add_string_value(connection.profile);
            builder.set_member_name("name");
            builder.add_string_value(rule.name);
            builder.set_member_name("action");
            builder.add_string_value(rule.action);
            builder.set_member_name("scope");
            builder.add_string_value("auto");
            builder.set_member_name("position");
            builder.add_string_value("append");
            builder.end_object();

            var generator = new Json.Generator();
            generator.set_root(builder.get_root());
            return generator.to_data(null);
        }

        public static string cleanup_rule_body(TrafficCleanupSuggestionPayload suggestion) {
            var target = suggestion.target_rule_name == "" ? suggestion.rule_name : suggestion.target_rule_name;
            var builder = new Json.Builder();
            builder.begin_object();
            builder.set_member_name("profile");
            builder.add_string_value(suggestion.profile);
            builder.set_member_name("kind");
            builder.add_string_value(suggestion.kind);
            builder.set_member_name("rule_name");
            builder.add_string_value(suggestion.rule_name);
            builder.set_member_name("target_rule_name");
            builder.add_string_value(target);
            builder.set_member_name("operation");
            builder.add_string_value(suggestion.operation);
            builder.end_object();

            var generator = new Json.Generator();
            generator.set_root(builder.get_root());
            return generator.to_data(null);
        }

        private async string send(string method, string path, string? body = null) throws Error {
            var message = new Soup.Message(method, build_uri(path));
            if (message == null) {
                throw new ApiClientError.INVALID_URL("invalid URL: %s".printf(path));
            }

            var authorization = authorization_header();
            if (authorization != "") {
                message.request_headers.append("Authorization", authorization);
            }

            if (body != null) {
                message.set_request_body_from_bytes("application/json", new Bytes(body.data));
            }

            var bytes = yield session.send_and_read_async(message, Priority.DEFAULT, null);
            unowned uint8[]? data = bytes.get_data();
            if (data == null) {
                return "";
            }
            var text = (string) data;

            if (message.status_code < 200 || message.status_code > 299) {
                throw new ApiClientError.HTTP_STATUS("%u: %s".printf(message.status_code, text));
            }
            return text;
        }

        private static string normalize_base_url(string value) {
            var trimmed = value.strip();
            if (trimmed == "") {
                trimmed = "http://127.0.0.1:9090";
            }
            while (trimmed.has_suffix("/")) {
                trimmed = trimmed.substring(0, trimmed.length - 1);
            }
            return trimmed;
        }
    }
}
