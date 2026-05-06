int main(string[] args) {
    Test.init(ref args);
    Clambhook.Tests.add_model_tests();
    Clambhook.Tests.add_api_client_tests();
    Clambhook.Tests.add_dashboard_store_tests();
    Clambhook.Tests.add_settings_daemon_tests();
    return Test.run();
}
