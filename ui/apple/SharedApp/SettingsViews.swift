import ClambhookShared
import SwiftUI

struct AppSettingsView: View {
    @ObservedObject var model: AppleAppModel
    @State private var endpointText = ""
    @State private var tokenText = ""
    @State private var daemonBinaryPath = ""
    @State private var daemonConfigPath = ""

    var body: some View {
        Form {
            Section("API") {
                TextField("Endpoint", text: $endpointText)
                    #if os(iOS)
                    .textInputAutocapitalization(.never)
                    .keyboardType(.URL)
                    #endif
                SecureField("Bearer token", text: $tokenText)
                Stepper(
                    "Refresh every \(Int(model.settingsStore.settings.refreshIntervalSeconds))s",
                    value: $model.settingsStore.settings.refreshIntervalSeconds,
                    in: 1...30,
                    step: 1
                )
            }
            #if os(macOS)
            Section("Daemon") {
                TextField("Daemon binary path", text: $daemonBinaryPath)
                TextField("Config path", text: $daemonConfigPath)
                Toggle("Launch daemon when app starts", isOn: $model.settingsStore.settings.launchDaemonOnStart)
                Toggle("Stop launched daemon when app quits", isOn: $model.settingsStore.settings.stopDaemonOnQuit)
            }
            #endif
            Section("History") {
                Stepper("Keep \(model.settingsStore.settings.logRetention) log lines", value: $model.settingsStore.settings.logRetention, in: 50...500, step: 50)
            }
            Section {
                Button("Apply") {
                    apply()
                }
                Button("Reset Endpoint") {
                    endpointText = defaultAPIEndpoint.absoluteString
                }
            }
        }
        .formStyle(.grouped)
        .onAppear {
            endpointText = model.settingsStore.settings.apiEndpoint.absoluteString
            tokenText = model.apiToken
            daemonBinaryPath = model.settingsStore.settings.daemonBinaryPath
            daemonConfigPath = model.settingsStore.settings.daemonConfigPath
        }
    }

    private func apply() {
        if let endpoint = URL(string: endpointText) {
            model.settingsStore.settings.apiEndpoint = endpoint
        }
        model.apiToken = tokenText
        model.settingsStore.settings.daemonBinaryPath = daemonBinaryPath
        model.settingsStore.settings.daemonConfigPath = daemonConfigPath
        model.applySettings()
    }
}
