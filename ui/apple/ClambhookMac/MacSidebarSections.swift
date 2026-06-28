import AppKit
import ClambhookShared
import SwiftUI

// MARK: - Dashboard

struct MacDashboardSection: View {
    @ObservedObject var model: AppleAppModel
    @ObservedObject private var daemon: DaemonSupervisor
    var onNavigate: ((SidebarItem) -> Void)?

    init(model: AppleAppModel, onNavigate: ((SidebarItem) -> Void)? = nil) {
        self.model = model
        self.onNavigate = onNavigate
        self._daemon = ObservedObject(wrappedValue: model.daemonSupervisor)
    }

    var body: some View {
        VStack(spacing: 0) {
            statusStrip
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    if !model.appRecoveryStates.isEmpty {
                        recoveryStates
                        Divider()
                    }
                    if !model.dashboard.policyGroups.groups.isEmpty {
                        policyGroupHealth
                        Divider()
                    }
                    miniActivityFeed
                }
                .padding(20)
            }
        }
    }

    // MARK: Status strip

    private var statusStrip: some View {
        HStack(spacing: 10) {
            Circle()
                .fill(statusDotColor)
                .frame(width: 8, height: 8)
            Text(statusText)
                .font(.subheadline.weight(.medium))
                .lineLimit(1)
            profilePicker
            if daemon.state.isBusy {
                ProgressView()
                    .controlSize(.small)
                    .scaleEffect(0.75)
            }
            Spacer(minLength: 8)
            if model.dashboard.status.running {
                let bw = model.dashboard.currentBandwidth
                Text("↓ \(formatRate(bw.rxBps))")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                Text("↑ \(formatRate(bw.txBps))")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
                let activeConns = model.dashboard.status.listeners.reduce(0) { $0 + $1.activeConns }
                if activeConns > 0 {
                    Text("\(activeConns) active")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                tunnelModeBadge
                if bestLatency != "--" {
                    Text(bestLatency)
                        .font(.caption.weight(.semibold))
                        .monospacedDigit()
                        .foregroundStyle(.orange)
                }
            }
            apiPill
            Button {
                model.connectOrDisconnect()
            } label: {
                Label(
                    model.dashboard.status.running ? "Disconnect" : "Connect",
                    systemImage: model.dashboard.status.running ? "stop.fill" : "play.fill"
                )
            }
            .buttonStyle(.borderedProminent)
            .tint(model.dashboard.status.running ? .red : .green)
            .controlSize(.small)
            .disabled(!model.dashboard.apiOnline && !model.dashboard.status.running)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
    }

    private var profilePicker: some View {
        HStack(spacing: 6) {
            Image(systemName: "person.crop.circle")
                .foregroundStyle(.secondary)
            if model.dashboard.profiles.profiles.isEmpty {
                Text(model.dashboard.activeProfile.isEmpty ? "No profile" : model.dashboard.activeProfile)
                    .foregroundStyle(.secondary)
            } else {
                Picker("Profile", selection: Binding(
                    get: { model.dashboard.activeProfile },
                    set: { model.selectProfile($0) }
                )) {
                    ForEach(model.dashboard.profiles.profiles, id: \.self) { profile in
                        Text(profile).tag(profile)
                    }
                }
                .labelsHidden()
                .pickerStyle(.menu)
            }
        }
    }

    private var apiPill: some View {
        Label(
            model.dashboard.apiOnline ? "API online" : "API offline",
            systemImage: model.dashboard.apiOnline ? "checkmark.circle.fill" : "xmark.circle.fill"
        )
        .font(.caption.weight(.medium))
        .foregroundStyle(model.dashboard.apiOnline ? Color.green : Color.red)
        .padding(.horizontal, 8)
        .padding(.vertical, 4)
        .background(
            (model.dashboard.apiOnline ? Color.green : Color.red).opacity(0.12),
            in: Capsule()
        )
    }

    @ViewBuilder
    private var tunnelModeBadge: some View {
        let mode = model.dashboard.status.tunnelMode
        if mode == "tun" {
            Label("Full Tunnel", systemImage: "network.badge.shield.half.filled")
                .font(.caption.weight(.medium))
                .foregroundStyle(Color.green)
                .padding(.horizontal, 6)
                .padding(.vertical, 3)
                .background(Color.green.opacity(0.12), in: Capsule())
        } else if mode == "proxy" {
            Label("Proxy", systemImage: "globe")
                .font(.caption.weight(.medium))
                .foregroundStyle(Color.secondary)
        }
    }

    private var statusDotColor: Color {
        if model.dashboard.status.running { return .green }
        switch daemon.state {
        case .starting, .stopping: return .orange
        case .failed: return .red
        default: return .secondary
        }
    }

    private var statusText: String {
        if model.dashboard.status.running { return "Connected" }
        switch daemon.state {
        case .running: return "Daemon running"
        case .starting: return "Starting…"
        case .stopping: return "Stopping…"
        case .failed: return "Daemon failed"
        case .stopped: return "Disconnected"
        }
    }

    private var bestLatency: String {
        for group in model.dashboard.policyGroups.groups {
            let selected = group.selectedChain.isEmpty ? group.selected : group.selectedChain
            if let result = group.results.first(where: { $0.chainName == selected }), result.latencyNs > 0 {
                return formatDurationNs(result.latencyNs)
            }
        }
        return "--"
    }

    private var recoveryStates: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Attention")
                .font(.headline)
            ForEach(model.appRecoveryStates) { state in
                AppRecoveryStatePanel(state: state) { action in
                    handleRecoveryAction(action)
                }
            }
        }
    }

    private func handleRecoveryAction(_ action: AppRecoveryStateAction) {
        switch action {
        case .createProfile, .importProfile, .openProfiles:
            onNavigate?(.profile(model.dashboard.activeProfile))
        case .openAppSettings, .openSettings, .openSystemSettings:
            onNavigate?(.settings)
        case .buyLicense, .activateLicense, .openLicensePortal, .renewUpdates:
            onNavigate?(.license)
        default:
            break
        }
        model.performAppRecoveryAction(action)
    }

    // MARK: Policy group health

    private var policyGroupHealth: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Policy Groups")
                .font(.headline)
            ForEach(model.dashboard.policyGroups.groups) { group in
                MacPolicyGroupHealthRow(group: group, onSelect: { chain in
                    model.selectPolicyGroup(group: group.name, chain: chain)
                })
            }
        }
    }

    // MARK: Mini activity feed

    private var miniActivityFeed: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Activity")
                    .font(.headline)
                Spacer()
                let counts = model.dashboard.monitorActionCounts
                HStack(spacing: 8) {
                    MacActionBadge(label: "P \(counts["proxy", default: 0])", color: .green)
                    MacActionBadge(label: "D \(counts["direct", default: 0])", color: .blue)
                    MacActionBadge(label: "B \(counts["block", default: 0])", color: .red)
                }
                if onNavigate != nil {
                    Button("View All") { onNavigate?(.activity) }
                        .buttonStyle(.borderless)
                        .font(.caption)
                        .foregroundStyle(Color.accentColor)
                }
            }
            let connections = Array(model.dashboard.traffic.connections.prefix(20))
            if connections.isEmpty && model.dashboard.recentDecisions.isEmpty {
                Text("No recent traffic")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else if !connections.isEmpty {
                ForEach(connections.prefix(15)) { conn in
                    MiniActivityRow(connection: conn)
                }
            } else {
                ForEach(model.dashboard.recentDecisions.prefix(15)) { decision in
                    HStack(spacing: 8) {
                        Circle()
                            .fill(decisionColor(decision.action))
                            .frame(width: 8, height: 8)
                        Text(emptyDash(decision.target))
                            .font(.caption)
                            .lineLimit(1)
                            .truncationMode(.middle)
                        Spacer(minLength: 8)
                        Text([decision.ruleName, decision.action].filter { !$0.isEmpty }.joined(separator: " / "))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
            }
        }
    }

    private func decisionColor(_ action: String) -> Color {
        switch action.lowercased() {
        case "direct": return .blue
        case "block", "reject": return .red
        default: return .green
        }
    }
}

private struct MacPolicyGroupHealthRow: View {
    var group: PolicyGroupPayload
    var onSelect: (String) -> Void

    private var selected: String {
        group.selectedChain.isEmpty ? group.selected : group.selectedChain
    }

    private var selectedResult: PolicyProbeResultPayload? {
        group.results.first(where: { $0.chainName == selected })
    }

    private var isManual: Bool {
        group.type.caseInsensitiveCompare("select") == .orderedSame ||
            group.selectionMode.caseInsensitiveCompare("manual") == .orderedSame
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Circle()
                    .fill(healthColor)
                    .frame(width: 9, height: 9)
                Text(group.name.isEmpty ? "Policy group" : group.name)
                    .font(.subheadline.weight(.medium))
                    .lineLimit(1)
                Spacer(minLength: 8)
                if let result = selectedResult, result.latencyNs > 0 {
                    Text(formatDurationNs(result.latencyNs))
                        .font(.caption.weight(.semibold))
                        .monospacedDigit()
                        .foregroundStyle(.secondary)
                }
                Text(selected.isEmpty ? "--" : selected)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            if isManual && !group.chains.isEmpty {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 6) {
                        ForEach(group.chains, id: \.self) { chain in
                            Button {
                                onSelect(chain)
                            } label: {
                                HStack(spacing: 4) {
                                    if chain == selected {
                                        Image(systemName: "checkmark")
                                            .font(.caption2.weight(.bold))
                                    }
                                    Text(chain)
                                        .font(.caption)
                                }
                                .padding(.horizontal, 8)
                                .padding(.vertical, 4)
                                .background(
                                    chain == selected ? Color.accentColor.opacity(0.15) : Color.secondary.opacity(0.08),
                                    in: Capsule()
                                )
                                .foregroundStyle(chain == selected ? Color.accentColor : Color.primary)
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }
            }
        }
        .padding(10)
        .background(Color.secondary.opacity(0.05), in: RoundedRectangle(cornerRadius: 8))
    }

    private var healthColor: Color {
        guard let result = selectedResult else { return .secondary }
        return result.healthy ? .green : .orange
    }
}

private struct MiniActivityRow: View {
    var connection: TrafficConnectionPayload

    private var isActive: Bool { connection.state.lowercased() == "active" }

    private var hostLabel: String {
        let host = connection.targetHost.isEmpty ? connection.target : connection.targetHost
        if !connection.targetPort.isEmpty && connection.targetPort != "0" {
            return "\(host):\(connection.targetPort)"
        }
        return host
    }

    private var actionColor: Color {
        switch connection.actionFamily {
        case "block": return .red
        case "direct": return .blue
        default: return .green
        }
    }

    var body: some View {
        HStack(spacing: 8) {
            Circle()
                .fill(actionColor)
                .frame(width: 8, height: 8)
            VStack(alignment: .leading, spacing: 1) {
                Text(emptyDash(hostLabel))
                    .font(.caption)
                    .lineLimit(1)
                    .truncationMode(.middle)
                if !connection.application.isEmpty {
                    Text(connection.application)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            Spacer(minLength: 8)
            VStack(alignment: .trailing, spacing: 1) {
                if isActive {
                    HStack(spacing: 3) {
                        Circle().fill(Color.green).frame(width: 5, height: 5)
                        Text("active").font(.caption2).foregroundStyle(.green)
                    }
                } else {
                    Text(timeAgoShort(connection.startTsNs))
                        .font(.caption2.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
            }
        }
        .padding(.vertical, 1)
    }
}

private struct MacActionBadge: View {
    var label: String
    var color: Color

    var body: some View {
        Text(label)
            .font(.caption2.weight(.semibold))
            .monospacedDigit()
            .foregroundStyle(color)
            .padding(.horizontal, 6)
            .padding(.vertical, 3)
            .background(color.opacity(0.12), in: Capsule())
    }
}

// MARK: - Profiles

struct MacProfilesSection: View {
    @ObservedObject var model: AppleAppModel

    // Config editor state
    @State private var showEditor = false
    @State private var editorText = ""
    @State private var editorSaveError = ""
    @State private var editorValidationResult = ""
    @State private var editorValidationOK = false

    // Config path validation badge
    @State private var pathValidationResult = ""
    @State private var pathValidationOK = false

    // Import state
    @State private var importError = ""
    @State private var showImportConfirm = false
    @State private var importPreviewProfiles: [String] = []
    @State private var pendingImportText = ""

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                profileHeader
                Divider()
                configFileSection
                Divider()
                subscriptionsSection
                if hasIssues {
                    Divider()
                    issuesSection
                }
                Divider()
                ServerListView(servers: model.dashboard.servers)
            }
            .padding(20)
        }
        .sheet(isPresented: $showEditor) {
            configEditorSheet
        }
        .confirmationDialog(
            "Replace Config?",
            isPresented: $showImportConfirm,
            titleVisibility: .visible
        ) {
            Button("Replace Config", role: .destructive) {
                do {
                    try model.writeConfigFile(pendingImportText)
                    model.reloadDaemon()
                    refreshPathValidation()
                } catch {
                    importError = error.localizedDescription
                }
                pendingImportText = ""
            }
            Button("Cancel", role: .cancel) { pendingImportText = "" }
        } message: {
            Text("This will overwrite the current config with the imported file. Profiles found: \(importPreviewProfiles.joined(separator: ", "))")
        }
        .onAppear { refreshPathValidation() }
    }

    // MARK: - Profile header

    private var profileHeader: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Active Profile")
                .font(.headline)
            if model.dashboard.profiles.profiles.isEmpty {
                AppRecoveryStatePanel(
                    state: model.noProfileRecoveryState ?? AppRecoveryStateBuilder.noProfile(),
                    showsDiagnostic: false
                ) { action in
                    model.performAppRecoveryAction(action)
                }
            } else {
                Picker("Profile", selection: Binding(
                    get: { model.dashboard.activeProfile },
                    set: { model.selectProfile($0) }
                )) {
                    ForEach(model.dashboard.profiles.profiles, id: \.self) { profile in
                        HStack {
                            Text(profile)
                            if profile == model.dashboard.activeProfile {
                                Image(systemName: "checkmark.circle.fill")
                                    .foregroundStyle(.green)
                            }
                        }
                        .tag(profile)
                    }
                }
                .pickerStyle(.menu)
            }
            if let issue = model.dashboard.recoveryIssue {
                Label(issue.title, systemImage: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
                    .font(.caption)
            }
        }
    }

    // MARK: - Config file

    private var configFileSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Config File")
                .font(.headline)
            let path = model.settingsStore.settings.daemonConfigPath
            if path.isEmpty {
                Label("No config file configured. Set it in Settings → Daemon.", systemImage: "exclamationmark.circle")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                HStack(spacing: 4) {
                    Image(systemName: "doc.text")
                        .foregroundStyle(.secondary)
                    Text(path)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                if !pathValidationResult.isEmpty {
                    Label(pathValidationResult, systemImage: pathValidationOK ? "checkmark.circle.fill" : "xmark.circle.fill")
                        .font(.caption)
                        .foregroundStyle(pathValidationOK ? Color.green : Color.red)
                }
            }
            if !importError.isEmpty {
                Text(importError)
                    .font(.caption)
                    .foregroundStyle(.red)
            }
            HStack(spacing: 8) {
                Button {
                    do {
                        editorText = try model.readConfigFile()
                        editorSaveError = ""
                        editorValidationResult = ""
                        showEditor = true
                    } catch {
                        importError = error.localizedDescription
                    }
                } label: {
                    Label("Edit", systemImage: "pencil")
                }
                .disabled(model.settingsStore.settings.daemonConfigPath.isEmpty)

                Button {
                    runImport()
                } label: {
                    Label("Import", systemImage: "square.and.arrow.down")
                }

                if let exportText = try? model.readConfigFile() {
                    ShareLink(
                        item: exportText,
                        subject: Text("clambhook config"),
                        message: Text("clambhook TOML configuration export")
                    ) {
                        Label("Export", systemImage: "square.and.arrow.up")
                    }
                } else {
                    Button {
                        importError = "Cannot read config file for export."
                    } label: {
                        Label("Export", systemImage: "square.and.arrow.up")
                    }
                    .disabled(model.settingsStore.settings.daemonConfigPath.isEmpty)
                }
            }
            .buttonStyle(.borderless)
        }
    }

    // MARK: - Config editor sheet

    private var configEditorSheet: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Edit Config")
                    .font(.headline)
                Spacer()
                Button("Cancel") { showEditor = false }
            }
            .padding([.horizontal, .top], 16)
            .padding(.bottom, 8)
            Divider()
            TextEditor(text: $editorText)
                .font(.system(.caption, design: .monospaced))
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .padding(8)
            Divider()
            VStack(alignment: .leading, spacing: 6) {
                if !editorValidationResult.isEmpty {
                    Label(editorValidationResult, systemImage: editorValidationOK ? "checkmark.circle.fill" : "xmark.circle.fill")
                        .font(.caption)
                        .foregroundStyle(editorValidationOK ? Color.green : Color.red)
                }
                if !editorSaveError.isEmpty {
                    Text(editorSaveError)
                        .font(.caption)
                        .foregroundStyle(.red)
                }
                HStack(spacing: 8) {
                    Button("Validate") {
                        validateEditorContent()
                    }
                    Spacer()
                    Button("Save") {
                        saveEditorContent()
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(editorText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
            .padding(12)
        }
        .frame(width: 640, height: 520)
    }

    private func validateEditorContent() {
        do {
            _ = try TunnelImportDecoder.decode(editorText)
            let profileCount = editorText.components(separatedBy: "[[profile]]").count - 1
            editorValidationResult = "Valid TOML · \(profileCount) profile\(profileCount == 1 ? "" : "s")"
            editorValidationOK = true
        } catch {
            editorValidationResult = error.localizedDescription
            editorValidationOK = false
        }
    }

    private func saveEditorContent() {
        do {
            try model.writeConfigFile(editorText)
            model.reloadDaemon()
            showEditor = false
            refreshPathValidation()
        } catch {
            editorSaveError = error.localizedDescription
        }
    }

    // MARK: - Import

    private func runImport() {
        let panel = NSOpenPanel()
        panel.title = "Import clambhook config"
        panel.allowedContentTypes = [.init(filenameExtension: "toml") ?? .data]
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = false
        if panel.runModal() == .OK, let url = panel.url {
            do {
                let text = try String(contentsOf: url, encoding: .utf8)
                _ = try TunnelImportDecoder.decode(text)
                importPreviewProfiles = profileNames(in: text)
                pendingImportText = text
                importError = ""
                showImportConfirm = true
            } catch {
                importError = error.localizedDescription
            }
        }
    }

    private func profileNames(in toml: String) -> [String] {
        var names: [String] = []
        var inProfile = false
        for line in toml.components(separatedBy: "\n") {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed == "[[profile]]" { inProfile = true; continue }
            if trimmed.hasPrefix("[[") { inProfile = false; continue }
            if inProfile, trimmed.lowercased().hasPrefix("name") {
                let parts = trimmed.split(separator: "=", maxSplits: 1)
                if parts.count == 2 {
                    let raw = parts[1].trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "\"' "))
                    if !raw.isEmpty { names.append(raw) }
                }
            }
        }
        return names.isEmpty ? ["(unknown)"] : names
    }

    // MARK: - Path validation

    private func refreshPathValidation() {
        let path = model.settingsStore.settings.daemonConfigPath
        guard !path.isEmpty else {
            pathValidationResult = ""
            return
        }
        do {
            let text = try model.readConfigFile()
            let profileCount = text.components(separatedBy: "[[profile]]").count - 1
            pathValidationResult = "Valid TOML · \(profileCount) profile\(profileCount == 1 ? "" : "s")"
            pathValidationOK = true
        } catch {
            pathValidationResult = error.localizedDescription
            pathValidationOK = false
        }
    }

    // MARK: - Subscriptions

    private var subscriptionsSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("Rule Subscriptions")
                    .font(.headline)
                Spacer()
                Button {
                    model.refreshActiveProfileRuleSubscriptions()
                } label: {
                    Label("Refresh All", systemImage: "arrow.clockwise")
                }
                .buttonStyle(.borderless)
                .font(.caption)
            }
            if model.dashboard.ruleSubscriptions.subscriptions.isEmpty {
                Text("No subscriptions for this profile")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(model.dashboard.ruleSubscriptions.subscriptions) { sub in
                    MacSubscriptionRow(subscription: sub) { updated in
                        var subs = model.dashboard.ruleSubscriptions.subscriptions
                        if let idx = subs.firstIndex(where: { $0.id == updated.id }) {
                            subs[idx] = updated
                        }
                        try? model.replaceActiveProfileRuleSubscriptions(subs)
                    }
                }
            }
        }
    }

    // MARK: - Issues

    private var hasIssues: Bool {
        model.dashboard.recoveryIssue != nil ||
        !model.dashboard.traffic.cleanupSuggestions.isEmpty ||
        (!model.dashboard.errorText.isEmpty && model.dashboard.recoveryIssue == nil)
    }

    private var issuesSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Issues")
                .font(.headline)
            if let issue = model.dashboard.recoveryIssue {
                MacIssueCard(
                    title: issue.title,
                    message: issue.message,
                    severity: issue.kind == .generic ? .secondary : .orange
                )
            } else if !model.dashboard.errorText.isEmpty {
                Text(model.dashboard.errorText)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            ForEach(model.dashboard.traffic.cleanupSuggestions) { suggestion in
                HStack(alignment: .top, spacing: 10) {
                    Image(systemName: "exclamationmark.triangle")
                        .foregroundStyle(.orange)
                        .frame(width: 18)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(suggestion.targetRuleName.isEmpty ? suggestion.ruleName : suggestion.targetRuleName)
                            .font(.caption.weight(.semibold))
                        Text(suggestion.message)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    Spacer(minLength: 8)
                    if !suggestion.operation.isEmpty {
                        Button(suggestion.operation == "move_rule_to_end" ? "Move" : "Delete") {
                            model.applyCleanupSuggestion(suggestion)
                        }
                        .buttonStyle(.borderless)
                        .font(.caption)
                        .foregroundStyle(suggestion.operation == "delete_rule" ? Color.red : Color.accentColor)
                    }
                }
                .padding(8)
                .background(Color.orange.opacity(0.07), in: RoundedRectangle(cornerRadius: 8))
            }
        }
    }
}

private struct MacSubscriptionRow: View {
    var subscription: RuleSubscriptionPayload
    var onUpdate: ((RuleSubscriptionPayload) -> Void)?

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 6) {
                Text(subscription.name.isEmpty ? "(unnamed)" : subscription.name)
                    .font(.caption.weight(.semibold))
                    .lineLimit(1)
                Spacer()
                Toggle("", isOn: Binding(
                    get: { !subscription.disabled },
                    set: { enabled in
                        var updated = subscription
                        updated.disabled = !enabled
                        onUpdate?(updated)
                    }
                ))
                .toggleStyle(.switch)
                .controlSize(.mini)
                .labelsHidden()
                statusChip
            }
            if !subscription.url.isEmpty {
                Text(subscription.url)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            HStack(spacing: 8) {
                Text(subscription.format.isEmpty ? "auto" : subscription.format.uppercased())
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                if subscription.domainCount + subscription.cidrCount > 0 {
                    Text("\(subscription.domainCount) domains / \(subscription.cidrCount) CIDRs")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
            let err = subscription.lastError.isEmpty ? subscription.cacheError : subscription.lastError
            if !err.isEmpty {
                Text(err)
                    .font(.caption2)
                    .foregroundStyle(.red)
                    .lineLimit(2)
            }
        }
        .padding(8)
        .background(Color.secondary.opacity(0.05), in: RoundedRectangle(cornerRadius: 8))
    }

    private var statusChip: some View {
        Group {
            if subscription.disabled {
                Label("Disabled", systemImage: "pause.circle")
                    .foregroundStyle(Color.secondary)
            } else if !subscription.lastError.isEmpty || !subscription.cacheError.isEmpty {
                Label("Error", systemImage: "xmark.circle.fill")
                    .foregroundStyle(Color.red)
            } else if subscription.cached {
                Label("Cached", systemImage: "checkmark.circle.fill")
                    .foregroundStyle(Color.green)
            } else {
                Label("Pending", systemImage: "clock")
                    .foregroundStyle(Color.secondary)
            }
        }
        .font(.caption2.weight(.medium))
        .padding(.horizontal, 6)
        .padding(.vertical, 3)
        .background(Color.secondary.opacity(0.08), in: Capsule())
    }
}

private struct MacIssueCard: View {
    var title: String
    var message: String
    var severity: Color

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(severity)
                .frame(width: 18)
            VStack(alignment: .leading, spacing: 3) {
                Text(title)
                    .font(.caption.weight(.semibold))
                if !message.isEmpty {
                    Text(message)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .padding(10)
        .background(severity.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
    }
}

// MARK: - Policy Groups

struct MacPolicyGroupsSection: View {
    @ObservedObject var model: AppleAppModel
    @State private var testingGroup: String = ""

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                HStack {
                    Text("Policy Groups")
                        .font(.headline)
                    Spacer()
                    Button {
                        testingGroup = ""
                        Task { await model.dashboard.testPolicyGroup() }
                    } label: {
                        Label("Test All", systemImage: "speedometer")
                    }
                    .buttonStyle(.borderless)
                    .font(.caption)
                }
                CompactPolicySelectorView(
                    summary: model.dashboard.policySelectorSummary,
                    groups: model.dashboard.policyGroups.groups,
                    onSelect: { group, chain in
                        model.selectPolicyGroup(group: group, chain: chain)
                    },
                    onTest: { group in
                        testingGroup = group
                        Task {
                            await model.dashboard.testPolicyGroup(group: group)
                            testingGroup = ""
                        }
                    }
                )
            }
            .padding(20)
        }
    }
}

// MARK: - Rules

struct MacRulesSection: View {
    @ObservedObject var model: AppleAppModel

    // Editor state
    @State private var isEditing = false
    @State private var draftRows: [RuleEditorRow] = []
    @State private var saveError = ""
    @State private var showAddSheet = false

    // Route tester / explain state
    @State private var routeTestNetwork = "tcp"
    @State private var routeTestTarget = "example.com:443"
    @State private var routeTestSource = ""
    @State private var testResult: RuleTestResponse?
    @State private var explainResult: RuleTestResponse?
    @State private var testerError = ""

    var body: some View {
        HSplitView {
            rulesPanel
                .frame(minWidth: 300)
            testerPanel
                .frame(minWidth: 240)
        }
        .sheet(isPresented: $showAddSheet) {
            MacRuleAddSheet(
                chainNames: model.dashboard.servers.chains.map { $0.name },
                policyGroupNames: model.dashboard.policyGroups.groups.map { $0.name }
            ) { newRow in
                draftRows.append(newRow)
            }
        }
        .onChange(of: model.dashboard.rules.rules) {
            if !isEditing { rebuildDraftRows() }
        }
        .onAppear { rebuildDraftRows() }
    }

    // MARK: - Left panel: ordered rule list / editor

    private var rulesPanel: some View {
        VStack(alignment: .leading, spacing: 0) {
            rulesPanelHeader
            Divider()
            if !saveError.isEmpty {
                Text(saveError)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .padding(.horizontal, 16)
                    .padding(.top, 8)
            }
            rulesList
            if !model.dashboard.rules.ruleSets.isEmpty {
                Divider()
                ruleSetsSection
            }
        }
    }

    private var rulesPanelHeader: some View {
        HStack(spacing: 8) {
            Text("Rules")
                .font(.headline)
            Spacer()
            if isEditing {
                Button {
                    showAddSheet = true
                } label: {
                    Image(systemName: "plus")
                }
                .buttonStyle(.borderless)
                Button("Cancel") {
                    isEditing = false
                    saveError = ""
                    rebuildDraftRows()
                }
                .buttonStyle(.borderless)
                Button("Save") {
                    saveError = ""
                    do {
                        let chainNames = model.dashboard.servers.chains.map { $0.name }
                        let policyGroupNames = model.dashboard.policyGroups.groups.map { $0.name }
                        let defaultChainName = model.dashboard.servers.chains.first?.name ?? ""
                        _ = try RuleEditor.rules(
                            from: draftRows,
                            chainNames: chainNames,
                            policyGroupNames: policyGroupNames,
                            defaultChainName: defaultChainName
                        )
                        model.saveRules(draftRows)
                        isEditing = false
                    } catch let err as RuleEditorValidationFailure {
                        saveError = err.localizedDescription
                    } catch {
                        saveError = error.localizedDescription
                    }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
            } else {
                Button {
                    isEditing = true
                    saveError = ""
                    rebuildDraftRows()
                } label: {
                    Label("Edit", systemImage: "pencil")
                }
                .buttonStyle(.borderless)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
    }

    private var rulesList: some View {
        List {
            if isEditing {
                ForEach($draftRows) { $row in
                    RuleEditorRowView(
                        row: $row,
                        chainNames: model.dashboard.servers.chains.map { $0.name },
                        policyGroupNames: model.dashboard.policyGroups.groups.map { $0.name }
                    )
                    .listRowSeparator(.visible)
                }
                .onMove { from, to in draftRows.move(fromOffsets: from, toOffset: to) }
                .onDelete { offsets in draftRows.remove(atOffsets: offsets) }
            } else {
                if draftRows.isEmpty {
                    Text("No routing rules")
                        .foregroundStyle(.secondary)
                        .listRowSeparator(.hidden)
                } else {
                    ForEach(Array(draftRows.enumerated()), id: \.element.id) { index, row in
                        RuleReadOnlyRowView(index: index, row: row)
                            .listRowSeparator(.visible)
                    }
                }
            }
        }
        .listStyle(.plain)
    }

    private var ruleSetsSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("Rule Sets")
                    .font(.subheadline.weight(.semibold))
                Spacer()
                Button {
                    model.refreshActiveProfileRuleSets()
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.borderless)
            }
            .padding(.horizontal, 16)
            .padding(.top, 10)
            ForEach(model.dashboard.rules.ruleSets) { rs in
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(rs.name).fontWeight(.medium).font(.caption)
                        Text(rs.url).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
                    }
                    Spacer()
                    VStack(alignment: .trailing, spacing: 2) {
                        Text(rs.cached ? "Cached" : "Not cached")
                            .font(.caption2)
                            .foregroundStyle(rs.cached ? .green : .secondary)
                        if rs.domainCount + rs.cidrCount > 0 {
                            Text("\(rs.domainCount)d / \(rs.cidrCount)c")
                                .font(.caption2).foregroundStyle(.secondary)
                        }
                    }
                }
                .padding(.horizontal, 16)
            }
            Spacer(minLength: 12)
        }
    }

    // MARK: - Right panel: route tester + explain

    private var testerPanel: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                Text("Route Tester")
                    .font(.headline)
                testerControls
                if !testerError.isEmpty {
                    Text(testerError)
                        .font(.caption)
                        .foregroundStyle(.red)
                }
                if let result = testResult {
                    RouteResultCard(title: "Test Result", result: result, showHops: false)
                }
                if let result = explainResult {
                    RouteResultCard(title: "Explain Result", result: result, showHops: true)
                }
            }
            .padding(16)
        }
    }

    private var testerControls: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Picker("Network", selection: $routeTestNetwork) {
                    Text("TCP").tag("tcp")
                    Text("UDP").tag("udp")
                }
                .labelsHidden()
                .pickerStyle(.segmented)
                .frame(width: 110)
                TextField("host:port", text: $routeTestTarget)
                    .textFieldStyle(.roundedBorder)
            }
            TextField("Source IP (optional)", text: $routeTestSource)
                .textFieldStyle(.roundedBorder)
                .font(.caption)
            HStack(spacing: 8) {
                Button {
                    testerError = ""
                    testResult = nil
                    Task {
                        do {
                            testResult = try await model.testRule(
                                network: routeTestNetwork,
                                target: routeTestTarget
                            )
                        } catch {
                            testerError = error.localizedDescription
                        }
                    }
                } label: {
                    Label("Test", systemImage: "checkmark.circle")
                }
                Button {
                    testerError = ""
                    explainResult = nil
                    Task {
                        do {
                            explainResult = try await model.explainRoute(
                                network: routeTestNetwork,
                                target: routeTestTarget,
                                source: routeTestSource
                            )
                        } catch {
                            testerError = error.localizedDescription
                        }
                    }
                } label: {
                    Label("Explain", systemImage: "questionmark.circle")
                }
            }
        }
    }

    // MARK: - Helpers

    private func rebuildDraftRows() {
        let defaultChain = model.dashboard.servers.chains.first?.name ?? ""
        draftRows = RuleEditor.rows(
            from: model.dashboard.rules.rules,
            defaultChainName: defaultChain,
            includeVirtualFinal: true
        )
    }
}

// MARK: Rule read-only row

private struct RuleReadOnlyRowView: View {
    var index: Int
    var row: RuleEditorRow

    var body: some View {
        HStack(spacing: 10) {
            Text("\(index + 1)")
                .font(.caption2.monospacedDigit())
                .foregroundStyle(.secondary)
                .frame(width: 22, alignment: .trailing)
            MatcherChip(kind: row.matcherKind, value: row.matcherSummary)
            Text("→")
                .foregroundStyle(.secondary)
                .font(.caption)
            PolicyBadge(row: row)
            Spacer()
            Text(row.name)
                .font(.caption2)
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .padding(.vertical, 2)
    }
}

// MARK: Rule editor row (edit mode)

private struct RuleEditorRowView: View {
    @Binding var row: RuleEditorRow
    var chainNames: [String]
    var policyGroupNames: [String]

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Picker("Type", selection: $row.matcherKind) {
                    ForEach(RuleMatcherKind.editableCases) { kind in
                        Text(kind.displayName).tag(kind)
                    }
                }
                .labelsHidden()
                .frame(width: 150)
                if row.matcherKind != .allTraffic {
                    TextField(row.matcherKind.placeholder, text: $row.value)
                        .textFieldStyle(.roundedBorder)
                        .font(.caption)
                }
            }
            HStack(spacing: 8) {
                Picker("Policy", selection: $row.policyKind) {
                    ForEach(RulePolicyKind.allCases) { kind in
                        Text(kind.displayName).tag(kind)
                    }
                }
                .labelsHidden()
                .frame(width: 90)
                if row.policyKind == .proxy {
                    Picker("Chain", selection: $row.chainName) {
                        ForEach(chainNames, id: \.self) { name in
                            Text(name).tag(name)
                        }
                    }
                    .labelsHidden()
                    .frame(width: 120)
                } else if row.policyKind == .group {
                    Picker("Group", selection: $row.chainName) {
                        ForEach(policyGroupNames, id: \.self) { name in
                            Text(name).tag(name)
                        }
                    }
                    .labelsHidden()
                    .frame(width: 120)
                }
                Spacer()
                TextField("Name", text: $row.name)
                    .textFieldStyle(.roundedBorder)
                    .font(.caption)
                    .frame(width: 120)
            }
        }
        .padding(.vertical, 2)
        .opacity(row.isGenerated ? 0.5 : 1)
        .disabled(row.isGenerated)
    }
}

// MARK: Matcher chip

private struct MatcherChip: View {
    var kind: RuleMatcherKind
    var value: String

    var body: some View {
        Text(value)
            .font(.caption.weight(.medium))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(chipColor.opacity(0.15))
            .foregroundStyle(chipColor)
            .clipShape(RoundedRectangle(cornerRadius: 4))
            .lineLimit(1)
    }

    private var chipColor: Color {
        switch kind {
        case .domain, .domainSuffix, .domainKeyword: return .blue
        case .cidr: return .orange
        case .port: return .purple
        case .network: return .teal
        case .allTraffic: return .gray
        case .combined: return .indigo
        }
    }
}

// MARK: Policy badge

private struct PolicyBadge: View {
    var row: RuleEditorRow

    var body: some View {
        Text(row.policySummary)
            .font(.caption.weight(.medium))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(badgeColor.opacity(0.15))
            .foregroundStyle(badgeColor)
            .clipShape(RoundedRectangle(cornerRadius: 4))
            .lineLimit(1)
    }

    private var badgeColor: Color {
        switch row.policyKind {
        case .direct: return .green
        case .block, .reject: return .red
        case .proxy: return .blue
        case .group: return .purple
        }
    }
}

// MARK: Route result card

private struct RouteResultCard: View {
    var title: String
    var result: RuleTestResponse
    var showHops: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title)
                .font(.subheadline.weight(.semibold))
            Divider()
            HStack(spacing: 8) {
                actionBadge
                VStack(alignment: .leading, spacing: 2) {
                    let ruleName = result.decision.ruleName.isEmpty ? "Default" : result.decision.ruleName
                    Text(ruleName)
                        .font(.caption.weight(.medium))
                    if result.decision.isDefault {
                        Text("No rule matched")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    } else {
                        Text("Rule #\(result.decision.ruleNumber)")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            if !result.decision.chainName.isEmpty {
                LabeledContent("Chain") {
                    Text(result.decision.chainName).font(.caption)
                }
                .font(.caption)
            }
            if !result.decision.groupName.isEmpty {
                LabeledContent("Group") {
                    Text(result.decision.groupName).font(.caption)
                }
                .font(.caption)
            }
            if result.decision.elapsedNs > 0 {
                LabeledContent("Elapsed") {
                    Text("\(result.decision.elapsedNs / 1_000) µs").font(.caption)
                }
                .font(.caption)
            }
            if showHops, !result.hops.isEmpty {
                Divider()
                Text("Hops")
                    .font(.caption.weight(.semibold))
                ForEach(result.hops) { hop in
                    HStack(spacing: 6) {
                        Text(hop.protocol.uppercased())
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .frame(width: 40, alignment: .leading)
                        Text(hop.name)
                            .font(.caption)
                        Spacer()
                        Text(hop.address)
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
            }
        }
        .padding(10)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
    }

    private var actionBadge: some View {
        let action = result.decision.action
        let color: Color = {
            switch action {
            case "direct": return .green
            case "block", "reject": return .red
            default: return action.hasPrefix("group:") ? .purple : .blue
            }
        }()
        return Text(action)
            .font(.caption.weight(.bold))
            .padding(.horizontal, 7)
            .padding(.vertical, 3)
            .background(color.opacity(0.15))
            .foregroundStyle(color)
            .clipShape(RoundedRectangle(cornerRadius: 5))
    }
}

// MARK: - Add Rule Sheet

struct MacRuleAddSheet: View {
    var chainNames: [String]
    var policyGroupNames: [String]
    var onAdd: (RuleEditorRow) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var matcherKind = RuleMatcherKind.domainSuffix
    @State private var value = ""
    @State private var policyKind = RulePolicyKind.direct
    @State private var chainName = ""
    @State private var validationError = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Add Rule")
                .font(.headline)

            TextField("Rule name", text: $name)
                .textFieldStyle(.roundedBorder)

            Picker("Match type", selection: $matcherKind) {
                ForEach(RuleMatcherKind.editableCases) { kind in
                    Text(kind.displayName).tag(kind)
                }
            }
            .pickerStyle(.menu)

            if matcherKind != .allTraffic {
                TextField(matcherKind.placeholder, text: $value)
                    .textFieldStyle(.roundedBorder)
            }

            Picker("Action", selection: $policyKind) {
                ForEach(RulePolicyKind.allCases) { kind in
                    Text(kind.displayName).tag(kind)
                }
            }
            .pickerStyle(.menu)
            .onChange(of: policyKind) { chainName = "" }

            if policyKind == .proxy {
                Picker("Chain", selection: $chainName) {
                    Text("(select chain)").tag("")
                    ForEach(chainNames, id: \.self) { n in Text(n).tag(n) }
                }
                .pickerStyle(.menu)
            } else if policyKind == .group {
                Picker("Group", selection: $chainName) {
                    Text("(select group)").tag("")
                    ForEach(policyGroupNames, id: \.self) { n in Text(n).tag(n) }
                }
                .pickerStyle(.menu)
            }

            if !validationError.isEmpty {
                Text(validationError)
                    .font(.caption)
                    .foregroundStyle(.red)
            }

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                Button("Add") { addRule() }
                    .buttonStyle(.borderedProminent)
                    .disabled(name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 340)
        .onAppear {
            chainName = chainNames.first ?? ""
        }
    }

    private func addRule() {
        validationError = ""
        let trimmedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let row = RuleEditorRow(
            name: trimmedName,
            matcherKind: matcherKind,
            value: matcherKind == .allTraffic ? "" : value.trimmingCharacters(in: .whitespacesAndNewlines),
            policyKind: policyKind,
            chainName: chainName
        )
        let errors = RuleEditor.validate(
            rows: [row],
            chainNames: chainNames,
            policyGroupNames: policyGroupNames
        )
        if let first = errors.first {
            validationError = first.message
            return
        }
        onAdd(row)
        dismiss()
    }
}

// MARK: - DNS

struct MacDNSSection: View {
    @ObservedObject var model: AppleAppModel
    @State private var showAddUpstreamSheet = false
    @State private var saveError = ""

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                dnsOverview
                Divider()
                upstreamsSection
                if !model.dashboard.dns.upstreamRoutes.isEmpty {
                    Divider()
                    routesTable
                }
            }
            .padding(20)
        }
        .sheet(isPresented: $showAddUpstreamSheet) {
            MacDNSUpstreamSheet { upstream in
                var upstreams = model.dashboard.dns.upstreams
                upstreams.append(upstream)
                Task {
                    await model.dashboard.updateDNS(
                        enabled: model.dashboard.dns.enabled,
                        timeout: model.dashboard.dns.timeout,
                        upstreams: upstreams
                    )
                }
            }
        }
    }

    private var dnsOverview: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("DNS Configuration")
                    .font(.headline)
                Spacer()
                Toggle("Encrypted DNS", isOn: Binding(
                    get: { model.dashboard.dns.enabled },
                    set: { enabled in
                        Task {
                            await model.dashboard.updateDNS(
                                enabled: enabled,
                                timeout: model.dashboard.dns.timeout,
                                upstreams: model.dashboard.dns.upstreams
                            )
                        }
                    }
                ))
                .toggleStyle(.switch)
                .controlSize(.small)
                .labelsHidden()
            }
            HStack(spacing: 16) {
                Label(model.dashboard.dns.enabled ? "Enabled" : "Disabled", systemImage: model.dashboard.dns.enabled ? "checkmark.circle.fill" : "xmark.circle")
                    .foregroundStyle(model.dashboard.dns.enabled ? .green : .secondary)
                Label("Strategy: \(model.dashboard.dns.strategy)", systemImage: "arrow.triangle.branch")
                    .foregroundStyle(.secondary)
                if !model.dashboard.dns.timeout.isEmpty {
                    Label("Timeout: \(model.dashboard.dns.timeout)", systemImage: "clock")
                        .foregroundStyle(.secondary)
                }
                if model.dashboard.dns.interceptsPort53 {
                    Label("Intercepts port 53", systemImage: "shield.lefthalf.filled")
                        .foregroundStyle(.blue)
                }
            }
            .font(.subheadline)
        }
    }

    private var upstreamsSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("Upstreams")
                    .font(.headline)
                Spacer()
                Button {
                    showAddUpstreamSheet = true
                } label: {
                    Label("Add", systemImage: "plus")
                }
                .buttonStyle(.borderless)
                .font(.caption)
            }
            if model.dashboard.dns.upstreams.isEmpty {
                Text("No upstreams configured. Add a DoH, DoT, or DoQ upstream.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(model.dashboard.dns.upstreams) { upstream in
                    HStack(spacing: 10) {
                        VStack(alignment: .leading, spacing: 3) {
                            Text(upstream.name.isEmpty ? upstream.id : upstream.name)
                                .font(.subheadline.weight(.medium))
                                .lineLimit(1)
                            HStack(spacing: 6) {
                                Text(upstream.protocol.uppercased())
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                Text(upstream.targetDescription)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                    .lineLimit(1)
                                    .truncationMode(.middle)
                            }
                        }
                        Spacer(minLength: 8)
                        Button(role: .destructive) {
                            let remaining = model.dashboard.dns.upstreams.filter { $0.id != upstream.id }
                            Task {
                                await model.dashboard.updateDNS(
                                    enabled: model.dashboard.dns.enabled,
                                    timeout: model.dashboard.dns.timeout,
                                    upstreams: remaining
                                )
                            }
                        } label: {
                            Image(systemName: "trash")
                                .foregroundStyle(.red)
                        }
                        .buttonStyle(.plain)
                        .help("Remove \(upstream.name.isEmpty ? upstream.id : upstream.name)")
                    }
                    .padding(.vertical, 2)
                    Divider()
                }
            }
            if !saveError.isEmpty {
                Text(saveError)
                    .font(.caption)
                    .foregroundStyle(.red)
            }
        }
    }

    private var upstreamsTable: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Upstreams")
                .font(.headline)
            Table(model.dashboard.dns.upstreams) {
                TableColumn("Name") { upstream in
                    Text(upstream.name.isEmpty ? upstream.id : upstream.name)
                }
                TableColumn("Protocol") { upstream in
                    Text(upstream.protocol.uppercased())
                }
                TableColumn("Address / URL") { upstream in
                    Text(upstream.targetDescription)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                TableColumn("Bootstrap IPs") { upstream in
                    Text(upstream.bootstrapIPs.isEmpty ? "--" : upstream.bootstrapIPs.joined(separator: ", "))
                        .font(.caption)
                        .lineLimit(1)
                }
            }
        }
    }

    private var routesTable: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Upstream Routes")
                .font(.headline)
            Table(model.dashboard.dns.upstreamRoutes) {
                TableColumn("Name") { route in
                    Text(route.name.isEmpty ? route.id : route.name)
                }
                TableColumn("Network") { route in
                    Text(route.network.isEmpty ? "all" : route.network)
                }
                TableColumn("Action") { route in
                    Text(route.action)
                }
                TableColumn("Target") { route in
                    Text(route.target)
                        .lineLimit(1)
                }
                TableColumn("Chain") { route in
                    Text(route.chainName.isEmpty ? "--" : route.chainName)
                }
            }
        }
    }
}

// MARK: - DNS Upstream Add Sheet

struct MacDNSUpstreamSheet: View {
    var onAdd: (DNSUpstreamPayload) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var proto = "doh"
    @State private var url = ""
    @State private var address = ""
    @State private var serverName = ""
    @State private var bootstrapIPs = ""
    @State private var validationError = ""

    private let protocols = ["doh", "dot", "doq"]

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Add DNS Upstream")
                    .font(.headline)
                Spacer()
                Button("Cancel") { dismiss() }
            }
            .padding([.horizontal, .top], 16)
            .padding(.bottom, 8)
            Divider()
            Form {
                TextField("Name (optional)", text: $name)
                Picker("Protocol", selection: $proto) {
                    ForEach(protocols, id: \.self) { p in
                        Text(p.uppercased()).tag(p)
                    }
                }
                if proto == "doh" {
                    TextField("URL (https://...)", text: $url)
                } else {
                    TextField("Address (host:port)", text: $address)
                    TextField("Server Name (TLS SNI, optional)", text: $serverName)
                }
                TextField("Bootstrap IPs (comma-separated, optional)", text: $bootstrapIPs)
            }
            .padding(12)
            Divider()
            VStack(alignment: .leading, spacing: 6) {
                if !validationError.isEmpty {
                    Text(validationError)
                        .font(.caption)
                        .foregroundStyle(.red)
                }
                HStack {
                    Spacer()
                    Button("Add Upstream") {
                        guard validate() else { return }
                        let ips = bootstrapIPs.split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
                        let upstream = DNSUpstreamPayload(
                            name: name,
                            protocol: proto,
                            url: proto == "doh" ? url : "",
                            address: proto != "doh" ? address : "",
                            serverName: serverName,
                            bootstrapIPs: ips
                        )
                        onAdd(upstream)
                        dismiss()
                    }
                    .buttonStyle(.borderedProminent)
                }
            }
            .padding(12)
        }
        .frame(width: 440, height: 340)
    }

    private func validate() -> Bool {
        if proto == "doh" && url.isEmpty {
            validationError = "URL is required for DoH"
            return false
        }
        if proto != "doh" && address.isEmpty {
            validationError = "Address is required for \(proto.uppercased())"
            return false
        }
        validationError = ""
        return true
    }
}

// MARK: - Activity

struct MacActivitySection: View {
    @ObservedObject var model: AppleAppModel
    @State private var filterKind: InspectionFilterKind = .all
    @State private var searchQuery = ""
    @State private var selectedID: String?
    @State private var draftRule: RulePayload?
    @State private var sourceConnection: TrafficConnectionPayload?

    private var filteredConnections: [TrafficConnectionPayload] {
        model.dashboard.traffic.inspectionConnections(
            filter: filterKind,
            query: searchQuery,
            pinnedIDs: model.pinnedConnectionIDs
        )
    }

    private var selectedConnection: TrafficConnectionPayload? {
        guard let id = selectedID else { return nil }
        return filteredConnections.first { $0.connID == id }
            ?? model.dashboard.traffic.connections.first { $0.connID == id }
    }

    private var activeCount: Int {
        model.dashboard.traffic.connections.filter { $0.state.lowercased() == "active" }.count
    }

    var body: some View {
        HSplitView {
            connectionListPanel
                .frame(minWidth: 360)
            if let conn = selectedConnection {
                ActivityDetailPanel(
                    connection: conn,
                    fallbackChain: dashboardFallbackProxyChain(model.dashboard),
                    onTemporaryAction: { connection, action in
                        model.createTemporaryRuleFromConnection(connection, action: action)
                    },
                    onPermanentRule: { connection, rule in
                        sourceConnection = connection
                        draftRule = rule
                    }
                )
                .frame(minWidth: 280)
            }
        }
        .sheet(item: $draftRule) { rule in
            MacRuleCreateSheet(model: model, initialRule: rule, sourceConnection: sourceConnection)
        }
    }

    // MARK: - Connection list panel

    private var connectionListPanel: some View {
        VStack(spacing: 0) {
            headerBar
            Divider()
            connectionList
        }
    }

    private var headerBar: some View {
        VStack(spacing: 8) {
            HStack(spacing: 10) {
                Picker("Filter", selection: $filterKind) {
                    Text("All").tag(InspectionFilterKind.all)
                    Text("Active").tag(InspectionFilterKind.active)
                    Text("Proxy").tag(InspectionFilterKind.proxy)
                    Text("Direct").tag(InspectionFilterKind.direct)
                    Text("Block").tag(InspectionFilterKind.block)
                }
                .labelsHidden()
                .pickerStyle(.segmented)
                .frame(maxWidth: 360)
                Spacer()
                statsLabel
            }
            TextField("Search app, host, rule, chain…", text: $searchQuery)
                .textFieldStyle(.roundedBorder)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
    }

    private var statsLabel: some View {
        HStack(spacing: 8) {
            if activeCount > 0 {
                Label("\(activeCount) active", systemImage: "circle.fill")
                    .foregroundStyle(.green)
                    .font(.caption.weight(.medium))
            }
            let summary = model.dashboard.traffic.summary
            if summary.rxBps > 0 || summary.txBps > 0 {
                Text("↓ \(formatRate(summary.rxBps))  ↑ \(formatRate(summary.txBps))")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var connectionList: some View {
        let connections = filteredConnections
        return Group {
            if connections.isEmpty {
                emptyState
            } else {
                List(connections, selection: $selectedID) { connection in
                    ActivityConnectionRow(
                        connection: connection,
                        attributedApp: model.attributedApplication(for: connection)
                    )
                        .tag(connection.connID)
                }
                .listStyle(.plain)
            }
        }
    }

    private var emptyState: some View {
        VStack(spacing: 8) {
            Spacer()
            Image(systemName: "antenna.radiowaves.left.and.right.slash")
                .font(.system(size: 36))
                .foregroundStyle(.quaternary)
            Text(searchQuery.isEmpty ? "No connections" : "No matches")
                .foregroundStyle(.secondary)
            Spacer()
        }
        .frame(maxWidth: .infinity)
    }
}

// MARK: - Activity connection row

private struct ActivityConnectionRow: View {
    var connection: TrafficConnectionPayload
    var attributedApp: String?

    private var appLabel: String {
        if let app = attributedApp, !app.isEmpty { return app }
        if !connection.application.isEmpty { return connection.application }
        return connection.listener.protocol.uppercased()
    }

    private var destinationLabel: String {
        let host = connection.targetHost.isEmpty ? connection.target : connection.targetHost
        if !connection.targetPort.isEmpty && connection.targetPort != "0" {
            return "\(host):\(connection.targetPort)"
        }
        return host
    }

    private var isActive: Bool { connection.state.lowercased() == "active" }

    var body: some View {
        HStack(spacing: 8) {
            ActivityDecisionBadge(actionFamily: connection.actionFamily, compact: true)
            VStack(alignment: .leading, spacing: 2) {
                Text(appLabel)
                    .font(.subheadline.weight(.medium))
                    .lineLimit(1)
                Text(emptyDash(destinationLabel))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            VStack(alignment: .trailing, spacing: 2) {
                if isActive {
                    HStack(spacing: 3) {
                        Circle()
                            .fill(Color.green)
                            .frame(width: 6, height: 6)
                        Text("active")
                            .font(.caption2)
                            .foregroundStyle(.green)
                    }
                    if connection.rxBps > 0 || connection.txBps > 0 {
                        Text("↓ \(formatRate(connection.rxBps))")
                            .font(.caption2.monospacedDigit())
                            .foregroundStyle(.secondary)
                    }
                } else {
                    Text(formatDurationNs(connection.durationNs))
                        .font(.caption2.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
            }
        }
        .padding(.vertical, 2)
    }
}

// MARK: - Activity decision badge

private struct ActivityDecisionBadge: View {
    var actionFamily: String
    var compact: Bool = false

    private var label: String {
        switch actionFamily {
        case "block": return "Block"
        case "direct": return "Direct"
        default: return "Proxy"
        }
    }

    private var icon: String {
        switch actionFamily {
        case "block": return "hand.raised.fill"
        case "direct": return "arrow.up.right"
        default: return "shield.lefthalf.filled"
        }
    }

    private var tint: Color {
        switch actionFamily {
        case "block": return .red
        case "direct": return .blue
        default: return .green
        }
    }

    var body: some View {
        if compact {
            Circle()
                .fill(tint)
                .frame(width: 8, height: 8)
        } else {
            Label(label, systemImage: icon)
                .font(.caption.weight(.semibold))
                .foregroundStyle(tint)
                .padding(.horizontal, 8)
                .padding(.vertical, 4)
                .background(tint.opacity(0.12), in: Capsule())
        }
    }
}

// MARK: - Activity detail panel

private struct ActivityDetailPanel: View {
    var connection: TrafficConnectionPayload
    var fallbackChain: String
    var onTemporaryAction: ((TrafficConnectionPayload, String) -> Void)?
    var onPermanentRule: ((TrafficConnectionPayload, RulePayload) -> Void)?

    private var isActive: Bool { connection.state.lowercased() == "active" }

    private var canCreateRule: Bool {
        !connection.connID.isEmpty && !connection.monitorHost.isEmpty
    }

    private var proxyAction: String {
        connection.temporaryProxyAction(fallbackChain: fallbackChain)
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                detailHeader
                Divider()
                attributionGroup
                decisionGroup
                if !connection.geo.country.isEmpty || !connection.geo.city.isEmpty {
                    geoGroup
                }
                bandwidthGroup
                if !connection.hops.isEmpty {
                    hopsGroup
                }
                if !connection.timeline.isEmpty {
                    timelineGroup
                }
                Divider()
                actionsGroup
            }
            .padding(16)
        }
        .background(Color(NSColor.controlBackgroundColor))
    }

    private var detailHeader: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .top, spacing: 8) {
                VStack(alignment: .leading, spacing: 3) {
                    Text(emptyDash(connection.targetHost.isEmpty ? connection.target : connection.targetHost))
                        .font(.title3.weight(.semibold))
                        .lineLimit(2)
                    if !connection.targetPort.isEmpty && connection.targetPort != "0" {
                        Text("Port \(connection.targetPort)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                Spacer()
                ActivityDecisionBadge(actionFamily: connection.actionFamily, compact: false)
            }
            HStack(spacing: 8) {
                Label(
                    connection.network.uppercased().isEmpty ? "TCP" : connection.network.uppercased(),
                    systemImage: "network"
                )
                .font(.caption)
                .foregroundStyle(.secondary)
                if isActive {
                    Label("Active", systemImage: "circle.fill")
                        .font(.caption)
                        .foregroundStyle(.green)
                } else {
                    Label("Closed", systemImage: "circle")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private var attributionGroup: some View {
        GroupBox("Attribution") {
            VStack(alignment: .leading, spacing: 5) {
                if !connection.application.isEmpty {
                    LabeledContent("App", value: connection.application)
                }
                if !connection.source.isEmpty {
                    LabeledContent("Source", value: connection.source)
                }
                if !connection.clientAddr.isEmpty {
                    LabeledContent("Client", value: connection.clientAddr)
                }
                LabeledContent(
                    "Listener",
                    value: "\(connection.listener.protocol.uppercased()) \(connection.listener.addr)"
                )
                if !connection.profile.isEmpty {
                    LabeledContent("Profile", value: connection.profile)
                }
            }
            .font(.caption)
        }
    }

    private var decisionGroup: some View {
        GroupBox("Decision") {
            VStack(alignment: .leading, spacing: 5) {
                LabeledContent("Action") {
                    ActivityDecisionBadge(actionFamily: connection.actionFamily, compact: false)
                }
                if !connection.ruleName.isEmpty {
                    LabeledContent("Rule", value: connection.ruleName)
                }
                if !connection.ruleAction.isEmpty {
                    LabeledContent("Rule action", value: connection.ruleAction)
                }
                if !connection.chainName.isEmpty {
                    LabeledContent("Chain", value: connection.chainName)
                }
                if !connection.groupName.isEmpty {
                    LabeledContent("Group", value: connection.groupName)
                }
            }
            .font(.caption)
        }
    }

    private var geoGroup: some View {
        GroupBox("Geography") {
            VStack(alignment: .leading, spacing: 5) {
                if !connection.geo.country.isEmpty {
                    LabeledContent("Country") {
                        HStack(spacing: 4) {
                            Text(countryFlag(connection.geo.countryCode))
                            Text(connection.geo.country)
                        }
                    }
                }
                if !connection.geo.city.isEmpty {
                    LabeledContent("City", value: connection.geo.city)
                }
            }
            .font(.caption)
        }
    }

    private var bandwidthGroup: some View {
        GroupBox("Bandwidth") {
            VStack(alignment: .leading, spacing: 5) {
                LabeledContent("Downloaded", value: formatBytes(connection.rxTotal))
                LabeledContent("Uploaded", value: formatBytes(connection.txTotal))
                if isActive && (connection.rxBps > 0 || connection.txBps > 0) {
                    LabeledContent("Rate ↓ / ↑") {
                        Text("\(formatRate(connection.rxBps)) / \(formatRate(connection.txBps))")
                            .monospacedDigit()
                    }
                }
                LabeledContent("Duration", value: formatDurationNs(connection.durationNs))
            }
            .font(.caption)
        }
    }

    private var hopsGroup: some View {
        GroupBox("Proxy Hops") {
            VStack(alignment: .leading, spacing: 6) {
                ForEach(Array(connection.hops.enumerated()), id: \.offset) { idx, hop in
                    HStack(spacing: 8) {
                        Text("\(idx + 1)")
                            .font(.caption2.weight(.bold))
                            .foregroundStyle(.secondary)
                            .frame(width: 16, alignment: .center)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(hop.name.isEmpty ? hop.address : hop.name)
                                .font(.caption.weight(.medium))
                            Text(
                                [hop.`protocol`, hop.state, hop.error]
                                    .filter { !$0.isEmpty }.joined(separator: " · ")
                            )
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                        }
                        Spacer(minLength: 4)
                        if hop.elapsedNs > 0 {
                            Text(formatDurationNs(hop.elapsedNs))
                                .font(.caption2.monospacedDigit())
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }
            .font(.caption)
        }
    }

    private var timelineGroup: some View {
        GroupBox("Timeline") {
            VStack(alignment: .leading, spacing: 4) {
                ForEach(connection.timeline) { entry in
                    HStack(alignment: .top, spacing: 8) {
                        Text(entry.type)
                            .font(.caption2.weight(.semibold))
                            .foregroundStyle(.secondary)
                            .frame(width: 60, alignment: .leading)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(entry.title)
                                .font(.caption2)
                            if !entry.detail.isEmpty {
                                Text(entry.detail)
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
        }
    }

    private var actionsGroup: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Quick Actions")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
            HStack(spacing: 8) {
                Button("Allow") {
                    onTemporaryAction?(connection, "allow")
                }
                .disabled(!canCreateRule)
                Button("Block", role: .destructive) {
                    onTemporaryAction?(connection, "block")
                }
                .disabled(!canCreateRule)
                if !proxyAction.isEmpty {
                    Button("Proxy") {
                        onTemporaryAction?(connection, proxyAction)
                    }
                    .disabled(!canCreateRule)
                }
                Button("Create Rule…") {
                    if let rule = connection.ruleDraft() {
                        onPermanentRule?(connection, rule)
                    }
                }
                .disabled(connection.ruleDraft() == nil)
            }
            .buttonStyle(.borderless)
            .font(.caption)
        }
    }
}

// MARK: - HTTP Capture

struct MacHTTPCaptureSection: View {
    @ObservedObject var model: AppleAppModel
    @State private var captureSearch = ""
    @State private var selectedEntryID = ""
    @State private var localPath = ""
    @State private var remoteURL = ""
    @State private var harExport = ""
    @State private var showingHARExportWarning = false
    @State private var editingBreakpoint: DeveloperPendingBreakpointPayload?
    @State private var breakpointRequestBody = ""
    @State private var breakpointResponseBody = ""
    @State private var breakpointStatus = ""
    @State private var selectedMessageSide = "request"
    @State private var selectedMessageTab = "headers"
    @State private var composeEntry: DeveloperEntryPayload?

    var body: some View {
        VStack(spacing: 0) {
            toolbar
                .padding(12)
            if model.developerStatus.mitmEnabled {
                Label("HTTPS capture is decrypting traffic routed through the daemon HTTP proxy.", systemImage: "exclamationmark.triangle")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .padding(.horizontal, 12)
                    .padding(.bottom, 8)
            }
            Divider()
            pendingBreakpoints
            HSplitView {
                requestList
                    .frame(minWidth: 280, idealWidth: 360)
                entryDetail
                    .frame(minWidth: 420)
            }
        }
        .task {
            await model.refreshDeveloperCaptureNow()
        }
        .sheet(item: $editingBreakpoint) { breakpoint in
            breakpointEditor(breakpoint)
                .frame(minWidth: 560, minHeight: 460)
        }
        .sheet(item: $composeEntry) { entry in
            MacComposeRequestSheet(entry: entry) { request in
                model.sendComposedDeveloperRequest(request)
            }
            .frame(minWidth: 580, minHeight: 520)
        }
        .confirmationDialog(
            "Export HAR?",
            isPresented: $showingHARExportWarning,
            titleVisibility: .visible
        ) {
            Button("Load HAR Export") {
                Task {
                    harExport = (try? await model.developerHAR()) ?? ""
                }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(developerHARExportDisclosure)
        }
    }

    private var toolbar: some View {
        HStack(spacing: 10) {
            Label("\(model.developerEntries.count) requests", systemImage: "list.bullet.rectangle")
                .foregroundStyle(model.developerStatus.enabled ? Color.blue : Color.secondary)
            if model.developerStatus.mitmEnabled {
                Label("HTTPS capture on", systemImage: "lock.open")
                    .foregroundStyle(.orange)
            }
            if model.developerStatus.noCacheEnabled {
                Label("No-cache", systemImage: "arrow.clockwise.circle")
                    .foregroundStyle(.purple)
            }
            TextField("Search requests", text: $captureSearch)
                .textFieldStyle(.roundedBorder)
                .frame(maxWidth: 260)
            Spacer()
            Button {
                model.refreshDeveloperCapture()
            } label: {
                Image(systemName: "arrow.clockwise")
            }
            .help("Refresh")
            Button {
                model.clearDeveloperEntries()
            } label: {
                Image(systemName: "trash")
            }
            .help("Clear")
            Button {
                showingHARExportWarning = true
            } label: {
                Image(systemName: "square.and.arrow.down")
            }
            .help("Load HAR export")
            if !harExport.isEmpty {
                ShareLink(item: harExport, subject: Text("ClambHook HAR export")) {
                    Image(systemName: "square.and.arrow.up")
                }
                .help("Share HAR")
            }
        }
    }

    private var requestList: some View {
        List(filteredEntries, selection: $selectedEntryID) { entry in
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(entry.method.isEmpty ? "--" : entry.method)
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(entry.scheme.lowercased() == "https" ? .blue : .green)
                        .frame(width: 54, alignment: .leading)
                    Text(entry.status == 0 ? "--" : "\(entry.status)")
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(statusColor(entry.status))
                    Spacer()
                }
                Text(displayURL(entry))
                    .font(.caption)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text([entry.chainName, bodySummary(entry)].filter { !$0.isEmpty }.joined(separator: " / "))
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            .padding(.vertical, 4)
            .tag(entry.id)
        }
        .onChange(of: filteredEntries) {
            if selectedEntryID.isEmpty || !filteredEntries.contains(where: { $0.id == selectedEntryID }) {
                selectedEntryID = filteredEntries.first?.id ?? ""
            }
        }
    }

    private var entryDetail: some View {
        ScrollView {
            if let entry = selectedEntry {
                VStack(alignment: .leading, spacing: 16) {
                    HStack {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(displayURL(entry))
                                .font(.headline)
                                .textSelection(.enabled)
                            Text([entry.method, entry.scheme.uppercased(), entry.host, entry.error].filter { !$0.isEmpty }.joined(separator: " / "))
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button {
                            model.repeatDeveloperEntry(entry)
                        } label: {
                            Label("Repeat", systemImage: "arrow.triangle.2.circlepath")
                        }
                        Button {
                            composeEntry = entry
                        } label: {
                            Label("Edit & Send", systemImage: "square.and.pencil")
                        }
                    }
                    ruleControls(entry)
                    Divider()
                    Picker("Message", selection: $selectedMessageSide) {
                        Text("Request").tag("request")
                        Text("Response").tag("response")
                    }
                    .pickerStyle(.segmented)
                    Picker("Detail", selection: $selectedMessageTab) {
                        Text("Headers").tag("headers")
                        Text("Body").tag("body")
                        Text("JSON").tag("json")
                        Text("Cookies").tag("cookies")
                    }
                    .pickerStyle(.segmented)
                    messageSection(
                        title: selectedMessageSide == "request" ? "Request" : "Response",
                        message: selectedMessageSide == "request" ? entry.request : entry.response,
                        tab: selectedMessageTab
                    )
                }
                .padding(18)
            } else {
                ContentUnavailableView("No Request", systemImage: "list.bullet.rectangle")
                    .padding(40)
            }
        }
    }

    private var pendingBreakpoints: some View {
        Group {
            if !model.developerPendingBreakpoints.isEmpty {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(model.developerPendingBreakpoints) { breakpoint in
                        HStack {
                            Label("\(breakpoint.stage.capitalized) breakpoint", systemImage: "pause.circle")
                                .foregroundStyle(.orange)
                            Text(breakpoint.request.url)
                                .font(.caption)
                                .lineLimit(1)
                                .truncationMode(.middle)
                            Spacer()
                            Button("Edit") {
                                editingBreakpoint = breakpoint
                                breakpointRequestBody = breakpoint.request.body
                                breakpointResponseBody = breakpoint.response?.body ?? ""
                                breakpointStatus = breakpoint.response.map { "\($0.status)" } ?? ""
                            }
                            Button("Continue") {
                                model.resolveDeveloperBreakpoint(breakpoint, action: "continue")
                            }
                            Button("Drop", role: .destructive) {
                                model.resolveDeveloperBreakpoint(breakpoint, action: "drop")
                            }
                        }
                    }
                }
                .padding(12)
                Divider()
            }
        }
    }

    private func breakpointEditor(_ breakpoint: DeveloperPendingBreakpointPayload) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                Label("\(breakpoint.stage.capitalized) Breakpoint", systemImage: "pause.circle")
                    .font(.headline)
                Spacer()
                Button("Drop", role: .destructive) {
                    model.resolveDeveloperBreakpoint(breakpoint, action: "drop")
                    editingBreakpoint = nil
                }
            }
            Text(breakpoint.request.url)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.middle)
            Text("Request Body")
                .font(.subheadline.weight(.semibold))
            TextEditor(text: $breakpointRequestBody)
                .font(.system(.caption, design: .monospaced))
                .frame(minHeight: 110)
            if breakpoint.response != nil {
                HStack {
                    Text("Response Status")
                        .font(.subheadline.weight(.semibold))
                    TextField("Status", text: $breakpointStatus)
                        .textFieldStyle(.roundedBorder)
                        .frame(width: 90)
                }
                Text("Response Body")
                    .font(.subheadline.weight(.semibold))
                TextEditor(text: $breakpointResponseBody)
                    .font(.system(.caption, design: .monospaced))
                    .frame(minHeight: 110)
            }
            Spacer()
            HStack {
                Spacer()
                Button("Cancel") {
                    editingBreakpoint = nil
                }
                Button("Continue Edited") {
                    var request = breakpoint.request
                    request.body = breakpointRequestBody
                    request.bodySet = true
                    var response = breakpoint.response
                    if var editedResponse = response {
                        editedResponse.body = breakpointResponseBody
                        editedResponse.bodySet = true
                        editedResponse.status = Int(breakpointStatus.trimmingCharacters(in: .whitespacesAndNewlines)) ?? editedResponse.status
                        response = editedResponse
                    }
                    model.resolveDeveloperBreakpoint(
                        breakpoint,
                        resolution: DeveloperBreakpointResolutionPayload(action: "continue", request: request, response: response)
                    )
                    editingBreakpoint = nil
                }
                .buttonStyle(.borderedProminent)
            }
        }
        .padding(18)
    }

    private func ruleControls(_ entry: DeveloperEntryPayload) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Tools")
                .font(.subheadline.weight(.semibold))
            HStack {
                TextField("Local file or directory", text: $localPath)
                    .textFieldStyle(.roundedBorder)
                Button("Map Local") {
                    model.addDeveloperMapRule(DeveloperMapRulePayload(
                        name: "Local \(entry.host)",
                        match: matchPayload(entry),
                        kind: "local",
                        localPath: localPath
                    ))
                }
                .disabled(localPath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
            HStack {
                TextField("Remote base URL", text: $remoteURL)
                    .textFieldStyle(.roundedBorder)
                Button("Map Remote") {
                    model.addDeveloperMapRule(DeveloperMapRulePayload(
                        name: "Remote \(entry.host)",
                        match: matchPayload(entry),
                        kind: "remote",
                        remoteURL: remoteURL
                    ))
                }
                .disabled(remoteURL.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
            HStack {
                Button {
                    model.addDeveloperBreakpointRule(DeveloperBreakpointRulePayload(
                        name: "Breakpoint \(entry.host)",
                        match: matchPayload(entry),
                        stage: "both"
                    ))
                } label: {
                    Label("Breakpoint", systemImage: "pause.circle")
                }
                Text("\(model.developerMapRules.count) map rules / \(model.developerBreakpointRules.count) breakpoint rules")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            DisclosureGroup("Rules") {
                if model.developerMapRules.isEmpty && model.developerBreakpointRules.isEmpty {
                    Text("No developer rules")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(model.developerMapRules) { rule in
                        developerRuleRow(
                            title: rule.name.isEmpty ? "Map \(rule.kind)" : rule.name,
                            subtitle: rule.kind == "local" ? rule.localPath : rule.remoteURL,
                            enabled: rule.enabled,
                            onToggle: { enabled in
                                var rules = model.developerMapRules
                                if let index = rules.firstIndex(where: { $0.id == rule.id }) {
                                    rules[index].enabled = enabled
                                    model.replaceDeveloperMapRules(rules)
                                }
                            },
                            onDelete: {
                                model.replaceDeveloperMapRules(model.developerMapRules.filter { $0.id != rule.id })
                            }
                        )
                    }
                    ForEach(model.developerBreakpointRules) { rule in
                        developerRuleRow(
                            title: rule.name.isEmpty ? "Breakpoint" : rule.name,
                            subtitle: "\(rule.stage) \(rule.match.host)",
                            enabled: rule.enabled,
                            onToggle: { enabled in
                                var rules = model.developerBreakpointRules
                                if let index = rules.firstIndex(where: { $0.id == rule.id }) {
                                    rules[index].enabled = enabled
                                    model.replaceDeveloperBreakpointRules(rules)
                                }
                            },
                            onDelete: {
                                model.replaceDeveloperBreakpointRules(model.developerBreakpointRules.filter { $0.id != rule.id })
                            }
                        )
                    }
                }
            }
        }
    }

    private func developerRuleRow(title: String, subtitle: String, enabled: Bool, onToggle: @escaping (Bool) -> Void, onDelete: @escaping () -> Void) -> some View {
        HStack {
            Toggle("", isOn: Binding(get: { enabled }, set: onToggle))
                .labelsHidden()
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.caption.weight(.semibold))
                if !subtitle.isEmpty {
                    Text(subtitle)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
            }
            Spacer()
            Button(role: .destructive, action: onDelete) {
                Image(systemName: "trash")
            }
            .buttonStyle(.borderless)
        }
    }

    private func messageSection(title: String, message: DeveloperMessagePayload, tab: String) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text(title)
                    .font(.subheadline.weight(.semibold))
                Spacer()
                Text("\(formatBytes(message.body.size))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            switch tab {
            case "body":
                bodyTab(message.body)
            case "json":
                jsonTab(message.body)
            case "cookies":
                cookiesTab(message.cookies)
            default:
                headersTab(message.headers)
            }
        }
    }

    @ViewBuilder
    private func headersTab(_ headers: [DeveloperHeaderPayload]) -> some View {
        if headers.isEmpty {
            Text("No headers")
                .font(.caption)
                .foregroundStyle(.secondary)
        } else {
            ForEach(headers) { header in
                HStack(alignment: .top) {
                    Text(header.name)
                        .font(.system(.caption, design: .monospaced).weight(.semibold))
                        .frame(width: 160, alignment: .leading)
                    Text(header.value)
                        .font(.system(.caption, design: .monospaced))
                        .foregroundStyle(header.redacted ? .red : .secondary)
                        .textSelection(.enabled)
                }
            }
        }
    }

    @ViewBuilder
    private func bodyTab(_ body: DeveloperBodyPayload) -> some View {
        let preview = bodyPreviewText(body)
        if preview.isEmpty {
            Text("No body preview")
                .font(.caption)
                .foregroundStyle(.secondary)
        } else {
            Text(preview)
                .font(.system(.caption, design: .monospaced))
                .textSelection(.enabled)
                .padding(8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.secondary.opacity(0.06), in: RoundedRectangle(cornerRadius: 6))
            Text([body.mimeType, body.encoding].filter { !$0.isEmpty }.joined(separator: " / "))
                .font(.caption)
                .foregroundStyle(.secondary)
            if body.truncated {
                Text("Truncated after \(formatBytes(body.truncatedAfter))")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
        }
    }

    @ViewBuilder
    private func jsonTab(_ body: DeveloperBodyPayload) -> some View {
        if let pretty = prettyJSON(body.preview) {
            Text(pretty)
                .font(.system(.caption, design: .monospaced))
                .textSelection(.enabled)
                .padding(8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.secondary.opacity(0.06), in: RoundedRectangle(cornerRadius: 6))
            if body.truncated {
                Text("JSON preview is truncated")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
        } else {
            Text("No valid JSON preview")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    @ViewBuilder
    private func cookiesTab(_ cookies: [DeveloperCookiePayload]) -> some View {
        if cookies.isEmpty {
            Text("No cookies")
                .font(.caption)
                .foregroundStyle(.secondary)
        } else {
            ForEach(cookies) { cookie in
                HStack(alignment: .top) {
                    Text(cookie.name)
                        .font(.system(.caption, design: .monospaced).weight(.semibold))
                        .frame(width: 160, alignment: .leading)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(cookie.value)
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(cookie.redacted ? .red : .secondary)
                            .textSelection(.enabled)
                        let attrs = cookieAttributes(cookie)
                        if !attrs.isEmpty {
                            Text(attrs)
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }
        }
    }

    private var filteredEntries: [DeveloperEntryPayload] {
        let query = captureSearch.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !query.isEmpty else {
            return model.developerEntries
        }
        return model.developerEntries.filter { entry in
            [entry.method, entry.url, entry.host, entry.chainName, "\(entry.status)"]
                .joined(separator: " ")
                .lowercased()
                .contains(query)
        }
    }

    private var selectedEntry: DeveloperEntryPayload? {
        if let selected = model.developerEntries.first(where: { $0.id == selectedEntryID }) {
            return selected
        }
        return filteredEntries.first
    }

    private func displayURL(_ entry: DeveloperEntryPayload) -> String {
        entry.url.isEmpty ? entry.host : entry.url
    }

    private func bodySummary(_ entry: DeveloperEntryPayload) -> String {
        let req = entry.request.body.previewBytes
        let resp = entry.response.body.previewBytes
        if req == 0 && resp == 0 {
            return ""
        }
        return "\(formatBytes(req)) req / \(formatBytes(resp)) resp"
    }

    private func bodyPreviewText(_ body: DeveloperBodyPayload) -> String {
        if !body.preview.isEmpty {
            return body.preview
        }
        if !body.previewBase64.isEmpty {
            return "[base64] \(body.previewBase64)"
        }
        return ""
    }

    private func prettyJSON(_ text: String) -> String? {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, let data = trimmed.data(using: .utf8) else {
            return nil
        }
        guard let object = try? JSONSerialization.jsonObject(with: data),
              JSONSerialization.isValidJSONObject(object),
              let pretty = try? JSONSerialization.data(withJSONObject: object, options: [.prettyPrinted, .sortedKeys])
        else {
            return nil
        }
        return String(data: pretty, encoding: .utf8)
    }

    private func cookieAttributes(_ cookie: DeveloperCookiePayload) -> String {
        var parts: [String] = []
        if !cookie.domain.isEmpty {
            parts.append("domain=\(cookie.domain)")
        }
        if !cookie.path.isEmpty {
            parts.append("path=\(cookie.path)")
        }
        if !cookie.expires.isEmpty {
            parts.append("expires=\(cookie.expires)")
        }
        if cookie.maxAge != 0 {
            parts.append("max-age=\(cookie.maxAge)")
        }
        if cookie.secure {
            parts.append("secure")
        }
        if cookie.httpOnly {
            parts.append("httponly")
        }
        if !cookie.sameSite.isEmpty {
            parts.append("samesite=\(cookie.sameSite)")
        }
        return parts.joined(separator: "  ")
    }

    private func statusColor(_ status: Int) -> Color {
        switch status {
        case 200..<300: return .green
        case 300..<400: return .blue
        case 400..<600: return .red
        default: return .secondary
        }
    }

    private func matchPayload(_ entry: DeveloperEntryPayload) -> DeveloperMatchPayload {
        let path = URL(string: entry.url)?.path ?? "/"
        return DeveloperMatchPayload(
            methods: entry.method.isEmpty ? [] : [entry.method],
            host: entry.host,
            pathPrefix: path.isEmpty ? "/" : path
        )
    }
}

// MARK: - Logs

struct MacLogsSection: View {
    @ObservedObject var model: AppleAppModel
    @State private var logSearch = ""

    private var filteredLogs: [(offset: Int, element: String)] {
        let query = logSearch.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let all = Array(model.dashboard.logs.enumerated())
        guard !query.isEmpty else { return all }
        return all.filter { $0.element.lowercased().contains(query) }
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                TextField("Filter logs…", text: $logSearch)
                    .textFieldStyle(.roundedBorder)
                    .font(.caption)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            Divider()
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 2) {
                        if filteredLogs.isEmpty {
                            Text(logSearch.isEmpty ? "No logs yet" : "No matches")
                                .foregroundStyle(.secondary)
                                .padding(20)
                        } else {
                            ForEach(filteredLogs, id: \.offset) { item in
                                Text(item.element)
                                    .font(.system(.caption, design: .monospaced))
                                    .foregroundStyle(logLineColor(item.element))
                                    .textSelection(.enabled)
                                    .id(item.offset)
                            }
                        }
                    }
                    .padding(12)
                }
                .onChange(of: model.dashboard.logs.count) {
                    if let last = filteredLogs.last {
                        proxy.scrollTo(last.offset, anchor: .bottom)
                    }
                }
            }
        }
    }

    private func logLineColor(_ line: String) -> Color {
        let lower = line.lowercased()
        if lower.contains("error") || lower.contains("err]") || lower.contains("[err") {
            return .red
        }
        if lower.contains("warn") {
            return .orange
        }
        return .secondary
    }
}

// MARK: - Settings

struct MacSettingsSection: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        AppSettingsView(model: model)
    }
}

// MARK: - License

struct MacLicenseSectionInline: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                ProductStatePanel(decision: model.licenseManager.decision)
                Divider()
                MacLicenseControls(manager: model.licenseManager)
            }
            .padding(20)
        }
    }
}

private struct MacLicenseControls: View {
    @ObservedObject var manager: MacLicenseManager
    @State private var licenseKey = ""
    @State private var email = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Label(deviceSummary, systemImage: "desktopcomputer")
                Spacer()
                Text("\(manager.deviceState.activeDeviceCount)/\(manager.deviceState.maxActiveDevices) active")
                    .foregroundStyle(.secondary)
            }

            SecureField("License key", text: $licenseKey)
                .textFieldStyle(.roundedBorder)
            TextField("Email", text: $email)
                .textFieldStyle(.roundedBorder)

            HStack(spacing: 10) {
                Button {
                    Task { await manager.activate(licenseKey: licenseKey, email: email) }
                } label: {
                    Label("Activate", systemImage: "checkmark.seal")
                }
                .disabled(manager.isLoading || licenseKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)

                Button(role: .destructive) {
                    Task { await manager.deactivateCurrentDevice() }
                } label: {
                    Label("Deactivate", systemImage: "minus.circle")
                }
                .disabled(manager.isLoading || !manager.deviceState.isCurrentDeviceActive)
            }

            HStack(spacing: 10) {
                Button {
                    Task { await manager.reactivateCurrentDevice() }
                } label: {
                    Label("Reactivate", systemImage: "arrow.clockwise.circle")
                }
                .disabled(manager.isLoading || !manager.deviceState.canReactivateCurrentDevice)

                Button {
                    Task { await manager.transferCurrentDevice() }
                } label: {
                    Label("Transfer", systemImage: "arrow.right.arrow.left")
                }
                .disabled(manager.isLoading || !manager.deviceState.canTransferCurrentDevice)
            }

            Link(destination: URL(string: "https://jpfchang.org/clambhook/buy")!) {
                Label("Buy macOS license - USD \(MobileLicenseCommercialTerms.licensePriceUSD)", systemImage: "cart")
            }

            Link(destination: defaultLicensePortalURL) {
                Label("License Portal", systemImage: "safari")
            }

            if manager.isLoading {
                ProgressView()
            }

            if !manager.statusMessage.isEmpty {
                Text(manager.statusMessage)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
        .onAppear {
            licenseKey = manager.savedLicenseKey()
            email = manager.savedEmail()
        }
    }

    private var deviceSummary: String {
        if let device = manager.deviceState.currentDevice {
            return device.status == .active ? "\(device.displayName) is active" : "\(device.displayName) is deactivated"
        }
        return "This Mac is not activated"
    }
}

// MARK: - Helpers

@MainActor
private func dashboardFallbackProxyChain(_ dashboard: DashboardStore) -> String {
    for group in dashboard.policyGroups.groups {
        if !group.selectedChain.isEmpty { return group.selectedChain }
        if !group.selected.isEmpty { return group.selected }
    }
    return dashboard.servers.chains.first?.name ?? ""
}

private func timeAgoShort(_ startTsNs: Int64) -> String {
    guard startTsNs > 0 else { return "--" }
    let nowNs = Int64(Date().timeIntervalSince1970 * 1_000_000_000)
    let elapsed = max(0, nowNs - startTsNs)
    let secs = elapsed / 1_000_000_000
    if secs < 60 { return "\(secs)s ago" }
    let mins = secs / 60
    if mins < 60 { return "\(mins)m ago" }
    return "\(mins / 60)h ago"
}

// MARK: - Compose request sheet

private struct ComposeHeaderRow: Identifiable {
    let id = UUID()
    var name: String
    var value: String
}

private struct MacComposeRequestSheet: View {
    @Environment(\.dismiss) private var dismiss
    let entry: DeveloperEntryPayload
    let onSend: (DeveloperRepeatRequestPayload) -> Void

    @State private var method: String
    @State private var url: String
    @State private var headers: [ComposeHeaderRow]
    @State private var bodyText: String

    init(entry: DeveloperEntryPayload, onSend: @escaping (DeveloperRepeatRequestPayload) -> Void) {
        self.entry = entry
        self.onSend = onSend
        _method = State(initialValue: entry.method.isEmpty ? "GET" : entry.method)
        _url = State(initialValue: entry.url)
        _headers = State(initialValue: entry.request.headers
            .filter { !$0.redacted && !$0.truncated }
            .map { ComposeHeaderRow(name: $0.name, value: $0.value) })
        _bodyText = State(initialValue: entry.request.body.preview)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Edit & Send Request")
                    .font(.headline)
                Spacer()
                Button("Cancel") { dismiss() }
                Button {
                    onSend(makeRequest())
                    dismiss()
                } label: {
                    Label("Send", systemImage: "paperplane")
                }
                .keyboardShortcut(.return, modifiers: .command)
                .disabled(url.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            .padding(16)
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    HStack {
                        TextField("Method", text: $method)
                            .frame(width: 90)
                        TextField("URL", text: $url)
                    }
                    .textFieldStyle(.roundedBorder)
                    HStack {
                        Text("Headers")
                            .font(.subheadline.weight(.semibold))
                        Spacer()
                        Button {
                            headers.append(ComposeHeaderRow(name: "", value: ""))
                        } label: {
                            Label("Add", systemImage: "plus")
                        }
                    }
                    ForEach($headers) { $header in
                        HStack {
                            TextField("Name", text: $header.name)
                                .frame(width: 180)
                            TextField("Value", text: $header.value)
                            Button(role: .destructive) {
                                headers.removeAll { $0.id == header.id }
                            } label: {
                                Image(systemName: "minus.circle")
                            }
                            .buttonStyle(.borderless)
                        }
                        .textFieldStyle(.roundedBorder)
                    }
                    if entry.request.body.truncated {
                        Label("Captured body was truncated; provide the full body to send.", systemImage: "exclamationmark.triangle")
                            .font(.caption)
                            .foregroundStyle(.orange)
                    }
                    Text("Body")
                        .font(.subheadline.weight(.semibold))
                    TextEditor(text: $bodyText)
                        .font(.system(.caption, design: .monospaced))
                        .frame(minHeight: 140)
                        .overlay(RoundedRectangle(cornerRadius: 6).stroke(.quaternary))
                }
                .padding(16)
            }
        }
    }

    private func makeRequest() -> DeveloperRepeatRequestPayload {
        DeveloperRepeatRequestPayload(
            entryID: entry.id,
            method: method.trimmingCharacters(in: .whitespaces),
            url: url.trimmingCharacters(in: .whitespaces),
            headers: headers
                .filter { !$0.name.trimmingCharacters(in: .whitespaces).isEmpty }
                .map { DeveloperHeaderPayload(name: $0.name, value: $0.value) },
            body: bodyText
        )
    }
}
