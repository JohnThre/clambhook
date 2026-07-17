namespace Clambhook {
    public class ClambhookApplication : Adw.Application {
        private FileSettingsStore settings_store;
        private SecretTokenVault token_vault;
        private DaemonSupervisor daemon;
        private FileLicenseStateStore license_state_store;
        private SecretLicenseKeyVault license_key_vault;

        public ClambhookApplication() {
            Object(application_id: "com.clambhook.Clambhook", flags: ApplicationFlags.NON_UNIQUE);
            settings_store = new FileSettingsStore();
            token_vault = new SecretTokenVault();
            daemon = new DaemonSupervisor();
            license_state_store = new FileLicenseStateStore();
            license_key_vault = new SecretLicenseKeyVault();
        }

        protected override void activate() {
            var window = active_window as MainWindow;
            if (window == null) {
                var settings = settings_store.load().normalized();
                var client = new ClambhookApiClient(settings.api_endpoint, () => window_token(window));
                var store = new DashboardStore(client, settings.log_retention);
                var license = new LicenseManager(license_state_store, license_key_vault, build_license_helper());
                window = new MainWindow(this, store, client, settings_store, token_vault, daemon, license);
            }
            window.present();
        }

        private static LicenseHelperClient build_license_helper() {
            var base_dir = DaemonSupervisor.default_app_base_dir();
            var helper_path = LicenseHelperClient.resolve_helper_path(base_dir);
            return new LicenseHelperClient(helper_path ?? "");
        }

        private static string window_token(MainWindow? window) {
            return window == null ? "" : window.api_token;
        }
    }
}
