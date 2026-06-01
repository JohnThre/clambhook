import ClambhookShared
import SwiftUI
import UniformTypeIdentifiers
import UIKit

struct IOSProfilesView: View {
    @ObservedObject var model: AppleAppModel
    @State private var searchText = ""
    @State private var showingFileImporter = false
    @State private var activeSheet: IOSProfileCaptureSheet?
    @State private var message = ""

    var body: some View {
        List {
            if !message.isEmpty {
                Section {
                    Text(message)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }

            Section {
                if filteredProfiles.isEmpty {
                    ContentUnavailableView(
                        searchText.isEmpty ? "No profiles" : "No matching profiles",
                        systemImage: "person.crop.rectangle.stack",
                        description: Text("Import or create a profile to connect.")
                    )
                } else {
                    ForEach(filteredProfiles, id: \.self) { profile in
                        NavigationLink {
                            IOSProfileDetailView(model: model, profile: profile)
                        } label: {
                            IOSProfileRow(
                                profile: profile,
                                isActive: profile == model.dashboard.activeProfile,
                                routeCount: activeRouteCount(for: profile)
                            )
                        }
                        .swipeActions(edge: .trailing, allowsFullSwipe: true) {
                            if profile != model.dashboard.activeProfile {
                                Button("Use") {
                                    model.selectProfile(profile)
                                }
                                .tint(.blue)
                            }
                        }
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .searchable(text: $searchText, prompt: "Search profiles")
        .refreshable {
            await model.refreshNow()
        }
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    Button {
                        showingFileImporter = true
                    } label: {
                        Label("Import From Files", systemImage: "doc.badge.plus")
                    }

                    Button {
                        importFromClipboard()
                    } label: {
                        Label("Import From Clipboard", systemImage: "doc.on.clipboard")
                    }

                    Button {
                        message = ""
                        activeSheet = .scanQR
                    } label: {
                        Label("Scan QR", systemImage: "qrcode.viewfinder")
                    }

                    Button {
                        activeSheet = .createProfile
                    } label: {
                        Label("Create Manually", systemImage: "plus.circle")
                    }
                } label: {
                    Image(systemName: "plus")
                }
                .accessibilityLabel("Add Profile")
            }
        }
        .fileImporter(
            isPresented: $showingFileImporter,
            allowedContentTypes: [.text, .plainText, .data],
            allowsMultipleSelection: false
        ) { result in
            importFromFile(result)
        }
        .sheet(item: $activeSheet) { sheet in
            switch sheet {
            case .scanQR:
                IOSProfileQRCodeImportView(message: $message) { value in
                    importText(value, successMessage: "Imported QR code.")
                }
            case .createProfile:
                IOSProfileCreateView(model: model) { message in
                    self.message = message
                    model.refresh()
                }
            }
        }
    }

    private var filteredProfiles: [String] {
        let query = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !query.isEmpty else {
            return model.dashboard.profiles.profiles
        }
        return model.dashboard.profiles.profiles.filter { $0.lowercased().contains(query) }
    }

    private func activeRouteCount(for profile: String) -> Int {
        guard profile == model.dashboard.activeProfile else {
            return 0
        }
        return model.dashboard.servers.chains.reduce(0) { $0 + $1.servers.count }
    }

    private func importFromClipboard() {
        guard let text = UIPasteboard.general.string, !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            message = "Clipboard does not contain profile text."
            return
        }
        _ = importText(text, successMessage: "Imported clipboard profile.")
    }

    private func importFromFile(_ result: Result<[URL], Error>) {
        do {
            guard let url = try result.get().first else {
                return
            }
            let scoped = url.startAccessingSecurityScopedResource()
            defer {
                if scoped {
                    url.stopAccessingSecurityScopedResource()
                }
            }
            _ = importText(try String(contentsOf: url, encoding: .utf8), successMessage: "Imported file profile.")
        } catch {
            message = error.localizedDescription
        }
    }

    private func importText(_ raw: String, successMessage: String) -> Bool {
        do {
            try model.importTunnelConfigText(raw)
            message = successMessage
            model.refresh()
            return true
        } catch {
            message = error.localizedDescription
            return false
        }
    }
}

private enum IOSProfileCaptureSheet: String, Identifiable {
    case scanQR
    case createProfile

    var id: String { rawValue }
}

private struct IOSProfileRow: View {
    var profile: String
    var isActive: Bool
    var routeCount: Int

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: isActive ? "checkmark.circle.fill" : "circle")
                .foregroundStyle(isActive ? Color.green : Color.secondary)
                .frame(width: 24)

            VStack(alignment: .leading, spacing: 3) {
                Text(emptyDash(profile))
                    .font(.body.weight(.medium))
                    .lineLimit(1)
                Text(subtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, 2)
    }

    private var subtitle: String {
        if isActive {
            return routeCount == 1 ? "Active / 1 route" : "Active / \(routeCount) routes"
        }
        return "Inactive"
    }
}

private struct IOSProfileDetailView: View {
    @ObservedObject var model: AppleAppModel
    var profile: String

    var body: some View {
        List {
            Section {
                LabeledContent("State", value: isActive ? "Active" : "Inactive")

                if !isActive {
                    Button {
                        model.selectProfile(profile)
                    } label: {
                        Label("Use Profile", systemImage: "checkmark.circle")
                    }
                }
            }

            if isActive {
                if routeRows.isEmpty {
                    Section {
                        ContentUnavailableView(
                            "No routes",
                            systemImage: "point.3.connected.trianglepath.dotted",
                            description: Text("Routes from this profile appear here.")
                        )
                    }
                } else {
                    Section("Routes") {
                        ForEach(routeRows) { row in
                            IOSServerHealthRow(row: row)
                        }
                    }
                }

                Section("Rules") {
                    LabeledContent("Active rules", value: "\(model.dashboard.rules.rules.count)")
                }
            } else {
                Section {
                    IOSInlineEmptyState(text: "Make active to inspect routes.", systemImage: "checkmark.circle")
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle(emptyDash(profile))
        .navigationBarTitleDisplayMode(.inline)
        .refreshable {
            await model.refreshNow()
        }
    }

    private var isActive: Bool {
        profile == model.dashboard.activeProfile
    }

    private var routeRows: [IOSServerHealthRowData] {
        let health = model.dashboard.passiveServerHealth
        return model.dashboard.servers.chains.flatMap { chain in
            chain.servers.map { server in
                IOSServerHealthRowData(chainName: chain.name, server: server, health: health[server.id])
            }
        }
    }
}

private struct IOSProfileQRCodeImportView: View {
    @Binding var message: String
    var onImport: (String) -> Bool
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                IOSQRCodeScannerView { value in
                    if onImport(value) {
                        dismiss()
                        return true
                    }
                    return false
                }
                .frame(maxWidth: .infinity)
                .frame(height: 360)
                .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
                .padding(20)

                if !message.isEmpty {
                    Text(message)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 20)
                }

                Spacer(minLength: 0)
            }
            .background(Color(.systemGroupedBackground))
            .navigationTitle("Scan QR")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
            }
        }
    }
}

private struct IOSProfileCreateView: View {
    @ObservedObject var model: AppleAppModel
    var onCreated: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var draft = TunnelProfileCreateDraft(replace: false)
    @State private var message = ""

    var body: some View {
        NavigationStack {
            Form {
                IOSTunnelProfileTemplateForm(draft: $draft)

                if !message.isEmpty {
                    Section {
                        Text(message)
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            .navigationTitle("Create Profile")
            .navigationBarTitleDisplayMode(.inline)
            .onAppear {
                seedAdvancedTOMLIfNeeded()
            }
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button(createButtonTitle) {
                        createProfile()
                    }
                    .fontWeight(.semibold)
                    .disabled(createDisabled)
                }
            }
        }
    }

    private var createDisabled: Bool {
        !draft.isInputComplete
    }

    private var createButtonTitle: String {
        draft.template == .advanced ? "Save" : "Create"
    }

    private func seedAdvancedTOMLIfNeeded() {
        guard draft.advancedTOML.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            return
        }
        draft.advancedTOML = (try? TunnelConfigStore.loadOrCreateConfig(groupIdentifier: model.settingsStore.settings.appGroupIdentifier)) ?? defaultIOSTunnelConfig
    }

    private func createProfile() {
        do {
            if draft.template == .advanced {
                try model.importTunnelConfigText(draft.advancedTOML)
                onCreated("Saved advanced config.")
            } else if let request = draft.makeCreateRequest() {
                try model.createTunnelProfile(request)
                onCreated("Created profile.")
            }
            dismiss()
        } catch {
            message = error.localizedDescription
        }
    }
}

struct IOSTunnelProfileTemplateForm: View {
    @Binding var draft: TunnelProfileCreateDraft

    var body: some View {
        Group {
            Section("Template") {
                Picker("Type", selection: $draft.template) {
                    ForEach(TunnelProfileTemplate.allCases) { template in
                        Text(template.displayName).tag(template)
                    }
                }
            }

            if draft.template == .advanced {
                advancedSection
            } else {
                commonProfileSections
                settingsSection
            }
        }
        .onChange(of: draft.template) { oldTemplate, _ in
            draft.applyTemplateDefaults(previousTemplate: oldTemplate)
        }
    }

    @ViewBuilder
    private var commonProfileSections: some View {
        Section("Profile") {
            TextField("Profile name", text: $draft.profileName)
                .profileTemplateInput()
            TextField("Route", text: $draft.chainName)
                .profileTemplateInput()
        }

        Section("Server") {
            TextField("Display name", text: $draft.serverName)
                .profileTemplateInput()
            TextField(serverAddressLabel, text: $draft.serverAddress)
                .profileTemplateInput()
        }
    }

    @ViewBuilder
    private var settingsSection: some View {
        switch draft.template {
        case .shadowsocks:
            Section("Shadowsocks") {
                Picker("Method", selection: $draft.shadowsocks.method) {
                    Text("chacha20-ietf-poly1305").tag("chacha20-ietf-poly1305")
                    Text("aes-128-gcm").tag("aes-128-gcm")
                    Text("aes-256-gcm").tag("aes-256-gcm")
                }
                SecureField("Password", text: $draft.shadowsocks.password)
                    .profileTemplateInput()
            }
        case .wireguard:
            Section("WireGuard") {
                SecureField("Private key", text: $draft.wireguard.privateKey)
                    .profileTemplateInput()
                TextField("Interface addresses", text: $draft.wireguard.interfaceAddresses)
                    .profileTemplateInput()
                TextField("DNS servers", text: $draft.wireguard.dnsServers)
                    .profileTemplateInput()
                TextField("Peer public key", text: $draft.wireguard.peerPublicKey)
                    .profileTemplateInput()
                SecureField("Preshared key", text: $draft.wireguard.presharedKey)
                    .profileTemplateInput()
                TextField("Allowed IPs", text: $draft.wireguard.allowedIPs)
                    .profileTemplateInput()
                Stepper("Keepalive \(draft.wireguard.persistentKeepalive)s", value: $draft.wireguard.persistentKeepalive, in: 0...65535)
                TextField("MTU", value: $draft.wireguard.mtu, format: .number)
                    .keyboardType(.numberPad)
                Picker("Log level", selection: $draft.wireguard.logLevel) {
                    Text("error").tag("error")
                    Text("silent").tag("silent")
                    Text("verbose").tag("verbose")
                }
            }
        case .openvpn:
            Section("OpenVPN") {
                pemEditor("CA certificate", text: $draft.openvpn.caCert)
                pemEditor("Client certificate", text: $draft.openvpn.clientCert)
                pemEditor("Client key", text: $draft.openvpn.clientKey)
                TextField("Server CN", text: $draft.openvpn.serverCN)
                    .profileTemplateInput()
                TextField("Username", text: $draft.openvpn.username)
                    .profileTemplateInput()
                SecureField("Password", text: $draft.openvpn.password)
                    .profileTemplateInput()
                Picker("Cipher", selection: $draft.openvpn.cipher) {
                    Text("Negotiated").tag("")
                    Text("AES-256-GCM").tag("AES-256-GCM")
                    Text("CHACHA20-POLY1305").tag("CHACHA20-POLY1305")
                }
                TextField("TUN MTU", value: $draft.openvpn.tunMTU, format: .number)
                    .keyboardType(.numberPad)
                Toggle("Skip certificate verification", isOn: $draft.openvpn.skipCertVerify)
            }
        case .trojan:
            trojanSection(title: "Trojan", settings: $draft.trojan)
        case .tor:
            Section("Tor") {
                TextField("Isolation user", text: $draft.tor.isolationUser)
                    .profileTemplateInput()
                SecureField("Isolation password", text: $draft.tor.isolationPass)
                    .profileTemplateInput()
            }
        case .clambback:
            trojanSection(title: "Clambback", settings: $draft.clambback)
        case .advanced:
            EmptyView()
        }
    }

    private var advancedSection: some View {
        Section("Advanced") {
            TextEditor(text: $draft.advancedTOML)
                .font(.system(.footnote, design: .monospaced))
                .frame(minHeight: 320)
                .profileTemplateInput()
        }
    }

    private var serverAddressLabel: String {
        switch draft.template {
        case .tor:
            return "SOCKS address"
        case .wireguard:
            return "Peer endpoint"
        case .openvpn:
            return "VPN address"
        default:
            return "Address"
        }
    }

    private func pemEditor(_ title: String, text: Binding<String>) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title)
                .font(.footnote)
                .foregroundStyle(.secondary)
            TextEditor(text: text)
                .font(.system(.footnote, design: .monospaced))
                .frame(minHeight: 92)
                .profileTemplateInput()
        }
    }

    private func trojanSection(title: String, settings: Binding<TunnelTrojanTemplateSettings>) -> some View {
        Section(title) {
            SecureField("Password", text: settings.password)
                .profileTemplateInput()
            TextField("SNI", text: settings.sni)
                .profileTemplateInput()
            TextField("ALPN", text: settings.alpn)
                .profileTemplateInput()
            Toggle("Skip certificate verification", isOn: settings.skipCertVerify)
        }
    }
}

private extension View {
    func profileTemplateInput() -> some View {
        textInputAutocapitalization(.never)
            .autocorrectionDisabled()
    }
}
