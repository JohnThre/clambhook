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
    @State private var tunEnabled = false
    @State private var tunName = ""
    @State private var tunChain = ""
    @State private var tunMTU = 1500
    @State private var tunAddressesText = ""
    @State private var tunRoutesText = ""
    @State private var tunExcludeCIDRsText = ""
    @State private var dnsEnabled = false
    @State private var dnsTimeout = "5s"
    @State private var dnsUpstreams: [EditableDNSUpstream] = []
    @State private var developerCaptureEnabled = false
    @State private var httpsCaptureEnabled = false
    @State private var noCacheEnabled = false
    @State private var captureLimit = 200
    @State private var bodyLimitBytes = 65_536
    @State private var headerValueLimitBytes = 8_192
    @State private var redactHeadersText = developerDefaultRedactHeaders.joined(separator: ", ")
    @State private var redactQueryParamsText = developerDefaultRedactQueryParams.joined(separator: ", ")
    @State private var showingHTTPSCaptureConfirmation = false
    @State private var showingCARegenerationConfirmation = false
    @State private var stableManifestURL = ""
    @State private var betaManifestURL = ""

    var body: some View {
        Form {
            apiSection
            daemonSection
            enhancedModeSection
            proxySection
            dnsSection
            developerCaptureSection
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
            model.privilegedHelperManager.refreshStatus()
            model.refreshConfigSettings()
            model.refreshDeveloperCapture()
        }
        .onChange(of: model.configSettings) { _, value in
            loadConfigSettings(value)
        }
        .onChange(of: model.developerSettings) { _, value in
            loadDeveloperSettings(value)
        }
        .confirmationDialog(
            "Enable HTTPS Capture?",
            isPresented: $showingHTTPSCaptureConfirmation,
            titleVisibility: .visible
        ) {
            Button("Enable HTTPS Capture", role: .destructive) {
                developerCaptureEnabled = true
                httpsCaptureEnabled = true
                saveDeveloperSettings(enabled: true, mitmEnabled: true, httpsCaptureAck: true)
            }
            Button("Cancel", role: .cancel) {
                httpsCaptureEnabled = model.developerSettings.mitmEnabled
            }
        } message: {
            Text(developerHTTPSCaptureDisclosure)
        }
        .confirmationDialog(
            "Regenerate Developer CA?",
            isPresented: $showingCARegenerationConfirmation,
            titleVisibility: .visible
        ) {
            Button("Regenerate CA", role: .destructive) {
                model.regenerateDeveloperCA()
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Regenerating the developer CA replaces the certificate used for HTTPS capture. You will need to trust the new certificate before HTTPS clients accept intercepted traffic.")
        }
    }

    private var privilegedHelperStatusImage: String {
        switch model.privilegedHelperManager.serviceStatus {
        case .enabled:
            return "checkmark.circle.fill"
        case .requiresApproval:
            return "exclamationmark.triangle.fill"
        case .notFound:
            return "questionmark.circle"
        case .notRegistered:
            return "lock.shield"
        case .unknown:
            return "questionmark.circle"
        }
    }

    private var privilegedHelperStatusColor: Color {
        switch model.privilegedHelperManager.serviceStatus {
        case .enabled:
            return .green
        case .requiresApproval:
            return .orange
        case .notFound, .unknown:
            return .red
        case .notRegistered:
            return .secondary
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
            Toggle("Use privileged helper", isOn: Binding(
                get: {
                    model.settingsStore.settings.routingMode.requiresPrivilegedHelper ||
                    model.settingsStore.settings.usePrivilegedHelper
                },
                set: { enabled in
                    model.settingsStore.settings.usePrivilegedHelper =
                        model.settingsStore.settings.routingMode.requiresPrivilegedHelper ? true : enabled
                }
            ))
            .disabled(model.settingsStore.settings.routingMode.requiresPrivilegedHelper)
            HStack {
                Label(
                    model.privilegedHelperManager.serviceStatus.label,
                    systemImage: privilegedHelperStatusImage
                )
                .foregroundStyle(privilegedHelperStatusColor)
                if model.privilegedHelperManager.isWorking {
                    ProgressView()
                        .controlSize(.small)
                }
            }
            if model.privilegedHelperManager.daemonRunning {
                Text("Helper daemon PID \(model.privilegedHelperManager.daemonPID.map(String.init) ?? "-")")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if let state = model.daemonFallbackUnavailableState {
                AppRecoveryStatePanel(state: state) { action in
                    model.performAppRecoveryAction(action)
                }
            }
            HStack {
                Button {
                    Task { await model.privilegedHelperManager.registerHelper() }
                } label: {
                    Label("Install Helper", systemImage: "lock.shield")
                }
                Button {
                    model.privilegedHelperManager.openSystemSettings()
                } label: {
                    Label("Open System Settings", systemImage: "gear")
                }
                Button(role: .destructive) {
                    Task { await model.privilegedHelperManager.unregisterHelper() }
                } label: {
                    Label("Remove Helper", systemImage: "trash")
                }
            }
            if !model.privilegedHelperManager.statusMessage.isEmpty {
                Text(model.privilegedHelperManager.statusMessage)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var enhancedModeSection: some View {
        Section("Enhanced Mode") {
            Toggle("Enable TUN listener in active profile", isOn: $tunEnabled)
            TextField("Interface name", text: $tunName)
                .help("Leave empty to use the platform default. macOS uses utun.")
            TextField("TUN chain", text: $tunChain)
                .help("Leave empty to use the profile's first chain.")
            Stepper("MTU \(tunMTU)", value: $tunMTU, in: 576...9000, step: 10)
            TextField("Tunnel addresses", text: $tunAddressesText, axis: .vertical)
                .lineLimit(2...4)
            TextField("Routes", text: $tunRoutesText, axis: .vertical)
                .lineLimit(2...4)
            TextField("Excluded CIDRs", text: $tunExcludeCIDRsText, axis: .vertical)
                .lineLimit(2...4)
            HStack {
                Button {
                    saveTUN()
                } label: {
                    Label("Save Enhanced Mode", systemImage: "square.and.arrow.down")
                }
                Button {
                    tunEnabled = true
                    tunName = ""
                    tunMTU = 1500
                    tunAddressesText = "198.18.0.1/30, fd7a:636c:616d::1/64"
                    tunRoutesText = "0.0.0.0/0, ::/0"
                    tunExcludeCIDRsText = "127.0.0.0/8, ::1/128"
                } label: {
                    Label("Use Defaults", systemImage: "wand.and.stars")
                }
            }
            Text("Enhanced Mode requires the privileged helper. When encrypted DNS is enabled, ClambHook temporarily rewrites macOS DNS servers and restores them when the daemon stops.")
                .font(.caption)
                .foregroundStyle(.secondary)
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

    private var developerCaptureSection: some View {
        Section("HTTP Capture") {
            Toggle("HTTP capture", isOn: Binding(
                get: { developerCaptureEnabled },
                set: { enabled in
                    developerCaptureEnabled = enabled
                    if !enabled {
                        httpsCaptureEnabled = false
                        noCacheEnabled = false
                    }
                    saveDeveloperSettings(enabled: enabled, mitmEnabled: false, noCacheEnabled: enabled ? nil : false)
                }
            ))
            Text(developerCaptureDisclosure)
                .font(.caption)
                .foregroundStyle(.secondary)

            Stepper("Keep \(captureLimit) captures", value: $captureLimit, in: 0...5_000, step: 50)
            Stepper("Body preview \(bodyLimitBytes) bytes", value: $bodyLimitBytes, in: 0...1_048_576, step: 4_096)
            Stepper("Header value limit \(headerValueLimitBytes) bytes", value: $headerValueLimitBytes, in: 0...65_536, step: 512)
            Toggle("No-cache inspected traffic", isOn: Binding(
                get: { noCacheEnabled },
                set: { enabled in
                    noCacheEnabled = enabled
                    saveDeveloperSettings(noCacheEnabled: enabled)
                }
            ))
            .disabled(!developerCaptureEnabled)
            TextField("Redacted headers", text: $redactHeadersText)
            TextField("Redacted query parameters", text: $redactQueryParamsText)
            Button {
                saveDeveloperSettings()
            } label: {
                Label("Save Capture Defaults", systemImage: "square.and.arrow.down")
            }

            Divider()

            Toggle("HTTPS capture", isOn: Binding(
                get: { httpsCaptureEnabled },
                set: { enabled in
                    if enabled {
                        showingHTTPSCaptureConfirmation = true
                    } else {
                        httpsCaptureEnabled = false
                        saveDeveloperSettings(mitmEnabled: false)
                    }
                }
            ))
            .disabled(!developerCaptureEnabled)
            Text(developerHTTPSCaptureDisclosure)
                .font(.caption)
                .foregroundStyle(.secondary)

            Label(
                httpsCaptureEnabled ? "HTTPS capture enabled" : "HTTPS capture disabled",
                systemImage: httpsCaptureEnabled ? "lock.open" : "lock"
            )
            .foregroundStyle(httpsCaptureEnabled ? .orange : .secondary)
            if developerCaptureEnabled, httpsCaptureEnabled {
                Label(
                    model.certificateManager.trustStatus.label,
                    systemImage: certificateTrustStatusImage
                )
                .font(.caption)
                .foregroundStyle(certificateTrustStatusColor)
            }
            if let state = model.certificateNotTrustedState {
                AppRecoveryStatePanel(state: state) { action in
                    model.performAppRecoveryAction(action)
                }
            }
            if model.certificateManager.fingerprint.isEmpty {
                Text("No developer CA is available from the daemon.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                Text(model.certificateManager.fingerprint)
                    .font(.caption)
                    .textSelection(.enabled)
            }
            if !model.developerStatus.caNotBefore.isEmpty || !model.developerStatus.caNotAfter.isEmpty {
                Text("Valid \(emptyDash(model.developerStatus.caNotBefore)) – \(emptyDash(model.developerStatus.caNotAfter))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }
            HStack {
                Button {
                    model.refreshDeveloperCapture()
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
                Button(role: .destructive) {
                    showingCARegenerationConfirmation = true
                } label: {
                    Label("Regenerate CA", systemImage: "arrow.triangle.2.circlepath")
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
            Toggle("Automatically check for updates", isOn: Binding(
                get: { model.sparkleUpdater.automaticallyChecksForUpdates },
                set: { model.sparkleUpdater.automaticallyChecksForUpdates = $0 }
            ))
            Button {
                model.checkForUpdatesWithSparkle()
            } label: {
                Label("Check for Updates and Install…", systemImage: "square.and.arrow.down.on.square")
            }
            .disabled(!model.sparkleUpdater.canCheckForUpdates)
            Label(model.updateChecker.state.label, systemImage: updateStatusImage)
                .foregroundStyle(updateStatusColor)
            if let manifest = model.updateChecker.manifest {
                Text("\(manifest.version) (\(manifest.build)) · \(manifest.filename)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }
            if let state = model.licenseExpiredForUpdatesState {
                AppRecoveryStatePanel(state: state) { action in
                    model.performAppRecoveryAction(action)
                }
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
        model.developerSettings.enabled && model.developerSettings.mitmEnabled && !model.developerCAPEMText.isEmpty
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

    private var certificateTrustStatusImage: String {
        switch model.certificateManager.trustStatus {
        case .trusted:
            return "checkmark.shield.fill"
        case .checking:
            return "hourglass"
        case .notTrusted:
            return "xmark.shield.fill"
        case .failed:
            return "exclamationmark.triangle.fill"
        case .unavailable:
            return "shield.slash"
        }
    }

    private var certificateTrustStatusColor: Color {
        switch model.certificateManager.trustStatus {
        case .trusted:
            return .green
        case .checking:
            return .orange
        case .notTrusted, .failed:
            return .red
        case .unavailable:
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
        loadDeveloperSettings(model.developerSettings)
    }

    private func loadConfigSettings(_ settings: ConfigSettingsPayload) {
        socks5Listen = settings.listen.socks5
        socks5Chain = settings.listen.socks5Chain
        httpListen = settings.listen.http
        httpChain = settings.listen.httpChain
        tunEnabled = settings.listen.tun.enabled
        tunName = settings.listen.tun.name
        tunChain = settings.listen.tun.chain
        tunMTU = settings.listen.tun.mtu == 0 ? 1500 : settings.listen.tun.mtu
        tunAddressesText = settings.listen.tun.addresses.joined(separator: ", ")
        tunRoutesText = settings.listen.tun.routes.joined(separator: ", ")
        tunExcludeCIDRsText = settings.listen.tun.excludeCIDRs.joined(separator: ", ")
        dnsEnabled = settings.dns.enabled
        dnsTimeout = settings.dns.timeout
        dnsUpstreams = settings.dns.upstreams.map(EditableDNSUpstream.init)
    }

    private func loadDeveloperSettings(_ settings: DeveloperSettingsPayload) {
        developerCaptureEnabled = settings.enabled
        httpsCaptureEnabled = settings.mitmEnabled
        noCacheEnabled = settings.noCacheEnabled
        captureLimit = settings.captureLimit
        bodyLimitBytes = Int(min(settings.bodyLimitBytes, UInt64(Int.max)))
        headerValueLimitBytes = settings.headerValueLimitBytes
        redactHeadersText = (settings.redactHeaders.isEmpty ? developerDefaultRedactHeaders : settings.redactHeaders).joined(separator: ", ")
        redactQueryParamsText = (settings.redactQueryParams.isEmpty ? developerDefaultRedactQueryParams : settings.redactQueryParams).joined(separator: ", ")
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
        if model.settingsStore.settings.routingMode.requiresPrivilegedHelper {
            model.settingsStore.settings.usePrivilegedHelper = true
        }
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

    private func saveTUN() {
        model.saveConfigSettings(listen: ConfigListenSettingsPayload(
            socks5: socks5Listen,
            socks5Chain: socks5Chain,
            http: httpListen,
            httpChain: httpChain,
            tun: ConfigTUNSettingsPayload(
                enabled: tunEnabled,
                name: tunName,
                chain: tunChain,
                mtu: tunMTU,
                addresses: splitList(tunAddressesText),
                routes: splitList(tunRoutesText),
                excludeCIDRs: splitList(tunExcludeCIDRsText)
            )
        ))
    }

    private func saveDeveloperSettings(enabled: Bool? = nil, mitmEnabled: Bool? = nil, noCacheEnabled: Bool? = nil, httpsCaptureAck: Bool = false) {
        model.saveDeveloperSettings(DeveloperSettingsUpdateRequest(
            enabled: enabled,
            mitmEnabled: mitmEnabled,
            noCacheEnabled: noCacheEnabled,
            captureLimit: captureLimit,
            bodyLimitBytes: UInt64(max(0, bodyLimitBytes)),
            headerValueLimitBytes: headerValueLimitBytes,
            redactHeaders: redactionList(redactHeadersText),
            redactQueryParams: redactionList(redactQueryParamsText),
            httpsCaptureAck: httpsCaptureAck
        ))
    }

    private func redactionList(_ value: String) -> [String] {
        value
            .split { character in character == "," || character == "\n" }
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() }
            .filter { !$0.isEmpty }
    }

    private func splitList(_ value: String) -> [String] {
        value
            .split { character in character == "," || character == "\n" || character == " " || character == "\t" }
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
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
