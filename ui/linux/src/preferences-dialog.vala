namespace Clambhook {
    public class PreferencesDialog : Window {
        private Entry api_entry;
        private Entry token_entry;
        private Entry daemon_entry;
        private Entry config_entry;
        private SpinButton refresh_spin;
        private CheckButton launch_check;
        private CheckButton stop_check;
        private CheckButton events_check;

        public signal void saved(AppSettings settings, string token);

        public PreferencesDialog(Gtk.Window parent, AppSettings settings, string token) {
            Object(title: "Settings", transient_for: parent, modal: true, default_width: 520);
            set_child(build_content(settings, token));
        }

        private Widget build_content(AppSettings settings, string token) {
            var root = new Box(Orientation.VERTICAL, 14);
            root.margin_top = 18;
            root.margin_bottom = 18;
            root.margin_start = 18;
            root.margin_end = 18;

            api_entry = entry(settings.api_endpoint);
            token_entry = entry(token);
            token_entry.visibility = false;
            daemon_entry = entry(settings.daemon_path);
            config_entry = entry(settings.config_path);
            refresh_spin = new SpinButton.with_range(2, 60, 1);
            refresh_spin.value = settings.refresh_interval_seconds;
            launch_check = new CheckButton.with_label("Launch daemon on start");
            launch_check.active = settings.launch_daemon_on_start;
            stop_check = new CheckButton.with_label("Stop daemon on quit");
            stop_check.active = settings.stop_daemon_on_exit;
            events_check = new CheckButton.with_label("Enable event stream");
            events_check.active = settings.event_stream_enabled;

            root.append(field("API endpoint", api_entry));
            root.append(field("Bearer token", token_entry));
            root.append(field("Daemon path", daemon_entry));
            root.append(field("Config path", config_entry));
            root.append(field("Refresh interval", refresh_spin));
            root.append(launch_check);
            root.append(stop_check);
            root.append(events_check);

            var actions = new Box(Orientation.HORIZONTAL, 8);
            actions.halign = Align.END;
            var cancel = new Button.with_label("Cancel");
            cancel.clicked.connect(() => close());
            var save = new Button.with_label("Save");
            save.add_css_class("suggested-action");
            save.clicked.connect(save_and_close);
            actions.append(cancel);
            actions.append(save);
            root.append(actions);
            return root;
        }

        private static Entry entry(string value) {
            var entry = new Entry();
            entry.text = value;
            return entry;
        }

        private static Widget field(string label_text, Widget control) {
            var box = new Box(Orientation.VERTICAL, 4);
            var label = new Label(label_text);
            label.xalign = 0;
            label.add_css_class("dim-label");
            box.append(label);
            box.append(control);
            return box;
        }

        private void save_and_close() {
            var settings = new AppSettings();
            settings.api_endpoint = api_entry.text;
            settings.daemon_path = daemon_entry.text;
            settings.config_path = config_entry.text;
            settings.refresh_interval_seconds = refresh_spin.get_value_as_int();
            settings.launch_daemon_on_start = launch_check.active;
            settings.stop_daemon_on_exit = stop_check.active;
            settings.event_stream_enabled = events_check.active;
            saved(settings, token_entry.text);
            close();
        }
    }
}
