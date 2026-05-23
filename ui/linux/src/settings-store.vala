namespace Clambhook {
    public const int MIN_LOG_RETENTION = 50;
    public const int MAX_LOG_RETENTION = 500;

    public class AppSettings : Object {
        public string api_endpoint { get; set; default = "http://127.0.0.1:9090"; }
        public string daemon_path { get; set; default = ""; }
        public string config_path { get; set; default = ""; }
        public bool launch_daemon_on_start { get; set; default = false; }
        public bool stop_daemon_on_exit { get; set; default = true; }
        public bool event_stream_enabled { get; set; default = true; }
        public int refresh_interval_seconds { get; set; default = 5; }
        public int log_retention { get; set; default = 200; }

        public AppSettings normalized() {
            var next = new AppSettings();
            next.api_endpoint = is_supported_api_endpoint(api_endpoint) ? normalize_endpoint(api_endpoint) : "http://127.0.0.1:9090";
            next.daemon_path = daemon_path.strip();
            next.config_path = config_path.strip();
            next.launch_daemon_on_start = launch_daemon_on_start;
            next.stop_daemon_on_exit = stop_daemon_on_exit;
            next.event_stream_enabled = event_stream_enabled;
            next.refresh_interval_seconds = clamp_int(refresh_interval_seconds, 2, 60);
            next.log_retention = clamp_int(log_retention, MIN_LOG_RETENTION, MAX_LOG_RETENTION);
            return next;
        }

        public static bool is_supported_api_endpoint(string value) {
            var normalized = normalize_endpoint(value);
            return (normalized.has_prefix("http://") || normalized.has_prefix("https://"))
                && DaemonSupervisor.api_listen_address(normalized) != normalized;
        }

        private static string normalize_endpoint(string value) {
            var trimmed = value.strip();
            if (trimmed == "") {
                return "http://127.0.0.1:9090";
            }
            while (trimmed.has_suffix("/")) {
                trimmed = trimmed.substring(0, trimmed.length - 1);
            }
            return trimmed;
        }

        private static int clamp_int(int value, int low, int high) {
            if (value < low) {
                return low;
            }
            if (value > high) {
                return high;
            }
            return value;
        }
    }

    public interface SettingsStore : Object {
        public abstract AppSettings load();
        public abstract void save(AppSettings settings) throws Error;
    }

    public class FileSettingsStore : Object, SettingsStore {
        private string path;

        public FileSettingsStore(string? path = null) {
            this.path = path ?? default_path();
        }

        public AppSettings load() {
            try {
                string data;
                FileUtils.get_contents(path, out data);
                return from_json(data).normalized();
            } catch (Error err) {
                return new AppSettings();
            }
        }

        public void save(AppSettings settings) throws Error {
            var normalized = settings.normalized();
            var parent = Path.get_dirname(path);
            DirUtils.create_with_parents(parent, 0700);
            FileUtils.set_contents(path, to_json(normalized));
        }

        public static string default_path() {
            return Path.build_filename(Environment.get_user_config_dir(), "clambhook", "linux-settings.json");
        }

        private static AppSettings from_json(string data) throws Error {
            var object = JsonReader.root_object(data);
            var settings = new AppSettings();
            settings.api_endpoint = JsonReader.string_member(object, "apiEndpoint");
            settings.daemon_path = JsonReader.string_member(object, "daemonPath");
            settings.config_path = JsonReader.string_member(object, "configPath");
            settings.launch_daemon_on_start = JsonReader.bool_member(object, "launchDaemonOnStart");
            settings.stop_daemon_on_exit = !object.has_member("stopDaemonOnExit") || JsonReader.bool_member(object, "stopDaemonOnExit");
            settings.event_stream_enabled = !object.has_member("eventStreamEnabled") || JsonReader.bool_member(object, "eventStreamEnabled");
            settings.refresh_interval_seconds = object.has_member("refreshIntervalSeconds")
                ? JsonReader.int_member(object, "refreshIntervalSeconds")
                : 5;
            settings.log_retention = object.has_member("logRetention")
                ? JsonReader.int_member(object, "logRetention")
                : 200;
            return settings;
        }

        private static string to_json(AppSettings settings) {
            var builder = new Json.Builder();
            builder.begin_object();
            builder.set_member_name("apiEndpoint");
            builder.add_string_value(settings.api_endpoint);
            builder.set_member_name("daemonPath");
            builder.add_string_value(settings.daemon_path);
            builder.set_member_name("configPath");
            builder.add_string_value(settings.config_path);
            builder.set_member_name("launchDaemonOnStart");
            builder.add_boolean_value(settings.launch_daemon_on_start);
            builder.set_member_name("stopDaemonOnExit");
            builder.add_boolean_value(settings.stop_daemon_on_exit);
            builder.set_member_name("eventStreamEnabled");
            builder.add_boolean_value(settings.event_stream_enabled);
            builder.set_member_name("refreshIntervalSeconds");
            builder.add_int_value(settings.refresh_interval_seconds);
            builder.set_member_name("logRetention");
            builder.add_int_value(settings.log_retention);
            builder.end_object();

            var generator = new Json.Generator();
            generator.pretty = true;
            generator.set_root(builder.get_root());
            return generator.to_data(null);
        }
    }
}
