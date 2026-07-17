namespace Clambhook {
    public class PolicyProbeResult : Object {
        public string chain_name { get; set; default = ""; }
        public bool healthy { get; set; default = false; }
        public int64 latency_ns { get; set; default = 0; }
        public int status_code { get; set; default = 0; }
        public string error { get; set; default = ""; }
        public bool udp_capable { get; set; default = false; }
    }

    public class PolicyGroupPayload : Object {
        public string name { get; set; default = ""; }
        public string group_type { get; set; default = ""; }
        public string selected { get; set; default = ""; }
        public string selected_chain { get; set; default = ""; }
        public string selection_mode { get; set; default = ""; }
        public bool hidden { get; set; default = false; }
        public Gee.ArrayList<string> chains { get; private set; default = new Gee.ArrayList<string>(); }
        public Gee.ArrayList<PolicyProbeResult> results { get; private set; default = new Gee.ArrayList<PolicyProbeResult>(); }

        public bool is_select() {
            return group_type.down() == "select";
        }

        public string active_chain() {
            if (selected_chain != "") {
                return selected_chain;
            }
            return selected;
        }

        public PolicyProbeResult? result_for(string chain) {
            foreach (var result in results) {
                if (result.chain_name == chain) {
                    return result;
                }
            }
            return null;
        }
    }

    public class PolicyGroupsPayload : Object {
        public string profile { get; set; default = ""; }
        public Gee.ArrayList<PolicyGroupPayload> groups { get; private set; default = new Gee.ArrayList<PolicyGroupPayload>(); }

        public static PolicyGroupsPayload from_json(string json) {
            try {
                return from_object(JsonReader.root_object(json));
            } catch (Error err) {
                return new PolicyGroupsPayload();
            }
        }

        public static PolicyGroupsPayload from_object(Json.Object object) {
            var payload = new PolicyGroupsPayload();
            payload.profile = JsonReader.string_member(object, "profile");
            if (JsonReader.has_array(object, "groups")) {
                var groups = object.get_array_member("groups");
                for (uint i = 0; i < groups.get_length(); i++) {
                    payload.groups.add(group_from_object(groups.get_object_element(i)));
                }
            }
            return payload;
        }

        private static PolicyGroupPayload group_from_object(Json.Object object) {
            var group = new PolicyGroupPayload();
            group.name = JsonReader.string_member(object, "name");
            group.group_type = JsonReader.string_member(object, "type");
            group.selected = JsonReader.string_member(object, "selected");
            group.selected_chain = JsonReader.string_member(object, "selected_chain");
            group.selection_mode = JsonReader.string_member(object, "selection_mode");
            group.hidden = JsonReader.bool_member(object, "hidden");
            if (JsonReader.has_array(object, "chains")) {
                var chains = object.get_array_member("chains");
                for (uint i = 0; i < chains.get_length(); i++) {
                    group.chains.add(chains.get_string_element(i));
                }
            }
            if (JsonReader.has_array(object, "results")) {
                var results = object.get_array_member("results");
                for (uint i = 0; i < results.get_length(); i++) {
                    var item = results.get_object_element(i);
                    var probe = new PolicyProbeResult();
                    probe.chain_name = JsonReader.string_member(item, "chain_name");
                    probe.healthy = JsonReader.bool_member(item, "healthy");
                    probe.latency_ns = JsonReader.int64_member(item, "latency_ns");
                    probe.status_code = JsonReader.int_member(item, "status_code");
                    probe.error = JsonReader.string_member(item, "error");
                    probe.udp_capable = JsonReader.bool_member(item, "udp_capable");
                    group.results.add(probe);
                }
            }
            return group;
        }
    }

    public class PromptPayload : Object {
        public string id { get; set; default = ""; }
        public string conn_id { get; set; default = ""; }
        public string profile { get; set; default = ""; }
        public string network { get; set; default = ""; }
        public string target { get; set; default = ""; }
        public string target_host { get; set; default = ""; }
        public string process_name { get; set; default = ""; }
        public string process_path { get; set; default = ""; }
        public int pid { get; set; default = 0; }
        public int waiters { get; set; default = 0; }
    }

    public class PromptsPayload : Object {
        public Gee.ArrayList<PromptPayload> prompts { get; private set; default = new Gee.ArrayList<PromptPayload>(); }

        public static PromptsPayload from_json(string json) {
            try {
                return from_object(JsonReader.root_object(json));
            } catch (Error err) {
                return new PromptsPayload();
            }
        }

        public static PromptsPayload from_object(Json.Object object) {
            var payload = new PromptsPayload();
            if (JsonReader.has_array(object, "prompts")) {
                var prompts = object.get_array_member("prompts");
                for (uint i = 0; i < prompts.get_length(); i++) {
                    var item = prompts.get_object_element(i);
                    var prompt = new PromptPayload();
                    prompt.id = JsonReader.string_member(item, "id");
                    prompt.conn_id = JsonReader.string_member(item, "conn_id");
                    prompt.profile = JsonReader.string_member(item, "profile");
                    prompt.network = JsonReader.string_member(item, "network");
                    prompt.target = JsonReader.string_member(item, "target");
                    prompt.target_host = JsonReader.string_member(item, "target_host");
                    prompt.process_name = JsonReader.string_member(item, "process_name");
                    prompt.process_path = JsonReader.string_member(item, "process_path");
                    prompt.pid = JsonReader.int_member(item, "pid");
                    prompt.waiters = JsonReader.int_member(item, "waiters");
                    payload.prompts.add(prompt);
                }
            }
            return payload;
        }
    }

    public class DnsUpstreamPayload : Object {
        public string name { get; set; default = ""; }
        public string protocol { get; set; default = ""; }
        public string url { get; set; default = ""; }
        public string address { get; set; default = ""; }
        public string server_name { get; set; default = ""; }

        public string endpoint() {
            if (url != "") {
                return url;
            }
            if (address != "") {
                return address;
            }
            return server_name;
        }
    }

    public class DnsRoutePayload : Object {
        public string name { get; set; default = ""; }
        public string protocol { get; set; default = ""; }
        public string target { get; set; default = ""; }
        public string action { get; set; default = ""; }
        public string chain_name { get; set; default = ""; }
        public string error { get; set; default = ""; }
    }

    public class DnsPayload : Object {
        public string profile { get; set; default = ""; }
        public string strategy { get; set; default = "route"; }
        public bool enabled { get; set; default = false; }
        public string timeout { get; set; default = ""; }
        public bool intercepts_port_53 { get; set; default = false; }
        public Gee.ArrayList<DnsUpstreamPayload> upstreams { get; private set; default = new Gee.ArrayList<DnsUpstreamPayload>(); }
        public Gee.ArrayList<DnsRoutePayload> upstream_routes { get; private set; default = new Gee.ArrayList<DnsRoutePayload>(); }

        public static DnsPayload from_json(string json) {
            try {
                return from_object(JsonReader.root_object(json));
            } catch (Error err) {
                return new DnsPayload();
            }
        }

        public static DnsPayload from_object(Json.Object object) {
            var payload = new DnsPayload();
            payload.profile = JsonReader.string_member(object, "profile");
            payload.strategy = JsonReader.string_member(object, "strategy");
            payload.enabled = JsonReader.bool_member(object, "enabled");
            payload.timeout = JsonReader.string_member(object, "timeout");
            payload.intercepts_port_53 = JsonReader.bool_member(object, "intercepts_port_53");
            if (JsonReader.has_array(object, "upstreams")) {
                var upstreams = object.get_array_member("upstreams");
                for (uint i = 0; i < upstreams.get_length(); i++) {
                    var item = upstreams.get_object_element(i);
                    var upstream = new DnsUpstreamPayload();
                    upstream.name = JsonReader.string_member(item, "name");
                    upstream.protocol = JsonReader.string_member(item, "protocol");
                    upstream.url = JsonReader.string_member(item, "url");
                    upstream.address = JsonReader.string_member(item, "address");
                    upstream.server_name = JsonReader.string_member(item, "server_name");
                    payload.upstreams.add(upstream);
                }
            }
            if (JsonReader.has_array(object, "upstream_routes")) {
                var routes = object.get_array_member("upstream_routes");
                for (uint i = 0; i < routes.get_length(); i++) {
                    var item = routes.get_object_element(i);
                    var route = new DnsRoutePayload();
                    route.name = JsonReader.string_member(item, "name");
                    route.protocol = JsonReader.string_member(item, "protocol");
                    route.target = JsonReader.string_member(item, "target");
                    route.action = JsonReader.string_member(item, "action");
                    route.chain_name = JsonReader.string_member(item, "chain_name");
                    route.error = JsonReader.string_member(item, "error");
                    payload.upstream_routes.add(route);
                }
            }
            return payload;
        }
    }

    public class DeveloperStatusPayload : Object {
        public bool enabled { get; set; default = false; }
        public bool mitm_enabled { get; set; default = false; }
        public bool no_cache_enabled { get; set; default = false; }
        public int capture_limit { get; set; default = 0; }
        public int64 body_limit_bytes { get; set; default = 0; }
        public int capture_count { get; set; default = 0; }
        public string ca_cert_path { get; set; default = ""; }
        public string ca_fingerprint_sha256 { get; set; default = ""; }

        public static DeveloperStatusPayload from_json(string json) {
            try {
                var object = JsonReader.root_object(json);
                var status = new DeveloperStatusPayload();
                status.enabled = JsonReader.bool_member(object, "enabled");
                status.mitm_enabled = JsonReader.bool_member(object, "mitm_enabled");
                status.no_cache_enabled = JsonReader.bool_member(object, "no_cache_enabled");
                status.capture_limit = JsonReader.int_member(object, "capture_limit");
                status.body_limit_bytes = JsonReader.int64_member(object, "body_limit_bytes");
                status.capture_count = JsonReader.int_member(object, "capture_count");
                status.ca_cert_path = JsonReader.string_member(object, "ca_cert_path");
                status.ca_fingerprint_sha256 = JsonReader.string_member(object, "ca_fingerprint_sha256");
                return status;
            } catch (Error err) {
                return new DeveloperStatusPayload();
            }
        }
    }

    public class DeveloperEntryPayload : Object {
        public string id { get; set; default = ""; }
        public string method { get; set; default = ""; }
        public string url { get; set; default = ""; }
        public string host { get; set; default = ""; }
        public int status_code { get; set; default = 0; }
        public int64 response_bytes { get; set; default = 0; }
        public string error { get; set; default = ""; }
        public CapturedMessage request { get; set; default = new CapturedMessage(); }
        public CapturedMessage response { get; set; default = new CapturedMessage(); }

        public static Gee.ArrayList<DeveloperEntryPayload> list_from_json(string json) {
            var list = new Gee.ArrayList<DeveloperEntryPayload>();
            try {
                var object = JsonReader.root_object(json);
                if (!JsonReader.has_array(object, "entries")) {
                    return list;
                }
                var entries = object.get_array_member("entries");
                for (uint i = 0; i < entries.get_length(); i++) {
                    list.add(entry_from_object(entries.get_object_element(i)));
                }
            } catch (Error err) {
            }
            return list;
        }

        public static DeveloperEntryPayload from_json(string json) {
            try {
                return entry_from_object(JsonReader.root_object(json));
            } catch (Error err) {
                return new DeveloperEntryPayload();
            }
        }

        private static DeveloperEntryPayload entry_from_object(Json.Object object) {
            var entry = new DeveloperEntryPayload();
            entry.id = JsonReader.string_member(object, "id");
            entry.method = JsonReader.string_member(object, "method");
            entry.url = JsonReader.string_member(object, "url");
            entry.host = JsonReader.string_member(object, "host");
            entry.status_code = JsonReader.int_member(object, "status");
            if (entry.status_code == 0) {
                entry.status_code = JsonReader.int_member(object, "status_code");
            }
            entry.response_bytes = JsonReader.int64_member(object, "response_bytes");
            entry.error = JsonReader.string_member(object, "error");
            if (JsonReader.has_object(object, "request")) {
                entry.request = message_from_object(object.get_object_member("request"));
            }
            if (JsonReader.has_object(object, "response")) {
                entry.response = message_from_object(object.get_object_member("response"));
            }
            return entry;
        }

        private static CapturedMessage message_from_object(Json.Object object) {
            var message = new CapturedMessage();
            if (JsonReader.has_object(object, "body")) {
                message.body = body_from_object(object.get_object_member("body"));
            }
            if (JsonReader.has_array(object, "headers")) {
                var headers = object.get_array_member("headers");
                for (uint i = 0; i < headers.get_length(); i++) {
                    message.headers.add(header_from_object(headers.get_object_element(i)));
                }
            }
            return message;
        }

        private static CapturedHeader header_from_object(Json.Object object) {
            var header = new CapturedHeader();
            header.name = JsonReader.string_member(object, "name");
            header.value = JsonReader.string_member(object, "value");
            header.redacted = JsonReader.bool_member(object, "redacted");
            header.truncated = JsonReader.bool_member(object, "truncated");
            return header;
        }

        private static CapturedBody body_from_object(Json.Object object) {
            var body = new CapturedBody();
            body.size = JsonReader.int64_member(object, "size");
            body.preview = JsonReader.string_member(object, "preview");
            body.preview_base64 = JsonReader.string_member(object, "preview_base64");
            body.truncated = JsonReader.bool_member(object, "truncated");
            return body;
        }
    }

    public class CapturedHeader : Object {
        public string name { get; set; default = ""; }
        public string value { get; set; default = ""; }
        public bool redacted { get; set; default = false; }
        public bool truncated { get; set; default = false; }
    }

    public class CapturedBody : Object {
        public int64 size { get; set; default = 0; }
        public string preview { get; set; default = ""; }
        public string preview_base64 { get; set; default = ""; }
        public bool truncated { get; set; default = false; }
    }

    public class CapturedMessage : Object {
        public Gee.ArrayList<CapturedHeader> headers { get; private set; default = new Gee.ArrayList<CapturedHeader>(); }
        public CapturedBody body { get; set; default = new CapturedBody(); }

        public string headers_text() {
            if (headers.size == 0) {
                return "(no headers captured)";
            }
            var builder = new StringBuilder();
            foreach (var header in headers) {
                builder.append_printf("%s: %s\n", header.name, header.value);
            }
            return builder.str;
        }

        public string body_text() {
            if (body.preview != "") {
                return body.preview;
            }
            if (body.preview_base64 != "") {
                return "[base64 preview: %s]".printf(body.preview_base64);
            }
            if (body.size == 0) {
                return "(empty body)";
            }
            return "(%lld bytes; preview unavailable)".printf(body.size);
        }
    }
}
