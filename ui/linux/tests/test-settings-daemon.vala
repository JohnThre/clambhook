namespace Clambhook.Tests {
    private string temp_dir(string template) {
        try {
            return DirUtils.make_tmp(template);
        } catch (FileError err) {
            assert_not_reached();
        }
    }

    public void add_settings_daemon_tests() {
        Test.add_func("/linux/settings/normalizes-defaults-and-refresh-interval", () => {
            var settings = new AppSettings();
            settings.api_endpoint = "   ";
            settings.refresh_interval_seconds = 1;
            settings.log_retention = 5;
            settings.daemon_path = " /usr/local/bin/clambhook ";

            var normalized = settings.normalized();
            assert_cmpstr(normalized.api_endpoint, CompareOperator.EQ, "http://127.0.0.1:9090");
            assert_cmpint(normalized.refresh_interval_seconds, CompareOperator.EQ, 2);
            assert_cmpint(normalized.log_retention, CompareOperator.EQ, MIN_LOG_RETENTION);
            assert_cmpstr(normalized.daemon_path, CompareOperator.EQ, "/usr/local/bin/clambhook");
            assert_true(AppSettings.is_supported_api_endpoint("https://proxy.example.test:9443/"));
            assert_false(AppSettings.is_supported_api_endpoint("ftp://proxy.example.test"));
        });

        Test.add_func("/linux/settings/persists-json-to-config-path", () => {
            var temp_root = temp_dir("clambhook-linux-settings-test-XXXXXX");
            var path = Path.build_filename(temp_root, "settings.json");
            var store = new FileSettingsStore(path);
            var settings = new AppSettings();
            settings.api_endpoint = " http://proxy.example:9090/ ";
            settings.refresh_interval_seconds = 90;
            settings.log_retention = 900;
            settings.event_stream_enabled = false;

            try {
                store.save(settings);
            } catch (Error err) {
                assert_not_reached();
            }
            var loaded = store.load();

            assert_cmpstr(loaded.api_endpoint, CompareOperator.EQ, "http://proxy.example:9090");
            assert_cmpint(loaded.refresh_interval_seconds, CompareOperator.EQ, 60);
            assert_cmpint(loaded.log_retention, CompareOperator.EQ, MAX_LOG_RETENTION);
            assert_false(loaded.event_stream_enabled);
        });

        Test.add_func("/linux/daemon/resolves-configured-path-and-adjacent-path", () => {
            var temp_root = temp_dir("clambhook-linux-daemon-path-test-XXXXXX");
            var configured = Path.build_filename(temp_root, "configured", "clambhook");
            var app_dir = Path.build_filename(temp_root, "app");
            var adjacent = Path.build_filename(app_dir, "clambhook");

            assert_cmpint(DirUtils.create_with_parents(Path.get_dirname(configured), 0700), CompareOperator.EQ, 0);
            assert_cmpint(DirUtils.create_with_parents(app_dir, 0700), CompareOperator.EQ, 0);
            try {
                FileUtils.set_contents(configured, "configured daemon");
            } catch (Error err) {
                assert_not_reached();
            }

            var settings = new AppSettings();
            settings.daemon_path = " %s ".printf(configured);
            assert_cmpstr(DaemonSupervisor.resolve_executable_path(settings, app_dir, false), CompareOperator.EQ, configured);

            settings.daemon_path = "";
            try {
                FileUtils.set_contents(adjacent, "adjacent daemon");
            } catch (Error err) {
                assert_not_reached();
            }
            assert_cmpstr(DaemonSupervisor.resolve_executable_path(settings, app_dir, false), CompareOperator.EQ, adjacent);

            assert_cmpint(FileUtils.remove(adjacent), CompareOperator.EQ, 0);
            assert_true(DaemonSupervisor.resolve_executable_path(settings, app_dir, false) == null);

            settings.config_path = " /tmp/clambhook.toml ";
            var args = DaemonSupervisor.build_arguments(settings, " token ");
            assert_cmpstr(args, CompareOperator.EQ, "-api \"127.0.0.1:9090\" -api-token \"token\" -config \"/tmp/clambhook.toml\"");

            settings.api_endpoint = " http://[::1]:9091/ ";
            args = DaemonSupervisor.build_arguments(settings, " token ");
            assert_cmpstr(args, CompareOperator.EQ, "-api \"[::1]:9091\" -api-token \"token\" -config \"/tmp/clambhook.toml\"");
        });

        Test.add_func("/linux/formatters/formats-rates-and-server-location", () => {
            assert_cmpstr(Formatters.format_rate(512), CompareOperator.EQ, "512 B/s");
            assert_cmpstr(Formatters.format_rate(1536), CompareOperator.EQ, "1.5 KB/s");

            var server = new ServerPayload();
            server.address = "uk.example:443";
            server.geo.city = "London";
            server.geo.country = "United Kingdom";
            assert_cmpstr(Formatters.server_location(server), CompareOperator.EQ, "London, United Kingdom");
        });
    }
}
