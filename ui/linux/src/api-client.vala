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
