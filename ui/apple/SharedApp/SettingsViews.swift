import ClambhookShared
import SwiftUI

#if os(macOS)
import AppKit

struct AppSettingsView: View {
    @ObservedObject var model: AppleAppModel
    @State private var endpointText = ""
    @State private var tokenText = ""
    @State private var daemonBinaryPath = ""
    @State private var daemonConfigPath = ""
    @State private var daemonBinaryBookmark: Data?
    @State private var daemonConfigBookmark: Data?
    @State private var socks5Listen = ""
    @State private var socks5Chain = ""
    @State private var httpListen = ""
    @State private var httpChain = ""
    @State private var dnsEnabled = false
    @State private var dnsTimeout = "5s"
    @State private var dnsUpstreams: [EditableDNSUpstream] = []
    @State private var stableManifestURL = ""
    @State private var betaManifestURL = ""

    var body: some View {
        Form {
            apiSection
            daemonSection
            proxySection
            dnsSection
            certificateSection
            logsSection
            updatesSection
            #if os(macOS)
            MacLicenseSection(manager: model.licenseManager)
            #endif
            applySection
        }
        .formStyle(.grouped)
        .onAppear {
            loadSettings()
            model.refreshConfigSettings()
            model.refreshDeveloperCA()
        }
        .onChange(of: model.configSettings) { _, value in
            loadConfigSettings(value)
        }
    }

    private var apiSection: some View {
        Section("API") {
            TextField("Endpoint", text: $endpointText)
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
    }

    private var daemonSection: some View {
        Section("Daemon") {
            Picker("Routing mode", selection: $model.settingsStore.settings.routingMode) {
                ForEach(AppRoutingMode.allCases) { mode in
                    Text(mode.displayName).tag(mode)
                }
            }
            .pickerStyle(.segmented)
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
    }

    private var proxySection: some View {
        Section("Proxy") {
            TextField("SOCKS5 listen", text: $socks5Listen)
            TextField("SOCKS5 chain", text: $socks5Chain)
            TextField("HTTP listen", text: $httpListen)
            TextField("HTTP chain", text: $httpChain)
            HStack {
                Button {
                    saveProxyPorts()
                } label: {
                    Label("Save Ports", systemImage: "square.and.arrow.down")
                }
                Button {
                    model.refreshConfigSettings()
                } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                }
            }
            Toggle("Use as macOS system proxy", isOn: Binding(
                get: { model.settingsStore.settings.systemProxyEnabled },
                set: { enabled in
                    model.settingsStore.settings.systemProxyEnabled = enabled
                    model.systemProxyManager.apply(
                        enabled: enabled,
                        listen: ConfigListenSettingsPayload(
                            socks5: socks5Listen,
                            socks5Chain: socks5Chain,
                            http: httpListen,
                            httpChain: httpChain
                        )
                    )
                }
            ))
            .help(macOSProxyScopeDisclosure)
            if model.systemProxyManager.isApplying {
                ProgressView()
            }
            if !model.systemProxyManager.statusMessage.isEmpty {
                Text(model.systemProxyManager.statusMessage)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var dnsSection: some View {
        Section("DNS") {
            Toggle("Encrypted DNS", isOn: $dnsEnabled)
            TextField("Timeout", text: $dnsTimeout)
            ForEach($dnsUpstreams) { $upstream in
                VStack(alignment: .leading, spacing: 6) {
                    HStack {
                        TextField("Name", text: $upstream.name)
                        Picker("Protocol", selection: $upstream.protocolName) {
                            Text("DoH").tag("doh")
                            Text("DoT").tag("dot")
                            Text("DoQ").tag("doq")
                        }
                        .pickerStyle(.segmented)
                    }
                    TextField(upstream.protocolName == "doh" ? "URL" : "Address", text: $upstream.target)
                    TextField("Server name", text: $upstream.serverName)
                    TextField("Bootstrap IPs", text: $upstream.bootstrapIPs)
                }
            }
            HStack {
                Button {
                    dnsUpstreams.append(EditableDNSUpstream())
                } label: {
                    Label("Add Upstream", systemImage: "plus")
                }
                Button {
                    if !dnsUpstreams.isEmpty {
                        dnsUpstreams.removeLast()
                    }
                } label: {
                    Label("Remove Last", systemImage: "minus")
                }
                .disabled(dnsUpstreams.isEmpty)
                Spacer()
                Button {
                    saveDNS()
                } label: {
                    Label("Save DNS", systemImage: "square.and.arrow.down")
                }
            }
            if model.dashboard.dns.enabled {
                Text("Runtime strategy: \(model.dashboard.dns.strategy)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var certificateSection: some View {
        Section("CA Certificate") {
            Label(
                model.developerStatus.enabled ? "Developer capture configured" : "Developer capture disabled",
                systemImage: model.developerStatus.mitmEnabled ? "lock.open" : "lock"
            )
            if model.certificateManager.fingerprint.isEmpty {
                Text("No developer CA is available from the daemon.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                Text(model.certificateManager.fingerprint)
                    .font(.caption)
                    .textSelection(.enabled)
            }
            HStack {
                Button {
                    model.refreshDeveloperCA()
                } label: {
                    Label("Refresh CA", systemImage: "arrow.clockwise")
                }
                Button {
                    model.certificateManager.install(pem: model.developerCAPEMText)
                } label: {
                    Label("Trust CA", systemImage: "checkmark.shield")
                }
                .disabled(!canManageCA)
                Button(role: .destructive) {
                    model.certificateManager.remove(pem: model.developerCAPEMText)
                } label: {
                    Label("Remove Trust", systemImage: "xmark.shield")
                }
                .disabled(!canManageCA)
            }
            if model.certificateManager.isWorking {
                ProgressView()
            }
            if !model.certificateManager.statusMessage.isEmpty {
                Text(model.certificateManager.statusMessage)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var logsSection: some View {
        Section("Logs") {
            Stepper(
                "Keep \(model.settingsStore.settings.logRetention) log lines",
                value: $model.settingsStore.settings.logRetention,
                in: minLogRetention...maxLogRetention,
                step: 50
            )
            HStack {
                Button {
                    NSPasteboard.general.clearContents()
                    NSPasteboard.general.setString(model.dashboard.logs.joined(separator: "\n"), forType: .string)
                } label: {
                    Label("Copy Logs", systemImage: "doc.on.doc")
                }
                .disabled(model.dashboard.logs.isEmpty)
                Button(role: .destructive) {
                    model.dashboard.clearLogs()
                } label: {
                    Label("Clear Logs", systemImage: "trash")
                }
                .disabled(model.dashboard.logs.isEmpty)
            }
        }
    }

    private var updatesSection: some View {
        Section("Updates") {
            Picker("Channel", selection: $model.settingsStore.settings.updateChannel) {
                Text("Stable").tag("stable")
                Text("Beta").tag("beta")
            }
            .pickerStyle(.segmented)
            TextField("Stable manifest", text: $stableManifestURL)
            TextField("Beta manifest", text: $betaManifestURL)
            HStack {
                Button {
                    applyManifestURLs()
                    model.updateChecker.check(settings: model.settingsStore.settings)
                } label: {
                    Label("Check Now", systemImage: "arrow.down.circle")
                }
                if model.updateChecker.state == .checking {
                    ProgressView()
                }
            }
            Label(model.updateChecker.state.label, systemImage: updateStatusImage)
                .foregroundStyle(updateStatusColor)
            if let manifest = model.updateChecker.manifest {
                Text("\(manifest.version) (\(manifest.build)) · \(manifest.filename)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }
            if case .failed(let message) = model.updateChecker.state {
                Text(message)
                    .font(.caption)
                    .foregroundStyle(.red)
            }
        }
    }

    private var applySection: some View {
        Section {
            Button("Apply") {
                apply()
            }
            .disabled(applyDisabled)
            Button("Reset Endpoint") {
                endpointText = defaultAPIEndpoint.absoluteString
            }
        }
    }

    private var canManageCA: Bool {
        model.developerStatus.enabled && model.developerStatus.mitmEnabled && !model.developerCAPEMText.isEmpty
    }

    private var updateStatusImage: String {
        switch model.updateChecker.state {
        case .available:
            return "arrow.down.circle.fill"
        case .current:
            return "checkmark.circle.fill"
        case .failed:
            return "xmark.circle.fill"
        default:
            return "circle"
        }
    }

    private var updateStatusColor: Color {
        switch model.updateChecker.state {
        case .available:
            return .blue
        case .current:
            return .green
        case .failed:
            return .red
        default:
            return .secondary
        }
    }

    private func loadSettings() {
        let settings = model.settingsStore.settings.normalized()
        endpointText = settings.apiEndpoint.absoluteString
        tokenText = model.apiToken
        daemonBinaryPath = settings.daemonBinaryPath
        daemonConfigPath = settings.daemonConfigPath
        daemonBinaryBookmark = settings.daemonBinaryBookmark
        daemonConfigBookmark = settings.daemonConfigBookmark
        stableManifestURL = settings.stableUpdateManifestURL.absoluteString
        betaManifestURL = settings.betaUpdateManifestURL.absoluteString
        loadConfigSettings(model.configSettings)
    }

    private func loadConfigSettings(_ settings: ConfigSettingsPayload) {
        socks5Listen = settings.listen.socks5
        socks5Chain = settings.listen.socks5Chain
        httpListen = settings.listen.http
        httpChain = settings.listen.httpChain
        dnsEnabled = settings.dns.enabled
        dnsTimeout = settings.dns.timeout
        dnsUpstreams = settings.dns.upstreams.map(EditableDNSUpstream.init)
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
        applyManifestURLs()
        model.applySettings()
    }

    private func saveProxyPorts() {
        model.saveConfigSettings(listen: ConfigListenSettingsPayload(
            socks5: socks5Listen,
            socks5Chain: socks5Chain,
            http: httpListen,
            httpChain: httpChain
        ))
    }

    private func saveDNS() {
        model.saveConfigSettings(dns: ConfigDNSSettingsPayload(
            enabled: dnsEnabled,
            timeout: dnsTimeout.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "5s" : dnsTimeout,
            upstreams: dnsUpstreams.map(\.payload)
        ))
    }

    private func applyManifestURLs() {
        if let stable = URL(string: stableManifestURL.trimmingCharacters(in: .whitespacesAndNewlines)) {
            model.settingsStore.settings.stableUpdateManifestURL = stable
        }
        if let beta = URL(string: betaManifestURL.trimmingCharacters(in: .whitespacesAndNewlines)) {
            model.settingsStore.settings.betaUpdateManifestURL = beta
        }
        model.settingsStore.settings.updateChannel = AppSettings.normalizedUpdateChannel(model.settingsStore.settings.updateChannel)
    }

    private var applyDisabled: Bool {
        endpointValidationMessage(endpointText) != nil
    }

    private func endpointValidationMessage(_ value: String) -> String? {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed), AppSettings.isSupportedAPIEndpoint(url) else {
            return "Use an http:// or https:// endpoint with a host."
        }
        return nil
    }

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
}

private struct EditableDNSUpstream: Identifiable, Equatable {
    var id = UUID()
    var name = ""
    var protocolName = "doh"
    var target = ""
    var serverName = ""
    var bootstrapIPs = ""

    init() {}

    init(payload: DNSUpstreamPayload) {
        self.name = payload.name
        self.protocolName = payload.protocol.isEmpty ? "doh" : payload.protocol
        self.target = payload.url.isEmpty ? payload.address : payload.url
        self.serverName = payload.serverName
        self.bootstrapIPs = payload.bootstrapIPs.joined(separator: ", ")
    }

    var payload: DNSUpstreamPayload {
        DNSUpstreamPayload(
            name: name.trimmingCharacters(in: .whitespacesAndNewlines),
            protocol: protocolName,
            url: protocolName == "doh" ? target.trimmingCharacters(in: .whitespacesAndNewlines) : "",
            address: protocolName == "doh" ? "" : target.trimmingCharacters(in: .whitespacesAndNewlines),
            serverName: serverName.trimmingCharacters(in: .whitespacesAndNewlines),
            bootstrapIPs: bootstrapIPs
                .split(separator: ",")
                .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
                .filter { !$0.isEmpty }
        )
    }
}
#else
struct AppSettingsView: View {
    @ObservedObject var model: AppleAppModel
    @State private var endpointText = ""
    @State private var tokenText = ""

    var body: some View {
        Form {
            Section("API") {
                TextField("Endpoint", text: $endpointText)
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

            Section("Logs") {
                Stepper(
                    "Keep \(model.settingsStore.settings.logRetention) log lines",
                    value: $model.settingsStore.settings.logRetention,
                    in: minLogRetention...maxLogRetention,
                    step: 50
                )
            }

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
        .onAppear(perform: loadSettings)
    }

    private func loadSettings() {
        let settings = model.settingsStore.settings.normalized()
        endpointText = settings.apiEndpoint.absoluteString
        tokenText = model.apiToken
    }

    private func apply() {
        guard endpointValidationMessage(endpointText) == nil,
              let endpoint = URL(string: endpointText.trimmingCharacters(in: .whitespacesAndNewlines)) else {
            return
        }
        model.settingsStore.settings.apiEndpoint = endpoint
        model.apiToken = tokenText
        model.applySettings()
    }

    private func endpointValidationMessage(_ value: String) -> String? {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = URL(string: trimmed), AppSettings.isSupportedAPIEndpoint(url) else {
            return "Use an http:// or https:// endpoint with a host."
        }
        return nil
    }
}
#endif
