namespace Clambhook {
    public class ListenerStatusPayload : Object {
        public string protocol { get; set; default = ""; }
        public string addr { get; set; default = ""; }
        public int active_conns { get; set; default = 0; }
    }

    public class StatusPayload : Object {
        public bool running { get; set; default = false; }
        public string profile { get; set; default = ""; }
        public Gee.ArrayList<ListenerStatusPayload> listeners { get; private set; default = new Gee.ArrayList<ListenerStatusPayload>(); }

        public static StatusPayload from_json(string json) {
            try {
                return from_object(JsonReader.root_object(json));
            } catch (Error err) {
                return new StatusPayload();
            }
        }

        public static StatusPayload from_object(Json.Object object) {
            var status = new StatusPayload();
            status.running = JsonReader.bool_member(object, "running");
            status.profile = JsonReader.string_member(object, "profile");
            if (JsonReader.has_array(object, "listeners")) {
                var listeners = object.get_array_member("listeners");
                for (uint i = 0; i < listeners.get_length(); i++) {
                    var item = listeners.get_object_element(i);
                    var listener = new ListenerStatusPayload();
                    listener.protocol = JsonReader.string_member(item, "protocol");
                    listener.addr = JsonReader.string_member(item, "addr");
                    listener.active_conns = JsonReader.int_member(item, "active_conns");
                    status.listeners.add(listener);
                }
            }
            return status;
        }
    }

    public class ProfilesPayload : Object {
        public Gee.ArrayList<string> profiles { get; private set; default = new Gee.ArrayList<string>(); }
        public string active { get; set; default = ""; }

        public static ProfilesPayload from_json(string json) {
            try {
                return from_object(JsonReader.root_object(json));
            } catch (Error err) {
                return new ProfilesPayload();
            }
        }

        public static ProfilesPayload from_object(Json.Object object) {
            var payload = new ProfilesPayload();
            payload.active = JsonReader.string_member(object, "active");
            if (JsonReader.has_array(object, "profiles")) {
                var profiles = object.get_array_member("profiles");
                for (uint i = 0; i < profiles.get_length(); i++) {
                    payload.profiles.add(profiles.get_string_element(i));
                }
            }
            return payload;
        }
    }

    public class LocationPayload : Object {
        public string country { get; set; default = ""; }
        public string country_code { get; set; default = ""; }
        public string city { get; set; default = ""; }
        public double latitude { get; set; default = 0; }
        public double longitude { get; set; default = 0; }
    }

    public class ServerPayload : Object {
        public string name { get; set; default = ""; }
        public string address { get; set; default = ""; }
        public string protocol { get; set; default = ""; }
        public LocationPayload geo { get; set; default = new LocationPayload(); }
        public string geo_error { get; set; default = ""; }
    }

    public class ChainPayload : Object {
        public string name { get; set; default = ""; }
        public Gee.ArrayList<ServerPayload> servers { get; private set; default = new Gee.ArrayList<ServerPayload>(); }
    }

    public class ServersPayload : Object {
        public string profile { get; set; default = ""; }
        public Gee.ArrayList<ChainPayload> chains { get; private set; default = new Gee.ArrayList<ChainPayload>(); }

        public static ServersPayload from_json(string json) {
            try {
                return from_object(JsonReader.root_object(json));
            } catch (Error err) {
                return new ServersPayload();
            }
        }

        public static ServersPayload from_object(Json.Object object) {
            var payload = new ServersPayload();
            payload.profile = JsonReader.string_member(object, "profile");
            if (JsonReader.has_array(object, "chains")) {
                var chains = object.get_array_member("chains");
                for (uint i = 0; i < chains.get_length(); i++) {
                    var chain_object = chains.get_object_element(i);
                    var chain = new ChainPayload();
                    chain.name = JsonReader.string_member(chain_object, "name");
                    if (JsonReader.has_array(chain_object, "servers")) {
                        var servers = chain_object.get_array_member("servers");
                        for (uint j = 0; j < servers.get_length(); j++) {
                            chain.servers.add(server_from_object(servers.get_object_element(j)));
                        }
                    }
                    payload.chains.add(chain);
                }
            }
            return payload;
        }

        private static ServerPayload server_from_object(Json.Object object) {
            var server = new ServerPayload();
            server.name = JsonReader.string_member(object, "name");
            server.address = JsonReader.string_member(object, "address");
            server.protocol = JsonReader.string_member(object, "protocol");
            server.geo_error = JsonReader.string_member(object, "geo_error");
            if (JsonReader.has_object(object, "geo")) {
                var geo_object = object.get_object_member("geo");
                server.geo.country = JsonReader.string_member(geo_object, "country");
                server.geo.country_code = JsonReader.string_member(geo_object, "country_code");
                server.geo.city = JsonReader.string_member(geo_object, "city");
                server.geo.latitude = JsonReader.double_member(geo_object, "latitude");
                server.geo.longitude = JsonReader.double_member(geo_object, "longitude");
            }
            return server;
        }
    }

    public class EventValue : Object {
        public string string_value { get; private set; default = ""; }
        public double number_value { get; private set; default = 0; }
        public bool bool_value { get; private set; default = false; }
        private string kind = "null";

        public EventValue.string(string value) {
            string_value = value;
            kind = "string";
        }

        public EventValue.number(double value) {
            number_value = value;
            kind = "number";
        }

        public EventValue.boolean(bool value) {
            bool_value = value;
            kind = "bool";
        }

        public EventValue.null() {
            kind = "null";
        }

        public double as_double() {
            if (kind == "number") {
                return number_value;
            }
            if (kind == "string" && string_value != "") {
                return double.parse(string_value);
            }
            return 0;
        }

        public string as_string() {
            return kind == "string" ? string_value : "";
        }

        public static EventValue from_node(Json.Node node) {
            switch (node.get_value_type().name()) {
            case "gchararray":
                return new EventValue.string(node.get_string());
            case "gdouble":
            case "gint64":
            case "gint":
                return new EventValue.number(node.get_double());
            case "gboolean":
                return new EventValue.boolean(node.get_boolean());
            default:
                return new EventValue.null();
            }
        }
    }

    public class DaemonEvent : Object {
        public uint64 shard_id { get; set; default = 0; }
        public uint64 lamport { get; set; default = 0; }
        public int64 ts_ns { get; set; default = 0; }
        public string type { get; set; default = ""; }
        public Gee.HashMap<string, EventValue> data { get; private set; default = new Gee.HashMap<string, EventValue>(); }

        public DaemonEvent.from_values(string type) {
            this.type = type;
        }

        public static DaemonEvent from_json(string json) {
            try {
                var object = JsonReader.root_object(json);
                var event = new DaemonEvent.from_values(JsonReader.string_member(object, "type"));
                event.shard_id = (uint64) JsonReader.int64_member(object, "shard_id");
                event.lamport = (uint64) JsonReader.int64_member(object, "lamport");
                event.ts_ns = JsonReader.int64_member(object, "ts_ns");
                if (JsonReader.has_object(object, "data")) {
                    var data_object = object.get_object_member("data");
                    foreach (unowned string key in data_object.get_members()) {
                        event.data[key] = EventValue.from_node(data_object.get_member(key));
                    }
                }
                return event;
            } catch (Error err) {
                return new DaemonEvent.from_values("");
            }
        }

        public DaemonEvent with_number(string key, double value) {
            data[key] = new EventValue.number(value);
            return this;
        }

        public DaemonEvent with_string(string key, string value) {
            data[key] = new EventValue.string(value);
            return this;
        }

        public double double_data(string key) {
            return data.has_key(key) ? data[key].as_double() : 0;
        }

        public string string_data(string key) {
            return data.has_key(key) ? data[key].as_string() : "";
        }
    }

    public class TrafficSummaryPayload : Object {
        public int active_connections { get; set; default = 0; }
        public double rx_bps { get; set; default = 0; }
        public double tx_bps { get; set; default = 0; }
        public uint64 rx_total { get; set; default = 0; }
        public uint64 tx_total { get; set; default = 0; }
        public int history_limit { get; set; default = 0; }
        public string history_path { get; set; default = ""; }
        public bool history_persisted { get; set; default = false; }
        public string persist_error { get; set; default = ""; }
    }

    public class TrafficListenerPayload : Object {
        public string protocol { get; set; default = ""; }
        public string addr { get; set; default = ""; }
    }

    public class TrafficHopPayload : Object {
        public int index { get; set; default = 0; }
        public string name { get; set; default = ""; }
        public string protocol { get; set; default = ""; }
        public string address { get; set; default = ""; }
        public string state { get; set; default = ""; }
        public int64 elapsed_ns { get; set; default = 0; }
        public string error { get; set; default = ""; }
    }

    public class TrafficConnectionPayload : Object {
        public string conn_id { get; set; default = ""; }
        public string state { get; set; default = ""; }
        public int64 start_ts_ns { get; set; default = 0; }
        public int64 updated_ts_ns { get; set; default = 0; }
        public int64 end_ts_ns { get; set; default = 0; }
        public TrafficListenerPayload listener { get; set; default = new TrafficListenerPayload(); }
        public string client_addr { get; set; default = ""; }
        public string chain_name { get; set; default = ""; }
        public string target { get; set; default = ""; }
        public string target_host { get; set; default = ""; }
        public string target_port { get; set; default = ""; }
        public string network { get; set; default = ""; }
        public string application { get; set; default = ""; }
        public Gee.ArrayList<TrafficHopPayload> hops { get; private set; default = new Gee.ArrayList<TrafficHopPayload>(); }
        public LocationPayload geo { get; set; default = new LocationPayload(); }
        public string geo_error { get; set; default = ""; }
        public int64 total_dial_ns { get; set; default = 0; }
        public double rx_bps { get; set; default = 0; }
        public double tx_bps { get; set; default = 0; }
        public uint64 rx_total { get; set; default = 0; }
        public uint64 tx_total { get; set; default = 0; }
        public int64 duration_ns { get; set; default = 0; }
        public string close_reason { get; set; default = ""; }
    }

    public class TrafficSnapshotPayload : Object {
        public int64 updated_ts_ns { get; set; default = 0; }
        public TrafficSummaryPayload summary { get; set; default = new TrafficSummaryPayload(); }
        public Gee.ArrayList<TrafficConnectionPayload> connections { get; private set; default = new Gee.ArrayList<TrafficConnectionPayload>(); }

        public static TrafficSnapshotPayload from_json(string json) {
            try {
                return from_object(JsonReader.root_object(json));
            } catch (Error err) {
                return new TrafficSnapshotPayload();
            }
        }

        public static TrafficSnapshotPayload from_object(Json.Object object) {
            var payload = new TrafficSnapshotPayload();
            payload.updated_ts_ns = JsonReader.int64_member(object, "updated_ts_ns");
            if (JsonReader.has_object(object, "summary")) {
                payload.summary = summary_from_object(object.get_object_member("summary"));
            }
            if (JsonReader.has_array(object, "connections")) {
                var connections = object.get_array_member("connections");
                for (uint i = 0; i < connections.get_length(); i++) {
                    payload.connections.add(connection_from_object(connections.get_object_element(i)));
                }
            }
            return payload;
        }

        private static TrafficSummaryPayload summary_from_object(Json.Object object) {
            var summary = new TrafficSummaryPayload();
            summary.active_connections = JsonReader.int_member(object, "active_connections");
            summary.rx_bps = JsonReader.double_member(object, "rx_bps");
            summary.tx_bps = JsonReader.double_member(object, "tx_bps");
            summary.rx_total = (uint64) JsonReader.int64_member(object, "rx_total");
            summary.tx_total = (uint64) JsonReader.int64_member(object, "tx_total");
            summary.history_limit = JsonReader.int_member(object, "history_limit");
            summary.history_path = JsonReader.string_member(object, "history_path");
            summary.history_persisted = JsonReader.bool_member(object, "history_persisted");
            summary.persist_error = JsonReader.string_member(object, "persist_error");
            return summary;
        }

        private static TrafficConnectionPayload connection_from_object(Json.Object object) {
            var connection = new TrafficConnectionPayload();
            connection.conn_id = JsonReader.string_member(object, "conn_id");
            connection.state = JsonReader.string_member(object, "state");
            connection.start_ts_ns = JsonReader.int64_member(object, "start_ts_ns");
            connection.updated_ts_ns = JsonReader.int64_member(object, "updated_ts_ns");
            connection.end_ts_ns = JsonReader.int64_member(object, "end_ts_ns");
            if (JsonReader.has_object(object, "listener")) {
                var listener = object.get_object_member("listener");
                connection.listener.protocol = JsonReader.string_member(listener, "protocol");
                connection.listener.addr = JsonReader.string_member(listener, "addr");
            }
            connection.client_addr = JsonReader.string_member(object, "client_addr");
            connection.chain_name = JsonReader.string_member(object, "chain_name");
            connection.target = JsonReader.string_member(object, "target");
            connection.target_host = JsonReader.string_member(object, "target_host");
            connection.target_port = JsonReader.string_member(object, "target_port");
            connection.network = JsonReader.string_member(object, "network");
            connection.application = JsonReader.string_member(object, "application");
            if (JsonReader.has_array(object, "hops")) {
                var hops = object.get_array_member("hops");
                for (uint i = 0; i < hops.get_length(); i++) {
                    var hop_object = hops.get_object_element(i);
                    var hop = new TrafficHopPayload();
                    hop.index = JsonReader.int_member(hop_object, "index");
                    hop.name = JsonReader.string_member(hop_object, "name");
                    hop.protocol = JsonReader.string_member(hop_object, "protocol");
                    hop.address = JsonReader.string_member(hop_object, "address");
                    hop.state = JsonReader.string_member(hop_object, "state");
                    hop.elapsed_ns = JsonReader.int64_member(hop_object, "elapsed_ns");
                    hop.error = JsonReader.string_member(hop_object, "error");
                    connection.hops.add(hop);
                }
            }
            if (JsonReader.has_object(object, "geo")) {
                var geo_object = object.get_object_member("geo");
                connection.geo.country = JsonReader.string_member(geo_object, "country");
                connection.geo.country_code = JsonReader.string_member(geo_object, "country_code");
                connection.geo.city = JsonReader.string_member(geo_object, "city");
                connection.geo.latitude = JsonReader.double_member(geo_object, "latitude");
                connection.geo.longitude = JsonReader.double_member(geo_object, "longitude");
            }
            connection.geo_error = JsonReader.string_member(object, "geo_error");
            connection.total_dial_ns = JsonReader.int64_member(object, "total_dial_ns");
            connection.rx_bps = JsonReader.double_member(object, "rx_bps");
            connection.tx_bps = JsonReader.double_member(object, "tx_bps");
            connection.rx_total = (uint64) JsonReader.int64_member(object, "rx_total");
            connection.tx_total = (uint64) JsonReader.int64_member(object, "tx_total");
            connection.duration_ns = JsonReader.int64_member(object, "duration_ns");
            connection.close_reason = JsonReader.string_member(object, "close_reason");
            return connection;
        }
    }

    public class BandwidthSample : Object {
        public double rx_bps { get; set; default = 0; }
        public double tx_bps { get; set; default = 0; }

        public BandwidthSample(double rx_bps = 0, double tx_bps = 0) {
            this.rx_bps = rx_bps;
            this.tx_bps = tx_bps;
        }
    }

    public class JsonReader {
        public static Json.Object root_object(string json) throws Error {
            var parser = new Json.Parser();
            parser.load_from_data(json);
            return parser.get_root().get_object();
        }

        public static bool has_array(Json.Object object, string key) {
            return object.has_member(key) && object.get_member(key).get_node_type() == Json.NodeType.ARRAY;
        }

        public static bool has_object(Json.Object object, string key) {
            return object.has_member(key) && object.get_member(key).get_node_type() == Json.NodeType.OBJECT;
        }

        public static string string_member(Json.Object object, string key) {
            if (!object.has_member(key) || object.get_member(key).get_node_type() == Json.NodeType.NULL) {
                return "";
            }
            return object.get_string_member(key);
        }

        public static bool bool_member(Json.Object object, string key) {
            return object.has_member(key) && object.get_boolean_member(key);
        }

        public static int int_member(Json.Object object, string key) {
            return object.has_member(key) ? (int) object.get_int_member(key) : 0;
        }

        public static int64 int64_member(Json.Object object, string key) {
            return object.has_member(key) ? object.get_int_member(key) : 0;
        }

        public static double double_member(Json.Object object, string key) {
            return object.has_member(key) ? object.get_double_member(key) : 0;
        }
    }
}
