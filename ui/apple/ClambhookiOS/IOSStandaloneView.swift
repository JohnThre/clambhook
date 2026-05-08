import ClambhookShared
import NetworkExtension
import SwiftUI
import UniformTypeIdentifiers

struct IOSStandaloneView: View {
    @ObservedObject var model: AppleAppModel
    @StateObject private var tunnel = IOSTunnelController()
    @State private var toml = ""
    @State private var activeProfile = ""
    @State private var importingConfig = false

    var body: some View {
        List {
            Section {
                HStack {
                    Label(tunnelStatusText, systemImage: tunnel.isRunning ? "checkmark.circle.fill" : "network")
                        .foregroundStyle(tunnel.isRunning ? .green : .secondary)
                    Spacer()
                    if tunnel.isRunning {
                        Button {
                            tunnel.stop()
                        } label: {
                            Label("Stop", systemImage: "stop.fill")
                        }
                    } else {
                        Button {
                            Task { await startTunnel() }
                        } label: {
                            Label("Start", systemImage: "play.fill")
                        }
                    }
                }
                if !tunnel.message.isEmpty {
                    Text(tunnel.message)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }

            Section("Profile") {
                TextField("Active profile", text: $activeProfile)
                    .textInputAutocapitalization(.never)
                Button {
                    Task { await saveConfig() }
                } label: {
                    Label("Save VPN Configuration", systemImage: "square.and.arrow.down")
                }
                Button {
                    importingConfig = true
                } label: {
                    Label("Import TOML", systemImage: "doc.badge.plus")
                }
            }

            Section("Configuration") {
                TextEditor(text: $toml)
                    .font(.system(.body, design: .monospaced))
                    .frame(minHeight: 280)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
            }
        }
        .navigationTitle("clambhook")
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button {
                    Task { await saveConfig() }
                } label: {
                    Image(systemName: "square.and.arrow.down")
                }
                .accessibilityLabel("Save")
            }
        }
        .task {
            loadDocument()
            await tunnel.load()
        }
        .fileImporter(
            isPresented: $importingConfig,
            allowedContentTypes: [UTType(filenameExtension: "toml") ?? .plainText, .plainText],
            allowsMultipleSelection: false
        ) { result in
            importConfig(result)
        }
    }

    private var tunnelStatusText: String {
        switch tunnel.status {
        case .connected:
            return "VPN connected"
        case .connecting:
            return "VPN connecting"
        case .disconnecting:
            return "VPN disconnecting"
        case .reasserting:
            return "VPN reconnecting"
        case .disconnected:
            return tunnel.isConfigured ? "VPN stopped" : "VPN not configured"
        case .invalid:
            return "VPN not configured"
        @unknown default:
            return "VPN status unknown"
        }
    }

    private func loadDocument() {
        let document = model.standaloneConfigStore.document
        toml = document.toml
        activeProfile = document.activeProfile
    }

    private func currentDocument() -> StandaloneConfigDocument {
        StandaloneConfigDocument(toml: toml, activeProfile: activeProfile, updatedAt: Date())
    }

    private func saveConfig() async {
        do {
            let document = currentDocument()
            try model.standaloneConfigStore.save(document)
            await tunnel.saveConfiguration(document)
        } catch {
            tunnel.message = error.localizedDescription
        }
    }

    private func startTunnel() async {
        await saveConfig()
        await tunnel.start(document: currentDocument())
    }

    private func importConfig(_ result: Result<[URL], Error>) {
        do {
            guard let url = try result.get().first else {
                return
            }
            let didAccess = url.startAccessingSecurityScopedResource()
            defer {
                if didAccess {
                    url.stopAccessingSecurityScopedResource()
                }
            }
            toml = try String(contentsOf: url, encoding: .utf8)
        } catch {
            tunnel.message = error.localizedDescription
        }
    }
}
