import ClambhookShared
import SwiftUI

struct IOSProfilesView: View {
    @ObservedObject var model: AppleAppModel
    @State private var searchText = ""
    @State private var showingCreateProfile = false
    @State private var message = ""

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 12) {
                if !message.isEmpty {
                    IOSSurfaceSection("Status") {
                        Text(message)
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                }

                IOSSurfaceSection("Profiles", detail: "\(filteredProfiles.count)/\(model.dashboard.profiles.profiles.count)") {
                    if filteredProfiles.isEmpty {
                        ContentUnavailableView(
                            searchText.isEmpty ? "No profiles" : "No matching profiles",
                            systemImage: "person.crop.rectangle.stack",
                            description: Text("Create or import a profile to connect.")
                        )
                    } else {
                        VStack(spacing: 8) {
                            IOSConsoleMetricStrip(metrics: [
                                IOSConsoleMetric(title: "Active", value: emptyDash(model.dashboard.activeProfile), tint: .green),
                                IOSConsoleMetric(title: "Profiles", value: "\(model.dashboard.profiles.profiles.count)"),
                                IOSConsoleMetric(title: "Routes", value: "\(activeRouteCount(for: model.dashboard.activeProfile))"),
                                IOSConsoleMetric(title: "Tags", value: "\(model.profileMetadata.state.profiles.values.reduce(0) { $0 + $1.tags.count })"),
                            ])
                            ForEach(filteredProfiles, id: \.self) { profile in
                                NavigationLink {
                                    IOSProfileDetailView(model: model, profile: profile)
                                } label: {
                                    IOSProfileRow(
                                        profile: profile,
                                        isActive: profile == model.dashboard.activeProfile,
                                        routeCount: activeRouteCount(for: profile),
                                        tags: model.profileMetadata.tags(for: profile)
                                    ) {
                                        model.selectProfile(profile)
                                    }
                                }
                                .buttonStyle(.plain)
                            }
                        }
                    }
                }

                IOSSurfaceSection("Import", detail: "\(model.attention.state.inbox.count) staged") {
                    VStack(spacing: 8) {
                        NavigationLink {
                            IOSProfileImportsView(model: model)
                        } label: {
                            IOSConsoleNavRow(
                                title: "Profile Imports",
                                detail: "Review staged configs and QR scans",
                                systemImage: "tray.and.arrow.down"
                            )
                        }
                        .buttonStyle(.plain)

                        IOSConsoleKeyValueRow(
                            label: "Staged",
                            value: "\(model.attention.state.inbox.count) profile\(model.attention.state.inbox.count == 1 ? "" : "s")"
                        )
                    }
                }

                IOSSurfaceSection("Rules", detail: "\(model.dashboard.rules.rules.count) manual") {
                    VStack(spacing: 8) {
                        NavigationLink {
                            IOSRulesView(model: model)
                        } label: {
                            IOSConsoleNavRow(
                                title: "Edit Routing Rules",
                                detail: model.dashboard.activeProfile.isEmpty ? "Choose a profile first" : model.dashboard.activeProfile,
                                systemImage: "slider.horizontal.3"
                            )
                        }
                        .buttonStyle(.plain)
                        .disabled(model.dashboard.activeProfile.isEmpty)

                        if model.dashboard.rules.rules.isEmpty {
                            IOSInlineEmptyState(text: "No active-profile rules.", systemImage: "checklist")
                        } else {
                            ForEach(Array(model.dashboard.rules.rules.prefix(6).enumerated()), id: \.element.id) { index, rule in
                                IOSProfileRulePreviewRow(rule: rule, order: index + 1)
                            }
                        }
                    }
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
        }
        .background(Color(.systemGroupedBackground))
        .searchable(text: $searchText, prompt: "Search profiles")
        .refreshable {
            await model.refreshNow()
        }
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button {
                    showingCreateProfile = true
                } label: {
                    Image(systemName: "plus")
                }
                .accessibilityLabel("Create Profile")
            }
        }
        .sheet(isPresented: $showingCreateProfile) {
            IOSProfileCreateView(model: model) { message in
                self.message = message
                model.refresh()
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
}

private struct IOSConsoleNavRow: View {
    var title: String
    var detail: String
    var systemImage: String

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: systemImage)
                .foregroundStyle(.secondary)
                .frame(width: 22)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 8)
            Image(systemName: "chevron.right")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.tertiary)
        }
        .padding(.vertical, 4)
    }
}

private struct IOSProfileRulePreviewRow: View {
    var rule: RulePayload
    var order: Int

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Text("\(order)")
                .font(.caption.monospacedDigit().weight(.semibold))
                .foregroundStyle(.secondary)
                .frame(width: 22, alignment: .trailing)
            IOSActionChip(action: rule.action)
            VStack(alignment: .leading, spacing: 3) {
                Text(emptyDash(rule.name))
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                Text(iosRuleSummary(rule))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 0)
        }
        .padding(.vertical, 2)
    }
}

private struct IOSProfileUseButton: View {
    var isActive: Bool
    var action: () -> Void

    var body: some View {
        if isActive {
            Image(systemName: "checkmark.circle.fill")
                .foregroundStyle(.green)
                .frame(width: 30, height: 30)
                .accessibilityLabel("Active profile")
        } else {
            Button(action: action) {
                Image(systemName: "arrow.right.circle")
                    .frame(width: 30, height: 30)
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
            .accessibilityLabel("Use profile")
        }
    }
}

private func iosRuleSummary(_ rule: RulePayload) -> String {
    var parts: [String] = []
    if !rule.domains.isEmpty {
        parts.append(rule.domains.joined(separator: ", "))
    }
    if !rule.domainSuffixes.isEmpty {
        parts.append(rule.domainSuffixes.map { "*.\($0)" }.joined(separator: ", "))
    }
    if !rule.domainKeywords.isEmpty {
        parts.append(rule.domainKeywords.joined(separator: ", "))
    }
    if !rule.cidrs.isEmpty {
        parts.append(rule.cidrs.joined(separator: ", "))
    }
    if !rule.ports.isEmpty {
        parts.append(rule.ports.map(String.init).joined(separator: ", "))
    }
    if !rule.networks.isEmpty {
        parts.append(rule.networks.joined(separator: ", "))
    }
    return parts.isEmpty ? "All traffic" : parts.joined(separator: " / ")
}

private struct IOSProfileRow: View {
    var profile: String
    var isActive: Bool
    var routeCount: Int
    var tags: [String]
    var onUse: () -> Void

    var body: some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 3) {
                Text(emptyDash(profile))
                    .font(.body.weight(.medium))
                    .lineLimit(1)
                Text(subtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                if !tags.isEmpty {
                    Text(tags.joined(separator: ", "))
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            Spacer(minLength: 8)
            IOSProfileUseButton(isActive: isActive, action: onUse)
        }
        .padding(10)
        .background(Color(.tertiarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 7, style: .continuous))
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
                let tags = model.profileMetadata.tags(for: profile)
                if !tags.isEmpty {
                    LabeledContent("Tags", value: tags.joined(separator: ", "))
                }

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

struct IOSProfileCreateView: View {
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
