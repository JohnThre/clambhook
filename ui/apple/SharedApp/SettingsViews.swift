import ClambhookShared
import SwiftUI
#if os(macOS)
import AppKit
#endif

struct AppSettingsView: View {
    @ObservedObject var model: AppleAppModel
    @State private var endpointText = ""
    @State private var tokenText = ""
    @State private var daemonBinaryPath = ""
    @State private var daemonConfigPath = ""
    @State private var daemonBinaryBookmark: Data?
    @State private var daemonConfigBookmark: Data?

    var body: some View {
        Form {
            Section("API") {
                TextField("Endpoint", text: $endpointText)
                    #if os(iOS)
                    .textInputAutocapitalization(.never)
                    .keyboardType(.URL)
                    #endif
                if let error = endpointValidationMessage(endpointText) {
                    Text(error)
                        .font(.caption)
                        .foregroundStyle(.red)
                }
                SecureField("Bearer token", text: $tokenText)
                Stepper(
                    "Refresh every \(Int(model.settingsStore.settings.refreshIntervalSeconds))s",
                    value: $model.settingsStore.settings.refreshIntervalSeconds,
                    in: minRefreshIntervalSeconds...maxRefreshIntervalSeconds,
                    step: 1
                )
            }
            #if os(macOS)
            Section("Daemon") {
                HStack {
                    TextField("Daemon binary path", text: $daemonBinaryPath)
                    Button {
                        chooseDaemonBinary()
                    } label: {
                        Image(systemName: "folder")
                    }
                    .help("Choose daemon binary")
                }
                HStack {
                    TextField("Config path", text: $daemonConfigPath)
                    Button {
                        chooseConfigFile()
                    } label: {
                        Image(systemName: "doc")
                    }
                    .help("Choose config file")
                }
                Toggle("Launch daemon when app starts", isOn: $model.settingsStore.settings.launchDaemonOnStart)
                Toggle("Stop launched daemon when app quits", isOn: $model.settingsStore.settings.stopDaemonOnQuit)
            }
            #endif
            Section("History") {
                Stepper(
                    "Keep \(model.settingsStore.settings.logRetention) log lines",
                    value: $model.settingsStore.settings.logRetention,
                    in: minLogRetention...maxLogRetention,
                    step: 50
                )
            }
            #if os(iOS) || os(visionOS)
            SupportPurchasesSection()
            #endif
            Section {
                Button("Apply") {
                    apply()
                }
                .disabled(endpointValidationMessage(endpointText) != nil)
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
            daemonBinaryBookmark = model.settingsStore.settings.daemonBinaryBookmark
            daemonConfigBookmark = model.settingsStore.settings.daemonConfigBookmark
        }
    }

    private func apply() {
        guard endpointValidationMessage(endpointText) == nil,
              let endpoint = URL(string: endpointText.trimmingCharacters(in: .whitespacesAndNewlines)) else {
            return
        }
        model.settingsStore.settings.apiEndpoint = endpoint
        model.apiToken = tokenText
        model.settingsStore.settings.daemonBinaryPath = daemonBinaryPath
        model.settingsStore.settings.daemonConfigPath = daemonConfigPath
        model.settingsStore.settings.daemonBinaryBookmark = matchingBookmark(daemonBinaryBookmark, path: daemonBinaryPath)
        model.settingsStore.settings.daemonConfigBookmark = matchingBookmark(daemonConfigBookmark, path: daemonConfigPath)
        model.applySettings()
    }

    private func endpointValidationMessage(_ value: String) -> String? {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed), AppSettings.isSupportedAPIEndpoint(url) else {
            return "Use an http:// or https:// endpoint with a host."
        }
        return nil
    }

    #if os(macOS)
    private func chooseDaemonBinary() {
        chooseFile(title: "Choose clambhook daemon") { url in
            daemonBinaryPath = url.path
            daemonBinaryBookmark = securityBookmark(for: url)
        }
    }

    private func chooseConfigFile() {
        chooseFile(title: "Choose clambhook config") { url in
            daemonConfigPath = url.path
            daemonConfigBookmark = securityBookmark(for: url)
        }
    }

    private func chooseFile(title: String, completion: (URL) -> Void) {
        let panel = NSOpenPanel()
        panel.title = title
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = false
        panel.canChooseFiles = true
        if panel.runModal() == .OK, let url = panel.url {
            completion(url)
        }
    }

    private func securityBookmark(for url: URL) -> Data? {
        try? url.bookmarkData(options: [.withSecurityScope], includingResourceValuesForKeys: nil, relativeTo: nil)
    }

    private func matchingBookmark(_ data: Data?, path: String) -> Data? {
        guard let data else {
            return nil
        }
        var stale = false
        guard let url = try? URL(
            resolvingBookmarkData: data,
            options: [.withSecurityScope],
            relativeTo: nil,
            bookmarkDataIsStale: &stale
        ) else {
            return nil
        }
        return url.path == path.trimmingCharacters(in: .whitespacesAndNewlines) ? data : nil
    }
    #else
    private func matchingBookmark(_ data: Data?, path: String) -> Data? {
        data
    }
    #endif
}
