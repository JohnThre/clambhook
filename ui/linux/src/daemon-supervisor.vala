namespace Clambhook {
    public errordomain DaemonError {
        MISSING_EXECUTABLE
    }

    public class DaemonSupervisor : Object {
        private Subprocess? process;

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

        public static string? resolve_executable_path(AppSettings settings, string app_base_dir) {
            var configured = settings.daemon_path.strip();
            if (configured != "" && FileUtils.test(configured, FileTest.EXISTS)) {
                return configured;
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
            args.add(normalized.api_endpoint);
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

        private static string quote(string value) {
            return "\"%s\"".printf(value.replace("\"", "\\\""));
        }
    }
}
