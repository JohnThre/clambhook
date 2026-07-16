using Gtk;

namespace Clambhook {
    // Shared helpers for the parity feature pages (Policies, Firewall, DNS,
    // Capture). Each page fetches its own data from the daemon API on refresh.
    private static Label parity_heading(string text) {
        var label = new Label(text);
        label.xalign = 0;
        label.add_css_class("heading");
        return label;
    }

    private static void parity_clear(ListBox list) {
        var child = list.get_first_child();
        while (child != null) {
            var next = child.get_next_sibling();
            list.remove(child);
            child = next;
        }
    }

    private static ListBoxRow parity_row(string title, string detail) {
        var row = new ListBoxRow();
        row.selectable = false;
        var box = new Box(Orientation.VERTICAL, 2);
        box.margin_top = 8;
        box.margin_bottom = 8;
        box.margin_start = 10;
        box.margin_end = 10;
        var title_label = new Label(title);
        title_label.xalign = 0;
        var detail_label = new Label(detail);
        detail_label.xalign = 0;
        detail_label.wrap = true;
        detail_label.add_css_class("dim-label");
        box.append(title_label);
        box.append(detail_label);
        row.set_child(box);
        return row;
    }

    // Surge-style policy groups: manual select and url-test latency display.
    public class PolicyView : Box {
        private ClambhookApiProviding api;
        private ListBox groups_list;
        private Label status_label;

        public PolicyView(ClambhookApiProviding api) {
            Object(orientation: Orientation.VERTICAL, spacing: 12);
            this.api = api;
            margin_top = 18;
            margin_bottom = 18;
            margin_start = 18;
            margin_end = 18;

            var header = new Box(Orientation.HORIZONTAL, 8);
            header.append(parity_heading("Policy groups"));
            var spacer = new Box(Orientation.HORIZONTAL, 0);
            spacer.hexpand = true;
            header.append(spacer);
            var test_button = new Button.with_label("Latency test");
            test_button.clicked.connect(() => run_test());
            header.append(test_button);
            append(header);

            status_label = new Label("Manual select and url-test policy groups from the active profile.");
            status_label.xalign = 0;
            status_label.wrap = true;
            status_label.add_css_class("dim-label");
            append(status_label);

            groups_list = new ListBox();
            groups_list.selection_mode = SelectionMode.NONE;
            var frame = new Frame(null);
            frame.set_child(groups_list);
            append(frame);
        }

        public async void refresh() {
            try {
                render(yield api.policy_groups());
            } catch (Error err) {
                status_label.label = "Policy groups unavailable: %s".printf(err.message);
            }
        }

        private void run_test() {
            status_label.label = "Running latency test...";
            api.test_policy_groups.begin("", (obj, res) => {
                try {
                    render(api.test_policy_groups.end(res));
                    status_label.label = "Latency test complete.";
                } catch (Error err) {
                    status_label.label = "Latency test failed: %s".printf(err.message);
                }
            });
        }

        private void render(PolicyGroupsPayload payload) {
            parity_clear(groups_list);
            if (payload.groups.size == 0) {
                groups_list.append(parity_row("No policy groups", "This profile routes through fixed chains only."));
                return;
            }
            foreach (var group in payload.groups) {
                if (group.hidden) {
                    continue;
                }
                groups_list.append(group_row(group));
            }
        }

        private ListBoxRow group_row(PolicyGroupPayload group) {
            var row = new ListBoxRow();
            row.selectable = false;
            var box = new Box(Orientation.VERTICAL, 6);
            box.margin_top = 10;
            box.margin_bottom = 10;
            box.margin_start = 10;
            box.margin_end = 10;

            var title = new Label("%s · %s".printf(group.name, group.group_type));
            title.xalign = 0;
            title.add_css_class("heading");
            box.append(title);

            if (group.is_select()) {
                var model = new StringList(null);
                var active = 0;
                for (var i = 0; i < group.chains.size; i++) {
                    model.append(group.chains[i]);
                    if (group.chains[i] == group.active_chain()) {
                        active = i;
                    }
                }
                var combo = new DropDown(model, null);
                if (group.chains.size > 0) {
                    combo.selected = active;
                }
                combo.notify["selected"].connect(() => {
                    if (combo.selected == Gtk.INVALID_LIST_POSITION) {
                        return;
                    }
                    var chain = model.get_string(combo.selected);
                    if (chain != null && chain != "" && chain != group.active_chain()) {
                        select_chain(group.name, chain);
                    }
                });
                box.append(combo);
            } else {
                var mode = new Label("Auto: %s".printf(group.active_chain() == "" ? "none" : group.active_chain()));
                mode.xalign = 0;
                mode.add_css_class("dim-label");
                box.append(mode);
            }

            foreach (var chain in group.chains) {
                var probe = group.result_for(chain);
                string latency;
                if (probe == null) {
                    latency = "not tested";
                } else if (!probe.healthy) {
                    latency = probe.error == "" ? "unreachable" : "unreachable (%s)".printf(probe.error);
                } else {
                    latency = "%lld ms".printf(probe.latency_ns / 1000000);
                }
                var line = new Label("%s — %s".printf(chain, latency));
                line.xalign = 0;
                line.add_css_class("dim-label");
                box.append(line);
            }

            row.set_child(box);
            return row;
        }

        private void select_chain(string group, string chain) {
            status_label.label = "Selecting %s → %s...".printf(group, chain);
            api.select_policy_group.begin(group, chain, (obj, res) => {
                try {
                    render(api.select_policy_group.end(res));
                    status_label.label = "%s now uses %s.".printf(group, chain);
                } catch (Error err) {
                    status_label.label = "Selection failed: %s".printf(err.message);
                }
            });
        }
    }

    // Little Snitch-style interactive connection prompts.
    public class FirewallView : Box {
        private ClambhookApiProviding api;
        private ListBox prompts_list;
        private Label status_label;
        private CheckButton match_host_check;

        public FirewallView(ClambhookApiProviding api) {
            Object(orientation: Orientation.VERTICAL, spacing: 12);
            this.api = api;
            margin_top = 18;
            margin_bottom = 18;
            margin_start = 18;
            margin_end = 18;

            append(parity_heading("Connection prompts"));
            status_label = new Label("Allow or block connections that no rule already decides. Enable interactive prompts in the daemon config.");
            status_label.xalign = 0;
            status_label.wrap = true;
            status_label.add_css_class("dim-label");
            append(status_label);

            match_host_check = new CheckButton.with_label("Remember rules for this host only");
            append(match_host_check);

            prompts_list = new ListBox();
            prompts_list.selection_mode = SelectionMode.NONE;
            var frame = new Frame(null);
            frame.set_child(prompts_list);
            append(frame);
        }

        public async void refresh() {
            try {
                render(yield api.pending_prompts());
            } catch (Error err) {
                status_label.label = "Prompts unavailable: %s".printf(err.message);
            }
        }

        private void render(PromptsPayload payload) {
            parity_clear(prompts_list);
            if (payload.prompts.size == 0) {
                prompts_list.append(parity_row("No pending prompts", "Undecided connections appear here for an allow/block choice."));
                return;
            }
            foreach (var prompt in payload.prompts) {
                prompts_list.append(prompt_row(prompt));
            }
        }

        private ListBoxRow prompt_row(PromptPayload prompt) {
            var row = new ListBoxRow();
            row.selectable = false;
            var box = new Box(Orientation.VERTICAL, 6);
            box.margin_top = 10;
            box.margin_bottom = 10;
            box.margin_start = 10;
            box.margin_end = 10;

            var proc = prompt.process_name == "" ? "Unknown process" : prompt.process_name;
            var title = new Label("%s → %s".printf(proc, prompt.target));
            title.xalign = 0;
            title.add_css_class("heading");
            box.append(title);

            var detail = new Label("%s · %s".printf(prompt.network == "" ? "tcp" : prompt.network, prompt.process_path));
            detail.xalign = 0;
            detail.wrap = true;
            detail.add_css_class("dim-label");
            box.append(detail);

            var actions = new Box(Orientation.HORIZONTAL, 8);
            actions.append(decision_button("Allow once", prompt.id, "allow", "once"));
            actions.append(decision_button("Allow session", prompt.id, "allow", "session"));
            actions.append(decision_button("Allow forever", prompt.id, "allow", "forever"));
            var block = decision_button("Block forever", prompt.id, "block", "forever");
            block.add_css_class("destructive-action");
            actions.append(block);
            box.append(actions);

            row.set_child(box);
            return row;
        }

        private Button decision_button(string label, string id, string action, string scope) {
            var button = new Button.with_label(label);
            button.clicked.connect(() => resolve(id, action, scope));
            return button;
        }

        private void resolve(string id, string action, string scope) {
            status_label.label = "Applying %s (%s)...".printf(action, scope);
            api.resolve_prompt.begin(id, action, scope, match_host_check.active, (obj, res) => {
                try {
                    api.resolve_prompt.end(res);
                    status_label.label = "Connection %sed (%s).".printf(action, scope);
                    refresh.begin();
                } catch (Error err) {
                    status_label.label = "Could not resolve prompt: %s".printf(err.message);
                }
            });
        }
    }

    // Encrypted DNS status (DoH/DoT/DoQ) and upstream routing.
    public class DnsView : Box {
        private ClambhookApiProviding api;
        private Label summary_label;
        private ListBox upstreams_list;

        public DnsView(ClambhookApiProviding api) {
            Object(orientation: Orientation.VERTICAL, spacing: 12);
            this.api = api;
            margin_top = 18;
            margin_bottom = 18;
            margin_start = 18;
            margin_end = 18;

            append(parity_heading("Encrypted DNS"));
            summary_label = new Label("DNS strategy for the active profile.");
            summary_label.xalign = 0;
            summary_label.wrap = true;
            summary_label.add_css_class("dim-label");
            append(summary_label);

            upstreams_list = new ListBox();
            upstreams_list.selection_mode = SelectionMode.NONE;
            var frame = new Frame(null);
            frame.set_child(upstreams_list);
            append(frame);
        }

        public async void refresh() {
            try {
                render(yield api.dns());
            } catch (Error err) {
                summary_label.label = "DNS status unavailable: %s".printf(err.message);
            }
        }

        private void render(DnsPayload payload) {
            if (!payload.enabled) {
                summary_label.label = "Encrypted DNS is off. TUN mode uses the system resolver (strategy: %s).".printf(payload.strategy == "" ? "route" : payload.strategy);
            } else {
                summary_label.label = "Encrypted DNS on · timeout %s · intercepts port 53: %s".printf(
                    payload.timeout == "" ? "default" : payload.timeout,
                    payload.intercepts_port_53 ? "yes" : "no"
                );
            }

            parity_clear(upstreams_list);
            if (payload.upstreams.size == 0) {
                upstreams_list.append(parity_row("No encrypted upstreams", "Add DoH/DoT/DoQ or a Control D resolver in the profile config."));
                return;
            }
            foreach (var upstream in payload.upstreams) {
                var name = upstream.name == "" ? upstream.protocol.up() : upstream.name;
                upstreams_list.append(parity_row("%s · %s".printf(name, upstream.protocol.up()), upstream.endpoint()));
            }
            foreach (var route in payload.upstream_routes) {
                if (route.error != "") {
                    upstreams_list.append(parity_row("Route: %s".printf(route.target), "error: %s".printf(route.error)));
                    continue;
                }
                var via = route.chain_name == "" ? route.action : "%s via %s".printf(route.action, route.chain_name);
                upstreams_list.append(parity_row("Route: %s".printf(route.target), via));
            }
        }
    }

    // Proxyman-style HTTP(S) capture status and recent transactions.
    public class CaptureView : Box {
        private ClambhookApiProviding api;
        private Label status_label;
        private Button toggle_button;
        private ListBox entries_list;
        private bool capture_enabled = false;

        public CaptureView(ClambhookApiProviding api) {
            Object(orientation: Orientation.VERTICAL, spacing: 12);
            this.api = api;
            margin_top = 18;
            margin_bottom = 18;
            margin_start = 18;
            margin_end = 18;

            var header = new Box(Orientation.HORIZONTAL, 8);
            header.append(parity_heading("HTTP(S) capture"));
            var spacer = new Box(Orientation.HORIZONTAL, 0);
            spacer.hexpand = true;
            header.append(spacer);
            toggle_button = new Button.with_label("Enable capture");
            toggle_button.clicked.connect(() => toggle());
            header.append(toggle_button);
            append(header);

            status_label = new Label("Opt-in local capture of traffic routed through the daemon HTTP proxy. HTTPS bodies require a user-trusted CA.");
            status_label.xalign = 0;
            status_label.wrap = true;
            status_label.add_css_class("dim-label");
            append(status_label);

            entries_list = new ListBox();
            entries_list.selection_mode = SelectionMode.NONE;
            var frame = new Frame(null);
            frame.set_child(entries_list);
            append(frame);
        }

        public async void refresh() {
            try {
                render(yield api.developer_status());
            } catch (Error err) {
                status_label.label = "Capture status unavailable: %s".printf(err.message);
            }
        }

        private void toggle() {
            toggle_button.sensitive = false;
            api.set_developer_capture.begin(!capture_enabled, (obj, res) => {
                try {
                    render(api.set_developer_capture.end(res));
                } catch (Error err) {
                    status_label.label = "Could not update capture: %s".printf(err.message);
                }
                toggle_button.sensitive = true;
            });
        }

        private void render(DeveloperStatusPayload status) {
            capture_enabled = status.enabled;
            toggle_button.label = status.enabled ? "Disable capture" : "Enable capture";
            var mitm = status.mitm_enabled ? "HTTPS decrypt on" : "metadata/HTTP only";
            status_label.label = status.enabled
                ? "Capture on · %s · %d/%d captured".printf(mitm, status.capture_count, status.capture_limit)
                : "Capture off. Enable to record request/response metadata for routed HTTP(S) traffic.";

            parity_clear(entries_list);
            if (!status.enabled) {
                entries_list.append(parity_row("Capture disabled", "Turn on capture to record recent transactions."));
                return;
            }
            fetch_entries();
        }

        private void fetch_entries() {
            api.developer_entries.begin((obj, res) => {
                try {
                    var entries = api.developer_entries.end(res);
                    parity_clear(entries_list);
                    if (entries.size == 0) {
                        entries_list.append(parity_row("No transactions yet", "Routed HTTP(S) requests appear here while capture is on."));
                        return;
                    }
                    foreach (var entry in entries) {
                        var method = entry.method == "" ? "GET" : entry.method;
                        var code = entry.status_code == 0 ? "pending" : entry.status_code.to_string();
                        entries_list.append(parity_row("%s %s".printf(method, entry.url == "" ? entry.host : entry.url), "status %s · %lld B".printf(code, entry.response_bytes)));
                    }
                } catch (Error err) {
                    status_label.label = "Could not load captures: %s".printf(err.message);
                }
            });
        }
    }
}
