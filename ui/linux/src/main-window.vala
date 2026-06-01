using Gtk;

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
        private DropDown profile_combo;
        private StringList profile_model;
        private bool updating_profiles = false;
        private Button connect_button;
        private Button disconnect_button;
        private Button daemon_button;
        private ListBox listeners_list;
        private ListBox servers_list;
        private ListBox traffic_list;
        private ListBox logs_list;
        private SearchEntry traffic_search;
        private DropDown traffic_filter;
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
            content.append(build_traffic_monitor());
            return content;
        }

        private Widget build_traffic_monitor() {
            var box = new Box(Orientation.VERTICAL, 8);
            var heading = new Label("Traffic Monitor");
            heading.xalign = 0;
            heading.add_css_class("heading");

            var controls = new Box(Orientation.HORIZONTAL, 8);
            traffic_filter = new DropDown(traffic_filter_model(), null);
            traffic_filter.selected = 0;
            traffic_filter.notify["selected"].connect(render_traffic);
            traffic_search = new SearchEntry();
            traffic_search.placeholder_text = "Search hosts, rules, chains";
            traffic_search.hexpand = true;
            traffic_search.search_changed.connect(render_traffic);
            controls.append(traffic_filter);
            controls.append(traffic_search);

            traffic_list = new ListBox();
            traffic_list.selection_mode = SelectionMode.NONE;
            var frame = new Frame(null);
            frame.set_child(traffic_list);
            box.append(heading);
            box.append(controls);
            box.append(frame);
            return box;
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

            profile_model = new StringList(null);
            profile_combo = new DropDown(profile_model, null);
            profile_combo.notify["selected"].connect(() => {
                if (updating_profiles || profile_combo.selected == Gtk.INVALID_LIST_POSITION) {
                    return;
                }
                var profile = profile_model.get_string(profile_combo.selected);
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
            updating_profiles = true;
            profile_model = new StringList(null);
            var active = store.active_profile();
            uint active_index = 0;
            for (int i = 0; i < store.profiles.profiles.size; i++) {
                var profile = store.profiles.profiles[i];
                profile_model.append(profile);
                if (profile == active) {
                    active_index = (uint) i;
                }
            }
            profile_combo.set_model(profile_model);
            if (store.profiles.profiles.size > 0) {
                profile_combo.set_selected(active_index);
            }
            updating_profiles = false;
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
            var filter = active_traffic_filter();
            var query = traffic_search.text.strip().down();
            var rendered = 0;
            foreach (var connection in store.traffic.connections) {
                if (!traffic_matches(connection, filter, query)) {
                    continue;
                }
                traffic_list.append(traffic_row(connection));
                rendered++;
                if (rendered >= 12) {
                    break;
                }
            }
            if (rendered == 0) {
                traffic_list.append(empty_row("No matching traffic"));
            }
        }

        private bool traffic_matches(TrafficConnectionPayload connection, string filter, string query) {
            if (filter != "all" && action_family(connection) != filter) {
                return false;
            }
            if (query == "") {
                return true;
            }
            return connection.target.down().contains(query)
                || connection.target_host.down().contains(query)
                || connection.rule_name.down().contains(query)
                || connection.rule_action.down().contains(query)
                || connection.chain_name.down().contains(query)
                || connection.application.down().contains(query)
                || connection.network.down().contains(query);
        }

        private ListBoxRow traffic_row(TrafficConnectionPayload connection) {
            var outer = new Box(Orientation.HORIZONTAL, 10);
            outer.margin_top = 8;
            outer.margin_bottom = 8;
            outer.margin_start = 10;
            outer.margin_end = 10;

            var text = new Box(Orientation.VERTICAL, 2);
            text.hexpand = true;
            var title = new Label("%s  %s".printf(action_family(connection).up(), empty_dash(connection.target)));
            title.xalign = 0;
            title.wrap = true;
            title.selectable = true;
            text.append(title);
            var secondary = new Label("%s / %s / %s down / %s up / %s".printf(
                traffic_label_for(connection),
                empty_dash(connection.rule_name),
                Formatters.format_bytes(connection.rx_total),
                Formatters.format_bytes(connection.tx_total),
                Formatters.format_duration_ns(connection.duration_ns)
            ));
            secondary.xalign = 0;
            secondary.wrap = true;
            secondary.add_css_class("dim-label");
            text.append(secondary);
            outer.append(text);

            var button = new Button.with_label("Rule");
            button.sensitive = rule_draft_from_connection(connection) != null;
            button.clicked.connect(() => show_rule_dialog(connection));
            outer.append(button);

            var row = new ListBoxRow();
            row.set_child(outer);
            return row;
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

        private static string action_family(TrafficConnectionPayload connection) {
            var action = connection.rule_action.down();
            if (action == "direct") {
                return "direct";
            }
            if (action == "block" || action == "reject") {
                return "block";
            }
            return "proxy";
        }

        private static string monitor_host(TrafficConnectionPayload connection) {
            var host = connection.target_host.strip();
            if (host == "") {
                host = connection.target;
                var idx = host.last_index_of(":");
                if (idx > 0) {
                    host = host.substring(0, idx);
                }
            }
            host = host.replace("[", "").replace("]", "").down();
            if (host.has_suffix(".")) {
                host = host.substring(0, host.length - 1);
            }
            return host;
        }

        private static RulePayload? rule_draft_from_connection(TrafficConnectionPayload connection) {
            var host = monitor_host(connection);
            if (host == "") {
                return null;
            }
            var family = action_family(connection);
            var rule = new RulePayload();
            rule.name = "%s-%s".printf(family, rule_token(host));
            if (family == "direct") {
                rule.action = "direct";
            } else if (family == "block") {
                rule.action = connection.rule_action.down() == "reject" ? "reject" : "block";
            } else {
                rule.action = connection.chain_name == "" ? "direct" : "chain:%s".printf(connection.chain_name);
            }
            if (looks_like_ipv4(host)) {
                rule.cidrs.add("%s/32".printf(host));
            } else if (host.contains(":")) {
                rule.cidrs.add("%s/128".printf(host));
            } else {
                rule.domains.add(host);
            }
            return rule;
        }

        private void show_rule_dialog(TrafficConnectionPayload connection) {
            var draft = rule_draft_from_connection(connection);
            if (draft == null) {
                return;
            }
            var win = new Window();
            win.title = "Create Rule";
            win.transient_for = this;
            win.modal = true;
            win.default_width = 420;
            var root = new Box(Orientation.VERTICAL, 12);
            root.margin_top = 18;
            root.margin_bottom = 18;
            root.margin_start = 18;
            root.margin_end = 18;
            var name = new Entry();
            name.text = draft.name;
            var action_model = new StringList(null);
            var action_values = new Gee.ArrayList<string>();
            append_dropdown_choice(action_model, action_values, "block", "Block");
            append_dropdown_choice(action_model, action_values, "direct", "Direct");
            foreach (var chain in store.servers.chains) {
                append_dropdown_choice(
                    action_model,
                    action_values,
                    "chain:%s".printf(chain.name),
                    "Proxy: %s".printf(chain.name)
                );
            }
            var action = new DropDown(action_model, null);
            action.set_selected(dropdown_index_for(action_values, draft.action));
            root.append(field_label("Name", name));
            root.append(field_label("Action", action));
            root.append(new Label("Match: %s".printf(draft.domains.size > 0 ? draft.domains[0] : draft.cidrs[0])));
            var buttons = new Box(Orientation.HORIZONTAL, 8);
            buttons.halign = Align.END;
            var cancel = new Button.with_label("Cancel");
            cancel.clicked.connect(() => win.close());
            var save = new Button.with_label("Save");
            save.add_css_class("suggested-action");
            save.clicked.connect(() => {
                draft.name = name.text.strip();
                if (action.selected != Gtk.INVALID_LIST_POSITION && (int) action.selected < action_values.size) {
                    draft.action = action_values[(int) action.selected];
                }
                store.create_rule.begin(draft);
                win.close();
            });
            buttons.append(cancel);
            buttons.append(save);
            root.append(buttons);
            win.set_child(root);
            win.present();
        }

        private static Widget field_label(string label_text, Widget control) {
            var box = new Box(Orientation.VERTICAL, 4);
            var label = new Label(label_text);
            label.xalign = 0;
            label.add_css_class("dim-label");
            box.append(label);
            box.append(control);
            return box;
        }

        private static string rule_token(string host) {
            var token = host.down()
                .replace(".", "-")
                .replace(":", "-")
                .replace("_", "-")
                .replace(" ", "-");
            return token == "" ? "connection" : token;
        }

        private static StringList traffic_filter_model() {
            var model = new StringList(null);
            model.append("All");
            model.append("Proxy");
            model.append("Direct");
            model.append("Block");
            return model;
        }

        private string active_traffic_filter() {
            switch (traffic_filter.selected) {
            case 1:
                return "proxy";
            case 2:
                return "direct";
            case 3:
                return "block";
            default:
                return "all";
            }
        }

        private static void append_dropdown_choice(StringList model, Gee.ArrayList<string> values, string value, string label) {
            values.add(value);
            model.append(label);
        }

        private static uint dropdown_index_for(Gee.ArrayList<string> values, string value) {
            for (int i = 0; i < values.size; i++) {
                if (values[i] == value) {
                    return (uint) i;
                }
            }
            return 0;
        }

        private static bool looks_like_ipv4(string host) {
            var parts = host.split(".");
            if (parts.length != 4) {
                return false;
            }
            foreach (var part in parts) {
                int value;
                if (!int.try_parse(part, out value) || value < 0 || value > 255) {
                    return false;
                }
            }
            return true;
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
