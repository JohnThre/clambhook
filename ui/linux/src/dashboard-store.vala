namespace Clambhook {
    public const int BANDWIDTH_SAMPLE_LIMIT = 60;
    public const int MAX_LOG_LINES = 200;

    public class DashboardStore : Object {
        public StatusPayload status { get; private set; default = new StatusPayload(); }
        public ProfilesPayload profiles { get; private set; default = new ProfilesPayload(); }
        public ServersPayload servers { get; private set; default = new ServersPayload(); }
        public Gee.ArrayList<BandwidthSample> bandwidth_samples { get; private set; default = new Gee.ArrayList<BandwidthSample>(); }
        public Gee.ArrayList<string> logs { get; private set; default = new Gee.ArrayList<string>(); }
        public bool api_online { get; private set; default = false; }
        public string error_text { get; private set; default = ""; }

        private ClambhookApiProviding api;

        public signal void changed();

        public DashboardStore(ClambhookApiProviding api) {
            this.api = api;
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
                api_online = true;
                error_text = "";
            } catch (Error err) {
                api_online = false;
                error_text = err.message;
            }
            changed();
        }

        public async void connect() {
            try {
                yield api.connect();
                yield refresh_dashboard();
            } catch (Error err) {
                api_online = false;
                error_text = err.message;
                changed();
            }
        }

        public async void disconnect() {
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

        public void apply_event(DaemonEvent event) {
            switch (event.type) {
            case "connection.bytes":
                apply_connection_bytes(event);
                break;
            case "log.line":
                apply_log_line(event);
                break;
            default:
                break;
            }
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
        }

        private void apply_log_line(DaemonEvent event) {
            var line = event.string_data("line");
            if (line == "") {
                return;
            }
            logs.add(line);
            while (logs.size > MAX_LOG_LINES) {
                logs.remove_at(0);
            }
        }
    }

}
