namespace Clambhook {
    public const int BANDWIDTH_SAMPLE_LIMIT = 60;
    public const int MAX_LOG_LINES = 200;

    public class DashboardStore : Object {
        public StatusPayload status { get; private set; default = new StatusPayload(); }
        public ProfilesPayload profiles { get; private set; default = new ProfilesPayload(); }
        public ServersPayload servers { get; private set; default = new ServersPayload(); }
        public RulesPayload rules { get; private set; default = new RulesPayload(); }
        public TrafficSnapshotPayload traffic { get; private set; default = new TrafficSnapshotPayload(); }
        public Gee.ArrayList<BandwidthSample> bandwidth_samples { get; private set; default = new Gee.ArrayList<BandwidthSample>(); }
        public Gee.ArrayList<string> logs { get; private set; default = new Gee.ArrayList<string>(); }
        public bool api_online { get; private set; default = false; }
        public string error_text { get; private set; default = ""; }

        private ClambhookApiProviding api;
        private int log_retention;

        public signal void changed();

        public DashboardStore(ClambhookApiProviding api, int log_retention = MAX_LOG_LINES) {
            this.api = api;
            this.log_retention = clamp_log_retention(log_retention);
        }

        public string active_profile() {
            return profiles.active != "" ? profiles.active : status.profile;
        }

        public int active_connections() {
            var total = 0;
            foreach (var listener in status.listeners) {
                total += listener.active_conns;
            }
            return total;
        }

        public BandwidthSample current_bandwidth() {
            if (bandwidth_samples.size == 0) {
                return new BandwidthSample();
            }
            return bandwidth_samples[bandwidth_samples.size - 1];
        }

        public async void refresh_dashboard() {
            try {
                status = yield api.status();
                profiles = yield api.profiles();
                servers = yield api.servers();
                rules = yield api.rules();
                traffic = yield api.traffic();
                api_online = true;
                error_text = "";
            } catch (Error err) {
                api_online = false;
                error_text = err.message;
            }
            changed();
        }

        public async void refresh_status() {
            try {
                status = yield api.status();
                traffic = yield api.traffic();
                api_online = true;
                error_text = "";
            } catch (Error err) {
                api_online = false;
                error_text = err.message;
            }
            changed();
        }

        public new async void connect() {
            try {
                yield api.connect();
                yield refresh_dashboard();
            } catch (Error err) {
                api_online = false;
                error_text = err.message;
                changed();
            }
        }

        public new async void disconnect() {
            try {
                yield api.disconnect();
                yield refresh_dashboard();
            } catch (Error err) {
                api_online = false;
                error_text = err.message;
                changed();
            }
        }

        public async void set_active_profile(string name) {
            if (name == active_profile()) {
                return;
            }
            try {
                yield api.set_active_profile(name);
                yield refresh_dashboard();
            } catch (Error err) {
                api_online = false;
                error_text = err.message;
                changed();
            }
        }

        public async void create_rule(RulePayload rule) {
            try {
                rules = yield api.create_rule(rule);
                yield refresh_dashboard();
            } catch (Error err) {
                api_online = false;
                error_text = err.message;
                changed();
            }
        }

        public void apply_event(DaemonEvent event) {
            switch (event.event_type) {
            case "connection.bytes":
                apply_connection_bytes(event);
                break;
            case "log.line":
                apply_log_line(event);
                break;
            default:
                if (event.event_type.has_prefix("connection.") || event.event_type.has_prefix("rule.") || event.event_type.has_prefix("hop.")) {
                    refresh_status.begin();
                }
                break;
            }
            changed();
        }

        public void set_log_retention(int value) {
            log_retention = clamp_log_retention(value);
            trim_logs();
            changed();
        }

        public void set_error(string message) {
            if (message.strip() == "") {
                return;
            }
            error_text = message;
            changed();
        }

        private void apply_connection_bytes(DaemonEvent event) {
            var interval_ns = event.double_data("interval_ns");
            if (interval_ns <= 0) {
                return;
            }

            var seconds = interval_ns / 1000000000.0;
            bandwidth_samples.add(new BandwidthSample(
                event.double_data("rx_delta") / seconds,
                event.double_data("tx_delta") / seconds
            ));

            while (bandwidth_samples.size > BANDWIDTH_SAMPLE_LIMIT) {
                bandwidth_samples.remove_at(0);
            }
            apply_traffic_bytes(event, seconds);
        }

        private void apply_log_line(DaemonEvent event) {
            var line = event.string_data("line");
            if (line == "") {
                return;
            }
            logs.add(line);
            trim_logs();
        }

        private void apply_traffic_bytes(DaemonEvent event, double seconds) {
            var conn_id = event.string_data("conn_id");
            if (conn_id == "" || seconds <= 0) {
                return;
            }
            var rx_delta = event.double_data("rx_delta");
            var tx_delta = event.double_data("tx_delta");
            var rx_bps = rx_delta / seconds;
            var tx_bps = tx_delta / seconds;
            foreach (var connection in traffic.connections) {
                if (connection.conn_id != conn_id) {
                    continue;
                }
                var old_rx_bps = connection.rx_bps;
                var old_tx_bps = connection.tx_bps;
                connection.rx_bps = rx_bps;
                connection.tx_bps = tx_bps;
                connection.rx_total += (uint64) rx_delta;
                connection.tx_total += (uint64) tx_delta;
                traffic.summary.rx_bps += rx_bps - old_rx_bps;
                traffic.summary.tx_bps += tx_bps - old_tx_bps;
                traffic.summary.rx_total += (uint64) rx_delta;
                traffic.summary.tx_total += (uint64) tx_delta;
                return;
            }
        }

        private void trim_logs() {
            while (logs.size > log_retention) {
                logs.remove_at(0);
            }
        }

        private static int clamp_log_retention(int value) {
            if (value < MIN_LOG_RETENTION) {
                return MIN_LOG_RETENTION;
            }
            if (value > MAX_LOG_RETENTION) {
                return MAX_LOG_RETENTION;
            }
            return value;
        }
    }

}
