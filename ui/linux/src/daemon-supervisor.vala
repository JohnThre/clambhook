namespace Clambhook {
    public errordomain DaemonError {
        MISSING_EXECUTABLE
    }

    public enum DaemonState {
        STOPPED,
        STARTING,
        RUNNING,
        STOPPING,
        FAILED
    }

    public class DaemonSupervisor : Object {
        private const string FLATPAK_DAEMON_PATH = "/app/libexec/clambhook";
        private Subprocess? process;
        public DaemonState state { get; private set; default = DaemonState.STOPPED; }
        public string message { get; private set; default = ""; }

        public signal void changed();

        public bool is_running {
            get {
                return process != null && !process.get_if_exited();
            }
        }

        public async void start(AppSettings settings, string token, string app_base_dir) throws Error {
            if (is_running) {
                set_state(DaemonState.RUNNING);
                return;
            }

            set_state(DaemonState.STARTING);
            try {
                var executable = resolve_executable_path(settings, app_base_dir);
                if (executable == null) {
                    throw new DaemonError.MISSING_EXECUTABLE("clambhook daemon executable was not found");
                }

                var argv = build_argv(settings, token);
                argv.insert(0, executable);
                process = new Subprocess.newv(argv.to_array(), SubprocessFlags.NONE);
                set_state(DaemonState.RUNNING);
                watch_process.begin(process);
            } catch (Error err) {
                process = null;
                set_state(DaemonState.FAILED, err.message);
                throw err;
            }
        }

        public void stop() {
            if (process != null && !process.get_if_exited()) {
                set_state(DaemonState.STOPPING);
                process.force_exit();
            }
            process = null;
            set_state(DaemonState.STOPPED);
        }

        public string state_label() {
            switch (state) {
            case DaemonState.STOPPED:
                return "Daemon stopped";
            case DaemonState.STARTING:
                return "Daemon starting";
            case DaemonState.RUNNING:
                return "Daemon running";
            case DaemonState.STOPPING:
                return "Daemon stopping";
            case DaemonState.FAILED:
                return "Daemon failed";
            default:
                return "Daemon";
            }
        }

        public bool state_is_busy() {
            return state == DaemonState.STARTING || state == DaemonState.STOPPING;
        }

        public static string default_app_base_dir() {
            try {
                return Path.get_dirname(FileUtils.read_link("/proc/self/exe"));
            } catch (FileError err) {
                return Environment.get_current_dir();
            }
        }

        public static string? resolve_executable_path(
            AppSettings settings,
            string app_base_dir,
            string? flatpak_daemon_path = null,
            bool search_path = true
        ) {
            var configured = settings.daemon_path.strip();
            if (configured != "" && FileUtils.test(configured, FileTest.EXISTS)) {
                return configured;
            }

            var flatpak = flatpak_daemon_path ?? FLATPAK_DAEMON_PATH;
            if (FileUtils.test(flatpak, FileTest.EXISTS)) {
                return flatpak;
            }

            if (search_path) {
                var path_executable = Environment.find_program_in_path("clambhook");
                if (path_executable != null && path_executable != "") {
                    return path_executable;
                }
            }

            var bundled = Path.build_filename(app_base_dir, "clambhook");
            return FileUtils.test(bundled, FileTest.EXISTS) ? bundled : null;
        }

        public static string build_arguments(AppSettings settings, string token) {
            var parts = new Gee.ArrayList<string>();
            foreach (var arg in build_argv(settings, token)) {
                parts.add(quote(arg));
            }
            return string.joinv(" ", parts.to_array());
        }

        private static Gee.ArrayList<string> build_argv(AppSettings settings, string token) {
            var normalized = settings.normalized();
            var args = new Gee.ArrayList<string>();
            args.add("-api");
            args.add(api_listen_address(normalized.api_endpoint));
            var trimmed_token = token.strip();
            if (trimmed_token != "") {
                args.add("-api-token");
                args.add(trimmed_token);
            }
            if (normalized.config_path != "") {
                args.add("-config");
                args.add(normalized.config_path);
            }
            return args;
        }

        public static string api_listen_address(string endpoint) {
            try {
                string? scheme;
                string? host;
                int port;
                if (Uri.split_network(endpoint, UriFlags.NONE, out scheme, out host, out port) && host != null && host != "") {
                    var address_host = host;
                    if (address_host.index_of(":") >= 0 && !address_host.has_prefix("[")) {
                        address_host = "[%s]".printf(address_host);
                    }
                    if (port >= 0) {
                        return "%s:%d".printf(address_host, port);
                    }
                    return address_host;
                }
            } catch (UriError err) {
            }
            return endpoint;
        }

        private static string quote(string value) {
            return "\"%s\"".printf(value.replace("\"", "\\\""));
        }

        private async void watch_process(Subprocess? watched) {
            if (watched == null) {
                return;
            }
            try {
                yield watched.wait_async(null);
            } catch (Error err) {
            }
            if (process == watched) {
                process = null;
                if (state != DaemonState.FAILED) {
                    set_state(DaemonState.STOPPED);
                }
            }
        }

        private void set_state(DaemonState next, string next_message = "") {
            state = next;
            message = next_message;
            changed();
        }
    }
}
