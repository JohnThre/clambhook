namespace Clambhook {
    public const string LICENSE_VALIDATION_BASE_URL = "https://store.swiphtgroup.com/clambhook/license";
    public const string LICENSE_BUY_URL = "https://store.swiphtgroup.com/clambhook/buy/";
    public const string LICENSE_PORTAL_URL = "https://store.swiphtgroup.com/clambhook/portal/";

    public errordomain LicenseError {
        MISSING_HELPER,
        HELPER_FAILED,
        INVALID_RESPONSE
    }

    public class LicenseDecision : Object {
        public string reason { get; set; default = "locked"; }
        public string trial_start_date { get; set; default = ""; }
        public string trial_ends_at { get; set; default = ""; }
        public int trial_days_remaining { get; set; default = 0; }
        public bool has_lifetime_unlock { get; set; default = false; }
        public string update_cutoff_date { get; set; default = ""; }
        public string offline_grace_ends_at { get; set; default = ""; }

        public bool can_use_app() {
            return reason != "locked";
        }

        public string title() {
            switch (reason) {
            case "trial":
                return "Trial active";
            case "lifetime":
                return "Licensed";
            case "offlineGrace":
                return "Licensed (offline grace)";
            default:
                return "License required";
            }
        }

        public string detail() {
            switch (reason) {
            case "trial":
                return "%d days left in the one-month trial".printf(trial_days_remaining);
            case "lifetime":
                return update_cutoff_date == "" ? "Updates included during your entitlement window" : "Updates included through %s".printf(short_date(update_cutoff_date));
            case "offlineGrace":
                return offline_grace_ends_at == "" ? "Using cached license while verification is unavailable" : "Offline grace until %s".printf(short_date(offline_grace_ends_at));
            default:
                return "Activate a license key to continue using ClambHook.";
            }
        }
    }

    public class LicenseProductState : Object {
        public string kind { get; set; default = ""; }
        public string title { get; set; default = ""; }
        public string detail { get; set; default = ""; }
        public bool active { get; set; default = false; }
    }

    public class LicenseRecoveryState : Object {
        public string kind { get; set; default = ""; }
        public string severity { get; set; default = ""; }
        public string title { get; set; default = ""; }
        public string detail { get; set; default = ""; }
        public string primary_action { get; set; default = ""; }
    }

    public class LicenseDevice : Object {
        public string device_id { get; set; default = ""; }
        public string install_id { get; set; default = ""; }
        public string display_name { get; set; default = ""; }
        public string platform { get; set; default = ""; }
        public string architecture { get; set; default = ""; }
        public string app_version { get; set; default = ""; }
        public string activated_at { get; set; default = ""; }
        public string last_seen_at { get; set; default = ""; }
        public string deactivated_at { get; set; default = ""; }

        public bool active() {
            return deactivated_at == "";
        }
    }

    public class LicenseDeviceState : Object {
        public string current_install_id { get; set; default = ""; }
        public string current_device_id { get; set; default = ""; }
        public int max_active_devices { get; set; default = 10; }
        public string payment_provider { get; set; default = ""; }
        public Gee.ArrayList<LicenseDevice> devices { get; private set; default = new Gee.ArrayList<LicenseDevice>(); }

        public int active_count() {
            var count = 0;
            foreach (var device in devices) {
                if (device.active()) {
                    count++;
                }
            }
            return count;
        }
    }

    public class LicenseStatus : Object {
        public LicenseDecision decision { get; set; default = new LicenseDecision(); }
        public Gee.ArrayList<LicenseProductState> product_states { get; private set; default = new Gee.ArrayList<LicenseProductState>(); }
        public LicenseRecoveryState? expired_trial { get; set; default = null; }
        public LicenseRecoveryState? license_expired_for_updates { get; set; default = null; }

        public static LicenseStatus from_json(string json) {
            try {
                var object = JsonReader.root_object(json);
                var status = new LicenseStatus();
                if (JsonReader.has_object(object, "decision")) {
                    status.decision = decision_from_object(object.get_object_member("decision"));
                }
                if (JsonReader.has_array(object, "productStates")) {
                    var rows = object.get_array_member("productStates");
                    for (uint i = 0; i < rows.get_length(); i++) {
                        status.product_states.add(product_state_from_object(rows.get_object_element(i)));
                    }
                }
                if (JsonReader.has_object(object, "expiredTrial")) {
                    status.expired_trial = recovery_from_object(object.get_object_member("expiredTrial"));
                }
                if (JsonReader.has_object(object, "licenseExpiredForUpdates")) {
                    status.license_expired_for_updates = recovery_from_object(object.get_object_member("licenseExpiredForUpdates"));
                }
                return status;
            } catch (Error err) {
                return new LicenseStatus();
            }
        }
    }

    public class LicensePersistedState : Object {
        public string install_id { get; set; default = ""; }
        public string email { get; set; default = ""; }
        public string snapshot_json { get; set; default = ""; }
        public string grant_json { get; set; default = ""; }
        public string device_state_json { get; set; default = ""; }
    }

    public interface LicenseStateStore : Object {
        public abstract LicensePersistedState load();
        public abstract void save(LicensePersistedState state) throws Error;
        public abstract string daemon_snapshot_path();
    }

    public class FileLicenseStateStore : Object, LicenseStateStore {
        private string path;

        public FileLicenseStateStore(string? path = null) {
            this.path = path ?? default_path();
        }

        public LicensePersistedState load() {
            try {
                string data;
                FileUtils.get_contents(path, out data);
                var object = JsonReader.root_object(data);
                var state = new LicensePersistedState();
                state.install_id = JsonReader.string_member(object, "installId");
                state.email = JsonReader.string_member(object, "email");
                state.snapshot_json = JsonReader.string_member(object, "snapshotJson");
                state.grant_json = JsonReader.string_member(object, "grantJson");
                state.device_state_json = JsonReader.string_member(object, "deviceStateJson");
                return state;
            } catch (Error err) {
                return new LicensePersistedState();
            }
        }

        public void save(LicensePersistedState state) throws Error {
            var parent = Path.get_dirname(path);
            DirUtils.create_with_parents(parent, 0700);
            FileUtils.set_contents(path, to_json(state));
            export_daemon_snapshot(state);
        }

        /// Writes the raw license snapshot JSON to a standalone file the
        /// daemon reads via its `--license` flag for defense-in-depth
        /// enforcement. Returns the file path.
        public string daemon_snapshot_path() {
            return Path.build_filename(Path.get_dirname(path), "license-snapshot.json");
        }

        private void export_daemon_snapshot(LicensePersistedState state) throws Error {
            var snapshot = state.snapshot_json.strip();
            if (snapshot == "") {
                snapshot = "{}";
            }
            FileUtils.set_contents(daemon_snapshot_path(), snapshot);
        }

        public static string default_path() {
            return Path.build_filename(Environment.get_user_config_dir(), "clambhook", "linux-license.json");
        }

        private static string to_json(LicensePersistedState state) {
            var builder = new Json.Builder();
            builder.begin_object();
            builder.set_member_name("installId");
            builder.add_string_value(state.install_id);
            builder.set_member_name("email");
            builder.add_string_value(state.email);
            builder.set_member_name("snapshotJson");
            builder.add_string_value(state.snapshot_json);
            builder.set_member_name("grantJson");
            builder.add_string_value(state.grant_json);
            builder.set_member_name("deviceStateJson");
            builder.add_string_value(state.device_state_json);
            builder.end_object();

            var generator = new Json.Generator();
            generator.pretty = true;
            generator.set_root(builder.get_root());
            return generator.to_data(null);
        }
    }

    public interface LicenseKeyVault : Object {
        public abstract async string read_license_key() throws Error;
        public abstract async void save_license_key(string license_key) throws Error;
    }

    public class SecretLicenseKeyVault : Object, LicenseKeyVault {
        private const string SCHEMA_NAME = "com.clambhook.Clambhook.LicenseKey";
        private const string ACCOUNT = "default";

        public async string read_license_key() throws Error {
            var attrs = attributes();
            string? value = yield Secret.password_lookupv(schema(), attrs, null);
            return value ?? "";
        }

        public async void save_license_key(string license_key) throws Error {
            var attrs = attributes();
            var trimmed = license_key.strip();
            if (trimmed == "") {
                yield Secret.password_clearv(schema(), attrs, null);
                return;
            }
            yield Secret.password_storev(
                schema(),
                attrs,
                Secret.COLLECTION_DEFAULT,
                "ClambHook license key",
                trimmed,
                null
            );
        }

        private static Secret.Schema schema() {
            return new Secret.Schema(
                SCHEMA_NAME,
                Secret.SchemaFlags.NONE,
                "account",
                Secret.SchemaAttributeType.STRING
            );
        }

        private static HashTable<string, string> attributes() {
            var attrs = new HashTable<string, string>(str_hash, str_equal);
            attrs.insert("account", ACCOUNT);
            return attrs;
        }
    }

    public class LicenseHelperClient : Object {
        private string helper_path;

        public LicenseHelperClient(string helper_path) {
            this.helper_path = helper_path;
        }

        public static string? resolve_helper_path(string app_base_dir, bool search_path = true) {
            if (search_path) {
                var path_executable = Environment.find_program_in_path("clambhook-license");
                if (path_executable != null && path_executable != "") {
                    return path_executable;
                }
            }

            var adjacent = Path.build_filename(app_base_dir, "clambhook-license");
            if (FileUtils.test(adjacent, FileTest.EXISTS)) {
                return adjacent;
            }

            var sibling_libexec = Path.build_filename(Path.get_dirname(app_base_dir), "libexec", "clambhook-license");
            if (FileUtils.test(sibling_libexec, FileTest.EXISTS)) {
                return sibling_libexec;
            }

            var usr_libexec = "/usr/libexec/clambhook-license";
            if (FileUtils.test(usr_libexec, FileTest.EXISTS)) {
                return usr_libexec;
            }

            return null;
        }

        public async string call(string command, Json.Builder request_builder) throws Error {
            if (helper_path.strip() == "") {
                throw new LicenseError.MISSING_HELPER("clambhook-license helper was not found");
            }

            var generator = new Json.Generator();
            generator.set_root(request_builder.get_root());
            var request = generator.to_data(null);
            string[] argv = { helper_path, null };
            var process = new Subprocess.newv(argv, SubprocessFlags.STDIN_PIPE | SubprocessFlags.STDOUT_PIPE | SubprocessFlags.STDERR_PIPE);
            string? stdout_text;
            string? stderr_text;
            yield process.communicate_utf8_async(request, null, out stdout_text, out stderr_text);
            if (!process.get_successful()) {
                throw new LicenseError.HELPER_FAILED(stderr_text ?? "clambhook-license failed");
            }

            var response = JsonReader.root_object(stdout_text ?? "");
            if (!JsonReader.bool_member(response, "ok")) {
                var message = JsonReader.string_member(response, "error");
                throw new LicenseError.HELPER_FAILED(message == "" ? "%s failed".printf(command) : message);
            }
            return JsonReader.string_member(response, "result");
        }

        public static Json.Builder request(string command) {
            var builder = new Json.Builder();
            builder.begin_object();
            builder.set_member_name("command");
            builder.add_string_value(command);
            return builder;
        }
    }

    public class LicenseManager : Object {
        private LicenseStateStore state_store;
        private LicenseKeyVault key_vault;
        private LicenseHelperClient helper;
        private LicensePersistedState persisted;

        public LicenseStatus status { get; private set; default = new LicenseStatus(); }
        public LicenseDeviceState device_state { get; private set; default = new LicenseDeviceState(); }
        public bool has_license_key { get; private set; default = false; }
        public bool loading { get; private set; default = false; }
        public bool initialized { get; private set; default = false; }
        public string email { get; private set; default = ""; }
        public string message { get; private set; default = ""; }

        public string daemon_snapshot_path() {
            return state_store.daemon_snapshot_path();
        }
        public signal void changed();

        public LicenseManager(LicenseStateStore state_store, LicenseKeyVault key_vault, LicenseHelperClient helper) {
            this.state_store = state_store;
            this.key_vault = key_vault;
            this.helper = helper;
            this.persisted = state_store.load();
            this.email = persisted.email;
        }

        public async void start() {
            loading = true;
            changed();
            try {
                if (persisted.install_id == "") {
                    persisted.install_id = yield install_id();
                }
                persisted.snapshot_json = yield ensure_trial(persisted.snapshot_json);
                state_store.save(persisted);
                has_license_key = (yield key_vault.read_license_key()).strip() != "";
                yield refresh();
                initialized = true;
            } catch (Error err) {
                message = err.message;
                initialized = true;
            }
            loading = false;
            changed();
        }

        public async void refresh() throws Error {
            status = LicenseStatus.from_json(yield status_json(persisted.snapshot_json));
            device_state = persisted.device_state_json == "" ? new LicenseDeviceState() : device_state_from_json(persisted.device_state_json);
            email = persisted.email;
            changed();
        }

        public async void activate(string license_key, string email) {
            loading = true;
            message = "Activating license...";
            changed();
            try {
                var result = yield activate_json(license_key, email);
                apply_applied_payload(result);
                persisted.email = email.strip();
                yield key_vault.save_license_key(license_key);
                state_store.save(persisted);
                has_license_key = true;
                message = "License activated on this GNU/Linux device.";
                yield refresh();
            } catch (Error err) {
                try {
                    persisted.snapshot_json = yield mark_verification_failure(persisted.snapshot_json);
                    state_store.save(persisted);
                    yield refresh();
                } catch (Error ignored) {
                }
                message = err.message;
            }
            loading = false;
            changed();
        }

        public async void deactivate_current_device() {
            yield device_action("deactivate", "This device was deactivated.");
        }

        public async void reactivate_current_device() {
            yield device_action("reactivate", "This device was reactivated.");
        }

        public async void transfer_current_device() {
            yield device_action("transfer", "This device was deactivated; the seat is available to transfer.");
        }

        private async void device_action(string action, string success_message) {
            loading = true;
            message = "Updating device seat...";
            changed();
            try {
                var key = yield key_vault.read_license_key();
                if (key.strip() == "") {
                    throw new LicenseError.INVALID_RESPONSE("Enter a license key before managing devices.");
                }
                var result = yield device_action_json(action, key);
                apply_applied_payload(result);
                state_store.save(persisted);
                message = success_message;
                yield refresh();
            } catch (Error err) {
                message = err.message;
            }
            loading = false;
            changed();
        }

        private async string install_id() throws Error {
            var builder = LicenseHelperClient.request("install-id");
            builder.end_object();
            return yield helper.call("install-id", builder);
        }

        private async string ensure_trial(string snapshot_json) throws Error {
            var builder = LicenseHelperClient.request("ensure-trial");
            builder.set_member_name("snapshot");
            builder.add_string_value(snapshot_json);
            builder.end_object();
            return yield helper.call("ensure-trial", builder);
        }

        private async string status_json(string snapshot_json) throws Error {
            var builder = LicenseHelperClient.request("status");
            builder.set_member_name("snapshot");
            builder.add_string_value(snapshot_json);
            builder.end_object();
            return yield helper.call("status", builder);
        }

        private async string activate_json(string license_key, string email) throws Error {
            var builder = LicenseHelperClient.request("activate");
            builder.set_member_name("baseURL");
            builder.add_string_value(LICENSE_VALIDATION_BASE_URL);
            builder.set_member_name("licenseKey");
            builder.add_string_value(license_key);
            builder.set_member_name("email");
            builder.add_string_value(email);
            builder.set_member_name("deviceRegistration");
            builder.add_string_value(device_registration_json());
            builder.end_object();
            return yield helper.call("activate", builder);
        }

        private async string device_action_json(string action, string license_key) throws Error {
            var builder = LicenseHelperClient.request("device-action");
            builder.set_member_name("baseURL");
            builder.add_string_value(LICENSE_VALIDATION_BASE_URL);
            builder.set_member_name("action");
            builder.add_string_value(action);
            builder.set_member_name("licenseKey");
            builder.add_string_value(license_key);
            builder.set_member_name("installID");
            builder.add_string_value(persisted.install_id);
            builder.set_member_name("deviceID");
            builder.add_string_value(device_state.current_device_id);
            builder.set_member_name("deviceRegistration");
            builder.add_string_value(device_registration_json());
            builder.end_object();
            return yield helper.call("device-action", builder);
        }

        private async string mark_verification_failure(string snapshot_json) throws Error {
            var builder = LicenseHelperClient.request("mark-verification-failure");
            builder.set_member_name("snapshot");
            builder.add_string_value(snapshot_json);
            builder.end_object();
            var result = yield helper.call("mark-verification-failure", builder);
            try {
                var object = JsonReader.root_object(result);
                if (JsonReader.has_object(object, "snapshot")) {
                    return node_to_string(object.get_member("snapshot"));
                }
            } catch (Error err) {
            }
            return snapshot_json;
        }

        private void apply_applied_payload(string result) throws Error {
            var object = JsonReader.root_object(result);
            if (JsonReader.has_object(object, "snapshot")) {
                persisted.snapshot_json = node_to_string(object.get_member("snapshot"));
            }
            if (object.has_member("grant")) {
                persisted.grant_json = node_to_string(object.get_member("grant"));
            }
            if (JsonReader.has_object(object, "deviceState")) {
                persisted.device_state_json = node_to_string(object.get_member("deviceState"));
            }
        }

        private string device_registration_json() {
            var builder = new Json.Builder();
            builder.begin_object();
            builder.set_member_name("install_id");
            builder.add_string_value(persisted.install_id);
            builder.set_member_name("display_name");
            builder.add_string_value(device_display_name());
            builder.set_member_name("platform");
            builder.add_string_value("linux");
            builder.set_member_name("architecture");
            builder.add_string_value(device_architecture());
            builder.set_member_name("app_version");
            builder.add_string_value("0.1.0");
            builder.end_object();
            var generator = new Json.Generator();
            generator.set_root(builder.get_root());
            return generator.to_data(null);
        }
    }

    private static LicenseDecision decision_from_object(Json.Object object) {
        var decision = new LicenseDecision();
        decision.reason = JsonReader.string_member(object, "reason");
        decision.trial_start_date = JsonReader.string_member(object, "trialStartDate");
        decision.trial_ends_at = JsonReader.string_member(object, "trialEndsAt");
        decision.trial_days_remaining = JsonReader.int_member(object, "trialDaysRemaining");
        decision.has_lifetime_unlock = JsonReader.bool_member(object, "hasLifetimeUnlock");
        decision.update_cutoff_date = JsonReader.string_member(object, "updateCutoffDate");
        decision.offline_grace_ends_at = JsonReader.string_member(object, "offlineGraceEndsAt");
        return decision;
    }

    private static LicenseProductState product_state_from_object(Json.Object object) {
        var state = new LicenseProductState();
        state.kind = JsonReader.string_member(object, "kind");
        state.title = JsonReader.string_member(object, "title");
        state.detail = JsonReader.string_member(object, "detail");
        state.active = JsonReader.bool_member(object, "active");
        return state;
    }

    private static LicenseRecoveryState recovery_from_object(Json.Object object) {
        var state = new LicenseRecoveryState();
        state.kind = JsonReader.string_member(object, "kind");
        state.severity = JsonReader.string_member(object, "severity");
        state.title = JsonReader.string_member(object, "title");
        state.detail = JsonReader.string_member(object, "detail");
        state.primary_action = JsonReader.string_member(object, "primaryAction");
        return state;
    }

    private static LicenseDeviceState device_state_from_json(string json) {
        try {
            var object = JsonReader.root_object(json);
            var state = new LicenseDeviceState();
            state.current_install_id = JsonReader.string_member(object, "current_install_id");
            state.current_device_id = JsonReader.string_member(object, "current_device_id");
            state.max_active_devices = JsonReader.int_member(object, "max_active_devices");
            state.payment_provider = JsonReader.string_member(object, "payment_provider");
            if (JsonReader.has_array(object, "devices")) {
                var devices = object.get_array_member("devices");
                for (uint i = 0; i < devices.get_length(); i++) {
                    state.devices.add(device_from_object(devices.get_object_element(i)));
                }
            }
            return state;
        } catch (Error err) {
            return new LicenseDeviceState();
        }
    }

    private static LicenseDevice device_from_object(Json.Object object) {
        var device = new LicenseDevice();
        device.device_id = JsonReader.string_member(object, "device_id");
        device.install_id = JsonReader.string_member(object, "install_id");
        device.display_name = JsonReader.string_member(object, "display_name");
        device.platform = JsonReader.string_member(object, "platform");
        device.architecture = JsonReader.string_member(object, "architecture");
        device.app_version = JsonReader.string_member(object, "app_version");
        device.activated_at = JsonReader.string_member(object, "activated_at");
        device.last_seen_at = JsonReader.string_member(object, "last_seen_at");
        device.deactivated_at = JsonReader.string_member(object, "deactivated_at");
        return device;
    }

    private static string node_to_string(Json.Node node) {
        var generator = new Json.Generator();
        generator.set_root(node);
        return generator.to_data(null);
    }

    private static string short_date(string iso) {
        return iso.length >= 10 ? iso.substring(0, 10) : iso;
    }

    private static string device_display_name() {
        var host = Environment.get_host_name();
        return host == null || host.strip() == "" ? "GNU/Linux device" : host.strip();
    }

    private static string device_architecture() {
        var arch = Environment.get_variable("HOSTTYPE");
        if (arch == null || arch.strip() == "") {
            arch = Environment.get_variable("MACHTYPE");
        }
        return arch == null || arch.strip() == "" ? "unknown" : arch.strip();
    }
}
