namespace Clambhook {
    public errordomain DaemonError {
        MISSING_EXECUTABLE
    }

    public class DaemonSupervisor : Object {
        private Subprocess? process;
        private const string FLATPAK_DAEMON_PATH = "/app/libexec/clambhook";

        public bool is_running {
            get {
                return process != null && !process.get_if_exited();
            }
        }

        public async void start(AppSettings settings, string token, string app_base_dir) throws Error {
            if (is_running) {
                return;
            }

            var executable = resolve_executable_path(settings, app_base_dir);
            if (executable == null) {
                throw new DaemonError.MISSING_EXECUTABLE("clambhook daemon executable was not found");
            }

            var argv = build_argv(settings, token);
            argv.insert(0, executable);
            process = new Subprocess.newv(argv.to_array(), SubprocessFlags.NONE);
        }

        public void stop() {
            if (process != null && !process.get_if_exited()) {
                process.force_exit();
            }
            process = null;
        }

        public static string? resolve_executable_path(AppSettings settings, string app_base_dir, string? flatpak_daemon_path = null) {
            var configured = settings.daemon_path.strip();
            if (configured != "" && FileUtils.test(configured, FileTest.EXISTS)) {
                return configured;
            }

            var flatpak = flatpak_daemon_path ?? FLATPAK_DAEMON_PATH;
            if (FileUtils.test(flatpak, FileTest.EXISTS)) {
                return flatpak;
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

        private static string api_listen_address(string endpoint) {
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
    }
}
