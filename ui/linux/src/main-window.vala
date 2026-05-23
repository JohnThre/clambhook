namespace Clambhook {
    public class MainWindow : Adw.ApplicationWindow {
        private DashboardStore store;
        private ClambhookApiClient client;
        private FileSettingsStore settings_store;
        private TokenVault token_vault;
        private DaemonSupervisor daemon;
        private EventStreamClient event_stream;
        private AppSettings settings;

        private Label status_label;
        private Label daemon_label;
        private Label profile_label;
        private Label api_label;
        private Label error_label;
        private Label daemon_message_label;
        private Label connections_label;
        private Label bandwidth_label;
        private Label traffic_label;
        private ComboBoxText profile_combo;
        private Button connect_button;
        private Button disconnect_button;
        private Button daemon_button;
        private ListBox listeners_list;
        private ListBox servers_list;
        private ListBox traffic_list;
        private ListBox logs_list;
        private uint refresh_source = 0;
        private uint event_reconnect_source = 0;
        private bool closing = false;
        private bool event_stream_active = false;

        public string api_token { get; private set; default = ""; }

        public MainWindow(
            Gtk.Application app,
            DashboardStore store,
            ClambhookApiClient client,
            FileSettingsStore settings_store,
            TokenVault token_vault,
            DaemonSupervisor daemon
        ) {
            Object(application: app, title: "clambhook", default_width: 960, default_height: 720);
            this.store = store;
            this.client = client;
            this.settings_store = settings_store;
            this.token_vault = token_vault;
            this.daemon = daemon;
            this.event_stream = new EventStreamClient();
            this.settings = settings_store.load().normalized();

            set_child(build_content());
            store.changed.connect(render);
            daemon.changed.connect(render);
            event_stream.event_received.connect((event) => store.apply_event(event));
            event_stream.stream_failed.connect((message) => {
                store.set_error("events: %s".printf(message));
                schedule_event_reconnect();
            });
            event_stream.closed.connect(() => {
                if (event_stream_active && !closing) {
                    schedule_event_reconnect();
                }
            });
            close_request.connect(on_close_request);

            token_vault.read_token.begin((obj, res) => {
                try {
                    api_token = token_vault.read_token.end(res);
                } catch (Error err) {
                    api_token = "";
                }
                maybe_launch_daemon();
                refresh_now();
                schedule_refresh();
                start_event_stream();
            });
        }

        private Widget build_content() {
            var root = new Box(Orientation.VERTICAL, 0);

            var header = new Adw.HeaderBar();
            var title = new Adw.WindowTitle("clambhook", "Linux controller");
            header.set_title_widget(title);

            var refresh_button = new Button.from_icon_name("view-refresh-symbolic");
            refresh_button.tooltip_text = "Refresh";
            refresh_button.clicked.connect(refresh_now);
            header.pack_start(refresh_button);

            daemon_button = new Button.with_label("Start daemon");
            daemon_button.clicked.connect(toggle_daemon);
            header.pack_start(daemon_button);

            var preferences_button = new Button.from_icon_name("emblem-system-symbolic");
            preferences_button.tooltip_text = "Settings";
            preferences_button.clicked.connect(open_preferences);
            header.pack_end(preferences_button);
            root.append(header);

            var scroller = new ScrolledWindow();
            scroller.vexpand = true;
            scroller.set_child(build_dashboard());
            root.append(scroller);
            return root;
        }

        private Widget build_dashboard() {
            var content = new Box(Orientation.VERTICAL, 18);
            content.margin_top = 18;
            content.margin_bottom = 18;
            content.margin_start = 18;
            content.margin_end = 18;

            var status_frame = new Frame(null);
            status_frame.set_child(build_status_panel());
            content.append(status_frame);

            var lists = new Box(Orientation.HORIZONTAL, 18);
            lists.homogeneous = true;
            lists.append(wrap_list("Listeners", out listeners_list));
            lists.append(wrap_list("Servers", out servers_list));
            content.append(lists);

            content.append(wrap_list("Recent logs", out logs_list));
            content.append(wrap_list("Traffic", out traffic_list));
            return content;
        }

        private Widget build_status_panel() {
            var grid = new Grid();
            grid.column_spacing = 16;
            grid.row_spacing = 12;
            grid.margin_top = 16;
            grid.margin_bottom = 16;
            grid.margin_start = 16;
            grid.margin_end = 16;

            status_label = value_label("Stopped");
            daemon_label = value_label("Daemon stopped");
            profile_label = value_label("No profile");
            api_label = value_label("API offline");
            error_label = value_label("");
            error_label.add_css_class("error");
            error_label.visible = false;
            daemon_message_label = value_label("");
            daemon_message_label.add_css_class("dim-label");
            daemon_message_label.visible = false;
            connections_label = value_label("0 active connections");
            bandwidth_label = value_label("0 B/s down / 0 B/s up");
            traffic_label = value_label("0 active · 0 B down / 0 B up");

            grid.attach(caption_label("Status"), 0, 0, 1, 1);
            grid.attach(status_label, 1, 0, 1, 1);
            grid.attach(caption_label("Daemon"), 0, 1, 1, 1);
            grid.attach(daemon_label, 1, 1, 1, 1);
            grid.attach(caption_label("Profile"), 0, 2, 1, 1);
            grid.attach(profile_label, 1, 2, 1, 1);
            grid.attach(caption_label("API"), 0, 3, 1, 1);
            grid.attach(api_label, 1, 3, 1, 1);
            grid.attach(caption_label("Connections"), 0, 4, 1, 1);
            grid.attach(connections_label, 1, 4, 1, 1);
            grid.attach(caption_label("Bandwidth"), 0, 5, 1, 1);
            grid.attach(bandwidth_label, 1, 5, 1, 1);
            grid.attach(caption_label("Traffic"), 0, 6, 1, 1);
            grid.attach(traffic_label, 1, 6, 1, 1);
            grid.attach(error_label, 0, 7, 3, 1);
            grid.attach(daemon_message_label, 0, 8, 3, 1);

            profile_combo = new ComboBoxText();
            profile_combo.changed.connect(() => {
                var profile = profile_combo.get_active_text();
                if (profile != null && profile != "") {
                    store.set_active_profile.begin(profile);
                }
            });
            grid.attach(profile_combo, 2, 0, 1, 1);

            var actions = new Box(Orientation.HORIZONTAL, 8);
            connect_button = new Button.with_label("Connect");
            connect_button.clicked.connect(() => store.connect.begin());
            disconnect_button = new Button.with_label("Disconnect");
            disconnect_button.clicked.connect(() => store.disconnect.begin());
            actions.append(connect_button);
            actions.append(disconnect_button);
            grid.attach(actions, 2, 1, 1, 1);
            return grid;
        }

        private Widget wrap_list(string title, out ListBox list) {
            var box = new Box(Orientation.VERTICAL, 8);
            var heading = new Label(title);
            heading.xalign = 0;
            heading.add_css_class("heading");
            list = new ListBox();
            list.selection_mode = SelectionMode.NONE;
            var frame = new Frame(null);
            frame.set_child(list);
            box.append(heading);
            box.append(frame);
            return box;
        }

        private static Label caption_label(string text) {
            var label = new Label(text);
            label.xalign = 0;
            label.add_css_class("dim-label");
            return label;
        }

        private static Label value_label(string text) {
            var label = new Label(text);
            label.xalign = 0;
            label.wrap = true;
            return label;
        }

        private void refresh_now() {
            store.refresh_dashboard.begin();
        }

        private void schedule_refresh() {
            if (refresh_source != 0) {
                Source.remove(refresh_source);
            }
            refresh_source = Timeout.add_seconds((uint) settings.refresh_interval_seconds, () => {
                store.refresh_status.begin();
                return Source.CONTINUE;
            });
        }

        private void render() {
            status_label.label = store.status.running ? "Running" : "Stopped";
            daemon_label.label = daemon.state_label();
            profile_label.label = store.active_profile() == "" ? "No profile" : store.active_profile();
            api_label.label = store.api_online ? "API online" : "API offline";
            error_label.label = store.error_text;
            error_label.visible = store.error_text != "";
            daemon_message_label.label = daemon.message;
            daemon_message_label.visible = daemon.message != "";
            connections_label.label = "%d active connections".printf(store.active_connections());
            var bandwidth = store.current_bandwidth();
            bandwidth_label.label = "%s down / %s up".printf(
                Formatters.format_rate(bandwidth.rx_bps),
                Formatters.format_rate(bandwidth.tx_bps)
            );
            traffic_label.label = "%d active · %s down / %s up · %s down total / %s up total".printf(
                store.traffic.summary.active_connections,
                Formatters.format_rate(store.traffic.summary.rx_bps),
                Formatters.format_rate(store.traffic.summary.tx_bps),
                Formatters.format_bytes(store.traffic.summary.rx_total),
                Formatters.format_bytes(store.traffic.summary.tx_total)
            );
            connect_button.sensitive = store.api_online && !store.status.running;
            disconnect_button.sensitive = store.status.running;
            daemon_button.label = daemon.is_running ? "Stop daemon" : "Start daemon";
            daemon_button.sensitive = !daemon.state_is_busy();

            render_profiles();
            render_listeners();
            render_servers();
            render_traffic();
            render_logs();
        }

        private void render_profiles() {
            profile_combo.remove_all();
            var active = store.active_profile();
            var active_index = 0;
            for (int i = 0; i < store.profiles.profiles.size; i++) {
                var profile = store.profiles.profiles[i];
                profile_combo.append_text(profile);
                if (profile == active) {
                    active_index = i;
                }
            }
            if (store.profiles.profiles.size > 0) {
                profile_combo.set_active(active_index);
            }
        }

        private void render_listeners() {
            clear_list(listeners_list);
            if (store.status.listeners.size == 0) {
                listeners_list.append(empty_row("No listeners"));
                return;
            }
            foreach (var listener in store.status.listeners) {
                listeners_list.append(detail_row(
                    listener.protocol.up(),
                    "%s / %d active".printf(listener.addr, listener.active_conns),
                    "network-wired-symbolic"
                ));
            }
        }

        private void render_servers() {
            clear_list(servers_list);
            if (store.servers.chains.size == 0) {
                servers_list.append(empty_row("No servers in active profile"));
                return;
            }
            foreach (var chain in store.servers.chains) {
                foreach (var server in chain.servers) {
                    servers_list.append(detail_row(
                        server.name,
                        "%s / %s / %s".printf(chain.name, server.protocol, Formatters.server_location(server)),
                        "network-server-symbolic"
                    ));
                }
            }
        }

        private void render_logs() {
            clear_list(logs_list);
            if (store.logs.size == 0) {
                logs_list.append(empty_row("No log events"));
                return;
            }
            var start = store.logs.size > 12 ? store.logs.size - 12 : 0;
            for (int i = start; i < store.logs.size; i++) {
                logs_list.append(detail_row(store.logs[i], "", "text-x-generic-symbolic"));
            }
        }

        private void render_traffic() {
            clear_list(traffic_list);
            if (store.traffic.connections.size == 0) {
                traffic_list.append(empty_row("No traffic history"));
                return;
            }
            var count = store.traffic.connections.size > 12 ? 12 : store.traffic.connections.size;
            for (int i = 0; i < count; i++) {
                var connection = store.traffic.connections[i];
                var label = traffic_label_for(connection);
                traffic_list.append(detail_row(
                    empty_dash(connection.target),
                    "%s / %s / %s down / %s up / %s".printf(
                        connection.state,
                        label,
                        Formatters.format_bytes(connection.rx_total),
                        Formatters.format_bytes(connection.tx_total),
                        Formatters.format_duration_ns(connection.duration_ns)
                    ),
                    "view-list-symbolic"
                ));
            }
        }

        private static string traffic_label_for(TrafficConnectionPayload connection) {
            if (connection.application != "") {
                return connection.application;
            }
            if (connection.network != "") {
                return connection.network;
            }
            if (connection.chain_name != "") {
                return connection.chain_name;
            }
            return connection.listener.protocol;
        }

        private static string empty_dash(string value) {
            return value.strip() == "" ? "--" : value;
        }

        private static ListBoxRow empty_row(string text) {
            var label = new Label(text);
            label.xalign = 0;
            label.wrap = true;
            label.add_css_class("dim-label");
            label.margin_top = 8;
            label.margin_bottom = 8;
            label.margin_start = 10;
            label.margin_end = 10;
            var row = new ListBoxRow();
            row.set_child(label);
            return row;
        }

        private static ListBoxRow detail_row(string primary, string secondary, string icon_name) {
            var outer = new Box(Orientation.HORIZONTAL, 10);
            outer.margin_top = 8;
            outer.margin_bottom = 8;
            outer.margin_start = 10;
            outer.margin_end = 10;

            var icon = new Image.from_icon_name(icon_name);
            icon.pixel_size = 18;
            icon.add_css_class("dim-label");
            outer.append(icon);

            var text = new Box(Orientation.VERTICAL, 2);
            text.hexpand = true;
            var primary_label = new Label(primary);
            primary_label.xalign = 0;
            primary_label.wrap = true;
            primary_label.selectable = true;
            text.append(primary_label);
            if (secondary != "") {
                var secondary_label = new Label(secondary);
                secondary_label.xalign = 0;
                secondary_label.wrap = true;
                secondary_label.add_css_class("dim-label");
                text.append(secondary_label);
            }
            outer.append(text);

            var row = new ListBoxRow();
            row.set_child(outer);
            return row;
        }

        private static void clear_list(ListBox list) {
            Widget? child = list.get_first_child();
            while (child != null) {
                var next = child.get_next_sibling();
                list.remove(child);
                child = next;
            }
        }

        private void toggle_daemon() {
            if (daemon.is_running) {
                daemon.stop();
                render();
                return;
            }
            start_daemon();
        }

        private void maybe_launch_daemon() {
            if (settings.launch_daemon_on_start) {
                start_daemon();
            }
        }

        private void start_daemon() {
            daemon.start.begin(settings, api_token, DaemonSupervisor.default_app_base_dir(), (obj, res) => {
                try {
                    daemon.start.end(res);
                    store.refresh_dashboard.begin();
                } catch (Error err) {
                    store.set_error(err.message);
                }
                render();
            });
        }

        private void open_preferences() {
            var dialog = new PreferencesDialog(this, settings, api_token);
            dialog.saved.connect((next_settings, next_token) => {
                try {
                    settings_store.save(next_settings);
                    settings = next_settings.normalized();
                    store.set_log_retention(settings.log_retention);
                } catch (Error err) {
                    return;
                }
                token_vault.save_token.begin(next_token, (obj, res) => {
                    try {
                        token_vault.save_token.end(res);
                        api_token = next_token.strip();
                        client.configure_base_url(settings.api_endpoint);
                        schedule_refresh();
                        start_event_stream();
                        refresh_now();
                    } catch (Error err) {
                    }
                });
            });
            dialog.present();
        }

        private void start_event_stream() {
            stop_event_stream();
            if (!settings.event_stream_enabled) {
                return;
            }
            event_stream_active = true;
            event_stream.start(client.events_uri(), client.authorization_header());
        }

        private void stop_event_stream(bool cancel_connection = true) {
            event_stream_active = false;
            if (event_reconnect_source != 0) {
                Source.remove(event_reconnect_source);
                event_reconnect_source = 0;
            }
            if (cancel_connection) {
                event_stream.stop();
            }
        }

        private void schedule_event_reconnect() {
            if (closing || !settings.event_stream_enabled || event_reconnect_source != 0) {
                return;
            }
            event_reconnect_source = Timeout.add_seconds(3, () => {
                event_reconnect_source = 0;
                start_event_stream();
                return Source.REMOVE;
            });
        }

        private bool on_close_request() {
            closing = true;
            stop_event_stream();
            if (refresh_source != 0) {
                Source.remove(refresh_source);
                refresh_source = 0;
            }
            if (settings.stop_daemon_on_exit) {
                daemon.stop();
            }
            return false;
        }
    }
}
