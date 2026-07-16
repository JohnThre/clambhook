using Gtk;

namespace Clambhook {
    public class LicenseView : Box {
        private LicenseManager license;

        private Label status_title;
        private Label status_detail;
        private Label recovery_label;
        private Label devices_label;
        private Label message_label;
        private Entry key_entry;
        private Entry email_entry;
        private Button activate_button;
        private Button deactivate_button;
        private ListBox devices_list;

        public LicenseView(LicenseManager license) {
            Object(orientation: Orientation.VERTICAL, spacing: 18);
            this.license = license;
            margin_top = 18;
            margin_bottom = 18;
            margin_start = 18;
            margin_end = 18;

            append(build_status_card());
            append(build_activation_card());
            append(build_devices_card());

            license.changed.connect(render);
            render();
        }

        private Widget build_status_card() {
            var box = new Box(Orientation.VERTICAL, 8);
            var heading = section_heading("License");

            status_title = new Label("");
            status_title.xalign = 0;
            status_title.add_css_class("title-4");
            status_detail = new Label("");
            status_detail.xalign = 0;
            status_detail.wrap = true;
            status_detail.add_css_class("dim-label");

            recovery_label = new Label("");
            recovery_label.xalign = 0;
            recovery_label.wrap = true;
            recovery_label.add_css_class("warning");
            recovery_label.visible = false;

            var frame = new Frame(null);
            var inner = new Box(Orientation.VERTICAL, 6);
            inner.margin_top = 12;
            inner.margin_bottom = 12;
            inner.margin_start = 12;
            inner.margin_end = 12;
            inner.append(status_title);
            inner.append(status_detail);
            inner.append(recovery_label);
            frame.set_child(inner);

            var actions = new Box(Orientation.HORIZONTAL, 8);
            var buy_button = new Button.with_label("Buy license");
            buy_button.add_css_class("suggested-action");
            buy_button.clicked.connect(() => open_uri(LICENSE_BUY_URL));
            var portal_button = new Button.with_label("Device portal");
            portal_button.clicked.connect(() => open_uri(LICENSE_PORTAL_URL));
            actions.append(buy_button);
            actions.append(portal_button);

            box.append(heading);
            box.append(frame);
            box.append(actions);
            return box;
        }

        private Widget build_activation_card() {
            var box = new Box(Orientation.VERTICAL, 8);
            box.append(section_heading("Activate this device"));

            key_entry = new Entry();
            key_entry.placeholder_text = "CLH-XXXXX-XXXXX-XXXXX-XXXXX";
            email_entry = new Entry();
            email_entry.placeholder_text = "you@example.com";
            email_entry.input_purpose = InputPurpose.EMAIL;
            email_entry.text = license.email;

            message_label = new Label("");
            message_label.xalign = 0;
            message_label.wrap = true;
            message_label.add_css_class("dim-label");
            message_label.visible = false;

            activate_button = new Button.with_label("Activate license");
            activate_button.add_css_class("suggested-action");
            activate_button.clicked.connect(on_activate);

            box.append(field("License key", key_entry));
            box.append(field("Email", email_entry));
            box.append(activate_button);
            box.append(message_label);

            var hint = new Label("ClambHook starts with a one-month trial. Buy a license with Creem or NOWPayments, then enter your key here. PayPal is not accepted.");
            hint.xalign = 0;
            hint.wrap = true;
            hint.add_css_class("dim-label");
            box.append(hint);
            return box;
        }

        private Widget build_devices_card() {
            var box = new Box(Orientation.VERTICAL, 8);
            box.append(section_heading("Devices"));

            devices_label = new Label("");
            devices_label.xalign = 0;
            devices_label.add_css_class("dim-label");
            box.append(devices_label);

            devices_list = new ListBox();
            devices_list.selection_mode = SelectionMode.NONE;
            var frame = new Frame(null);
            frame.set_child(devices_list);
            box.append(frame);

            deactivate_button = new Button.with_label("Deactivate this device");
            deactivate_button.clicked.connect(() => license.deactivate_current_device.begin());
            box.append(deactivate_button);
            return box;
        }

        private void on_activate() {
            var key = key_entry.text.strip();
            if (key == "") {
                return;
            }
            license.activate.begin(key, email_entry.text.strip());
        }

        private void render() {
            var decision = license.status.decision;
            status_title.label = decision.title();
            status_detail.label = decision.detail();

            var recovery = license.status.expired_trial ?? license.status.license_expired_for_updates;
            if (recovery != null && recovery.title != "") {
                recovery_label.label = recovery.detail == "" ? recovery.title : "%s — %s".printf(recovery.title, recovery.detail);
                recovery_label.visible = true;
            } else {
                recovery_label.visible = false;
            }

            message_label.label = license.message;
            message_label.visible = license.message != "";
            activate_button.sensitive = !license.loading;

            var state = license.device_state;
            devices_label.label = "%d of %d device seats active".printf(state.active_count(), state.max_active_devices);
            deactivate_button.sensitive = license.has_license_key && !license.loading;
            render_devices(state);
        }

        private void render_devices(LicenseDeviceState state) {
            var child = devices_list.get_first_child();
            while (child != null) {
                var next = child.get_next_sibling();
                devices_list.remove(child);
                child = next;
            }

            if (state.devices.size == 0) {
                devices_list.append(placeholder_row("No devices activated", "Activate a license key to register this device."));
                return;
            }
            foreach (var device in state.devices) {
                var name = device.display_name == "" ? "Device" : device.display_name;
                var status = device.active() ? "active" : "deactivated";
                var detail = "%s · %s · %s".printf(device.platform, device.architecture, status);
                devices_list.append(placeholder_row(name, detail));
            }
        }

        private static ListBoxRow placeholder_row(string title, string detail) {
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
            detail_label.add_css_class("dim-label");
            box.append(title_label);
            box.append(detail_label);
            row.set_child(box);
            return row;
        }

        private static Label section_heading(string text) {
            var label = new Label(text);
            label.xalign = 0;
            label.add_css_class("heading");
            return label;
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

        private void open_uri(string uri) {
            var launcher = new UriLauncher(uri);
            launcher.launch.begin(get_root() as Gtk.Window, null, (obj, res) => {
                try {
                    launcher.launch.end(res);
                } catch (Error err) {
                }
            });
        }
    }
}
