namespace Clambhook.Tests {
    public void add_license_service_tests() {
        Test.add_func("/linux/license/parses-status-json", () => {
            var status = LicenseStatus.from_json("""
            {
              "decision": {
                "reason": "trial",
                "trialStartDate": "2026-07-15T00:00:00Z",
                "trialEndsAt": "2026-08-15T00:00:00Z",
                "trialDaysRemaining": 31,
                "hasLifetimeUnlock": false
              },
              "productStates": [
                { "kind": "trial", "title": "Trial", "detail": "Active", "active": true }
              ]
            }
            """);

            assert_cmpstr(status.decision.reason, CompareOperator.EQ, "trial");
            assert_cmpint(status.decision.trial_days_remaining, CompareOperator.EQ, 31);
            assert_true(status.decision.can_use_app());
            assert_cmpstr(status.decision.title(), CompareOperator.EQ, "Trial active");
            assert_cmpint(status.product_states.size, CompareOperator.EQ, 1);
            assert_true(status.product_states[0].active);
        });

        Test.add_func("/linux/license/parses-device-state", () => {
            var state = device_state_from_json("""
            {
              "current_install_id": "install-1",
              "current_device_id": "device-1",
              "max_active_devices": 10,
              "payment_provider": "creem",
              "devices": [
                { "device_id": "device-1", "install_id": "install-1", "display_name": "Linux", "platform": "linux", "architecture": "x86_64", "deactivated_at": null },
                { "device_id": "device-2", "install_id": "install-2", "display_name": "Old", "platform": "linux", "architecture": "x86_64", "deactivated_at": "2026-07-15T00:00:00Z" }
              ]
            }
            """);

            assert_cmpstr(state.current_install_id, CompareOperator.EQ, "install-1");
            assert_cmpstr(state.payment_provider, CompareOperator.EQ, "creem");
            assert_cmpint(state.devices.size, CompareOperator.EQ, 2);
            assert_cmpint(state.active_count(), CompareOperator.EQ, 1);
        });

        Test.add_func("/linux/license/persists-local-state", () => {
            var temp_root = temp_dir("clambhook-linux-license-test-XXXXXX");
            var path = Path.build_filename(temp_root, "license.json");
            var store = new FileLicenseStateStore(path);
            var state = new LicensePersistedState();
            state.install_id = "install-1";
            state.email = "user@example.com";
            state.snapshot_json = "{\"reason\":\"trial\"}";
            state.grant_json = "{\"version\":1}";
            state.device_state_json = "{\"current_install_id\":\"install-1\"}";

            try {
                store.save(state);
            } catch (Error err) {
                assert_not_reached();
            }
            var loaded = store.load();
            assert_cmpstr(loaded.install_id, CompareOperator.EQ, "install-1");
            assert_cmpstr(loaded.email, CompareOperator.EQ, "user@example.com");
            assert_cmpstr(loaded.snapshot_json, CompareOperator.EQ, "{\"reason\":\"trial\"}");
        });

        Test.add_func("/linux/license/resolves-adjacent-helper", () => {
            var temp_root = temp_dir("clambhook-linux-license-helper-test-XXXXXX");
            var app_dir = Path.build_filename(temp_root, "bin");
            var helper = Path.build_filename(app_dir, "clambhook-license");
            assert_cmpint(DirUtils.create_with_parents(app_dir, 0700), CompareOperator.EQ, 0);
            try {
                FileUtils.set_contents(helper, "helper");
            } catch (Error err) {
                assert_not_reached();
            }
            assert_cmpstr(LicenseHelperClient.resolve_helper_path(app_dir, false), CompareOperator.EQ, helper);
        });
    }
}
