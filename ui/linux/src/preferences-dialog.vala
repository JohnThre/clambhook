namespace Clambhook {
    public class PreferencesDialog : Window {
        private Entry api_entry;
        private Entry token_entry;
        private Entry daemon_entry;
        private Entry config_entry;
        private SpinButton refresh_spin;
        private SpinButton log_spin;
        private CheckButton launch_check;
        private CheckButton stop_check;
        private CheckButton events_check;
        private Label validation_label;
        private Button save_button;

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
            log_spin = new SpinButton.with_range(MIN_LOG_RETENTION, MAX_LOG_RETENTION, 50);
            log_spin.value = settings.log_retention;
            launch_check = new CheckButton.with_label("Launch daemon on start");
            launch_check.active = settings.launch_daemon_on_start;
            stop_check = new CheckButton.with_label("Stop daemon on quit");
            stop_check.active = settings.stop_daemon_on_exit;
            events_check = new CheckButton.with_label("Enable event stream");
            events_check.active = settings.event_stream_enabled;
            validation_label = new Label("");
            validation_label.xalign = 0;
            validation_label.add_css_class("error");
            validation_label.visible = false;
            api_entry.changed.connect(validate);

            root.append(field("API endpoint", api_entry));
            root.append(validation_label);
            root.append(field("Bearer token", token_entry));
            root.append(path_field("Daemon path", daemon_entry, "Choose clambhook daemon"));
            root.append(path_field("Config path", config_entry, "Choose clambhook config"));
            root.append(field("Refresh interval", refresh_spin));
            root.append(field("Log retention", log_spin));
            root.append(launch_check);
            root.append(stop_check);
            root.append(events_check);

            var actions = new Box(Orientation.HORIZONTAL, 8);
            actions.halign = Align.END;
            var cancel = new Button.with_label("Cancel");
            cancel.clicked.connect(() => close());
            save_button = new Button.with_label("Save");
            save_button.add_css_class("suggested-action");
            save_button.clicked.connect(save_and_close);
            actions.append(cancel);
            actions.append(save_button);
            root.append(actions);
            validate();
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

        private Widget path_field(string label_text, Entry control, string title) {
            var row = new Box(Orientation.HORIZONTAL, 8);
            control.hexpand = true;
            var browse = new Button.from_icon_name("folder-open-symbolic");
            browse.tooltip_text = title;
            browse.clicked.connect(() => choose_file(title, control));
            row.append(control);
            row.append(browse);
            return field(label_text, row);
        }

        private void choose_file(string title, Entry target) {
            var chooser = new FileChooserNative(title, this, FileChooserAction.OPEN, "Select", "Cancel");
            chooser.response.connect((response) => {
                if (response == ResponseType.ACCEPT) {
                    var file = chooser.get_file();
                    if (file != null && file.get_path() != null) {
                        target.text = file.get_path();
                    }
                }
                chooser.destroy();
            });
            chooser.show();
        }

        private void validate() {
            var valid = AppSettings.is_supported_api_endpoint(api_entry.text);
            validation_label.label = valid ? "" : "Use an http:// or https:// endpoint with a host.";
            validation_label.visible = !valid;
            if (save_button != null) {
                save_button.sensitive = valid;
            }
        }

        private void save_and_close() {
            if (!AppSettings.is_supported_api_endpoint(api_entry.text)) {
                validate();
                return;
            }
            var settings = new AppSettings();
            settings.api_endpoint = api_entry.text;
            settings.daemon_path = daemon_entry.text;
            settings.config_path = config_entry.text;
            settings.refresh_interval_seconds = refresh_spin.get_value_as_int();
            settings.log_retention = log_spin.get_value_as_int();
            settings.launch_daemon_on_start = launch_check.active;
            settings.stop_daemon_on_exit = stop_check.active;
            settings.event_stream_enabled = events_check.active;
            saved(settings, token_entry.text);
            close();
        }
    }
}
