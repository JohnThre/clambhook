namespace Clambhook {
    public class ClambhookApplication : Adw.Application {
        private FileSettingsStore settings_store;
        private SecretTokenVault token_vault;
        private DaemonSupervisor daemon;

        public ClambhookApplication() {
            Object(application_id: "com.clambhook.Clambhook", flags: ApplicationFlags.DEFAULT_FLAGS);
            settings_store = new FileSettingsStore();
            token_vault = new SecretTokenVault();
            daemon = new DaemonSupervisor();
        }

        protected override void activate() {
            var window = active_window as MainWindow;
            if (window == null) {
                var settings = settings_store.load();
                var client = new ClambhookApiClient(settings.normalized().api_endpoint, () => window_token(window));
                var store = new DashboardStore(client);
                window = new MainWindow(this, store, client, settings_store, token_vault, daemon);
            }
            window.present();
        }

        private static string window_token(MainWindow? window) {
            return window == null ? "" : window.api_token;
        }
    }
}
