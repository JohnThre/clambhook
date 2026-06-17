import AppKit
import ClambhookShared
import SwiftUI

// MARK: - Dashboard

struct MacDashboardSection: View {
    @ObservedObject var model: AppleAppModel
    @ObservedObject private var daemon: DaemonSupervisor

    init(model: AppleAppModel) {
        self.model = model
        self._daemon = ObservedObject(wrappedValue: model.daemonSupervisor)
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                connectionControl
                Divider()
                metricsGrid
                if !model.dashboard.policyGroups.groups.isEmpty {
                    Divider()
                    policyGroupHealth
                }
                Divider()
                recentRequests
            }
            .padding(20)
        }
    }

    // MARK: Connection control

    private var connectionControl: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 12) {
                profilePicker
                Spacer()
                apiPill
            }
            HStack(spacing: 10) {
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
                .disabled(!model.dashboard.apiOnline && !model.dashboard.status.running)

                statusLabel
            }
        }
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

    private var statusLabel: some View {
        HStack(spacing: 6) {
            Circle()
                .fill(statusDotColor)
                .frame(width: 8, height: 8)
            Text(statusText)
                .font(.subheadline)
                .foregroundStyle(.secondary)
            if daemon.state.isBusy {
                ProgressView()
                    .controlSize(.small)
                    .scaleEffect(0.75)
            }
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
        if model.dashboard.status.running {
            return "Connected"
        }
        switch daemon.state {
        case .running: return "Daemon running"
        case .starting: return "Starting…"
        case .stopping: return "Stopping…"
        case .failed: return "Daemon failed"
        case .stopped: return "Disconnected"
        }
    }

    // MARK: Metrics grid

    private var metricsGrid: some View {
        let sample = model.dashboard.currentBandwidth
        let activeConnections = model.dashboard.status.listeners.reduce(0) { $0 + $1.activeConns }
        let latency = bestLatency
        return LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible()), GridItem(.flexible())], spacing: 10) {
            MacMetricCard(title: "Download", value: formatRate(sample.rxBps), systemImage: "arrow.down", tint: .blue)
            MacMetricCard(title: "Upload", value: formatRate(sample.txBps), systemImage: "arrow.up", tint: .green)
            MacMetricCard(title: "Latency", value: latency, systemImage: "timer", tint: latency == "--" ? .secondary : .orange)
            MacMetricCard(title: "Active", value: "\(activeConnections)", systemImage: "point.3.connected.trianglepath.dotted", tint: .purple)
            MacMetricCard(title: "Total ↓", value: formatBytes(model.dashboard.traffic.summary.rxTotal), systemImage: "internaldrive", tint: .blue)
            MacMetricCard(title: "Total ↑", value: formatBytes(model.dashboard.traffic.summary.txTotal), systemImage: "internaldrive", tint: .green)
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

    // MARK: Recent requests

    private var recentRequests: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("Recent Requests")
                    .font(.headline)
                Spacer()
                let counts = model.dashboard.monitorActionCounts
                HStack(spacing: 8) {
                    MacActionBadge(label: "P \(counts["proxy", default: 0])", color: .green)
                    MacActionBadge(label: "D \(counts["direct", default: 0])", color: .blue)
                    MacActionBadge(label: "B \(counts["block", default: 0])", color: .red)
                }
            }
            if model.dashboard.recentDecisions.isEmpty {
                Text("No recent traffic")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(model.dashboard.recentDecisions) { decision in
                    HStack(spacing: 8) {
                        Circle()
                            .fill(actionColor(decision.action))
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

    private func actionColor(_ action: String) -> Color {
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

private struct MacMetricCard: View {
    var title: String
    var value: String
    var systemImage: String
    var tint: Color

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: systemImage)
                .font(.subheadline)
                .foregroundStyle(tint)
                .frame(width: 22)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                Text(value)
                    .font(.subheadline.weight(.semibold))
                    .monospacedDigit()
                    .lineLimit(1)
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .background(Color.secondary.opacity(0.06), in: RoundedRectangle(cornerRadius: 10))
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
                Text("No profiles")
                    .foregroundStyle(.secondary)
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
                    MacSubscriptionRow(subscription: sub)
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

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 6) {
                Text(subscription.name.isEmpty ? "(unnamed)" : subscription.name)
                    .font(.caption.weight(.semibold))
                    .lineLimit(1)
                Spacer()
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

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                CompactPolicySelectorView(
                    summary: model.dashboard.policySelectorSummary,
                    groups: model.dashboard.policyGroups.groups,
                    onSelect: { group, chain in
                        model.selectPolicyGroup(group: group, chain: chain)
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
    @State private var routeTestNetwork = "tcp"
    @State private var routeTestTarget = "example.com:443"
    @State private var routeTestResult: RuleTestResponse?
    @State private var routeTestError = ""

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                RuleListView(rules: model.dashboard.rules)
                if !model.dashboard.rules.effectiveRules.isEmpty && model.dashboard.rules.effectiveRules.count != model.dashboard.rules.rules.count {
                    Divider()
                    VStack(alignment: .leading, spacing: 10) {
                        Text("Effective Rules")
                            .font(.headline)
                        ForEach(model.dashboard.rules.effectiveRules) { rule in
                            VStack(alignment: .leading, spacing: 4) {
                                HStack {
                                    Text(rule.name).fontWeight(.medium)
                                    Spacer()
                                    Text(rule.action).foregroundStyle(.secondary)
                                }
                                Text(ruleSummary(rule))
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    }
                }
                if !model.dashboard.rules.ruleSets.isEmpty {
                    Divider()
                    VStack(alignment: .leading, spacing: 10) {
                        Text("Rule Sets")
                            .font(.headline)
                        ForEach(model.dashboard.rules.ruleSets) { rs in
                            HStack {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(rs.name).fontWeight(.medium)
                                    Text(rs.url).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                                }
                                Spacer()
                                VStack(alignment: .trailing, spacing: 2) {
                                    Text(rs.cached ? "Cached" : "Not cached")
                                        .font(.caption)
                                        .foregroundStyle(rs.cached ? .green : .secondary)
                                    if rs.domainCount + rs.cidrCount > 0 {
                                        Text("\(rs.domainCount) domains / \(rs.cidrCount) CIDRs")
                                            .font(.caption2).foregroundStyle(.secondary)
                                    }
                                }
                            }
                        }
                        Button {
                            model.refreshActiveProfileRuleSets()
                        } label: {
                            Label("Refresh Rule Sets", systemImage: "arrow.clockwise")
                        }
                    }
                }
                Divider()
                routeTester
            }
            .padding(20)
        }
    }

    private var routeTester: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Route Tester")
                .font(.headline)
            HStack(spacing: 8) {
                Picker("Network", selection: $routeTestNetwork) {
                    Text("TCP").tag("tcp")
                    Text("UDP").tag("udp")
                }
                .labelsHidden()
                .pickerStyle(.segmented)
                .frame(width: 120)
                TextField("host:port", text: $routeTestTarget)
                    .textFieldStyle(.roundedBorder)
                Button {
                    routeTestError = ""
                    Task {
                        do {
                            routeTestResult = try await model.testRule(network: routeTestNetwork, target: routeTestTarget)
                        } catch {
                            routeTestResult = nil
                            routeTestError = error.localizedDescription
                        }
                    }
                } label: {
                    Label("Test", systemImage: "checkmark.circle")
                }
            }
            if !routeTestError.isEmpty {
                Text(routeTestError)
                    .font(.caption)
                    .foregroundStyle(.red)
            } else if let result = routeTestResult {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Action: \(result.decision.action)")
                        .font(.caption.weight(.semibold))
                    Text("Rule: \(result.decision.ruleName.isEmpty ? "Default" : result.decision.ruleName)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    if !result.decision.chainName.isEmpty {
                        Text("Chain: \(result.decision.chainName)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
    }

    private func ruleSummary(_ rule: RulePayload) -> String {
        var parts: [String] = []
        if !rule.domains.isEmpty { parts.append(rule.domains.joined(separator: ", ")) }
        if !rule.domainSuffixes.isEmpty { parts.append(rule.domainSuffixes.map { "*.\($0)" }.joined(separator: ", ")) }
        if !rule.cidrs.isEmpty { parts.append(rule.cidrs.joined(separator: ", ")) }
        if !rule.ports.isEmpty { parts.append(rule.ports.map(String.init).joined(separator: ", ")) }
        if !rule.networks.isEmpty { parts.append(rule.networks.joined(separator: ", ")) }
        return parts.isEmpty ? "all traffic" : parts.joined(separator: " · ")
    }
}

// MARK: - DNS

struct MacDNSSection: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                dnsOverview
                if !model.dashboard.dns.upstreams.isEmpty {
                    Divider()
                    upstreamsTable
                }
                if !model.dashboard.dns.upstreamRoutes.isEmpty {
                    Divider()
                    routesTable
                }
            }
            .padding(20)
        }
    }

    private var dnsOverview: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("DNS Configuration")
                .font(.headline)
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

// MARK: - Activity

struct MacActivitySection: View {
    @ObservedObject var model: AppleAppModel
    @State private var trafficFilter = "all"
    @State private var trafficSearch = ""
    @State private var draftRule: RulePayload?
    @State private var sourceConnection: TrafficConnectionPayload?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                TrafficSummaryView(traffic: model.dashboard.traffic)
                Divider()
                filterBar
                trafficList
                if !model.dashboard.traffic.blockDecisions.isEmpty {
                    Divider()
                    blockDecisionsList
                }
                if !model.dashboard.traffic.cleanupSuggestions.isEmpty {
                    Divider()
                    cleanupList
                }
            }
            .padding(20)
        }
        .sheet(item: $draftRule) { rule in
            MacRuleCreateSheet(model: model, initialRule: rule, sourceConnection: sourceConnection)
        }
    }

    private var filterBar: some View {
        HStack(spacing: 10) {
            Picker("Filter", selection: $trafficFilter) {
                Text("All").tag("all")
                Text("Proxy").tag("proxy")
                Text("Direct").tag("direct")
                Text("Block").tag("block")
            }
            .labelsHidden()
            .pickerStyle(.segmented)
            TextField("Search hosts, rules, chains", text: $trafficSearch)
                .textFieldStyle(.roundedBorder)
        }
    }

    private var filteredTraffic: [TrafficConnectionPayload] {
        let query = trafficSearch.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return model.dashboard.traffic.connections.filter { connection in
            (trafficFilter == "all" || connection.actionFamily == trafficFilter)
            && (query.isEmpty || [
                connection.target, connection.monitorHost, connection.ruleName,
                connection.ruleAction, connection.chainName, connection.application, connection.network,
            ].contains { $0.lowercased().contains(query) })
        }
    }

    private var trafficList: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Connections")
                .font(.headline)
            TrafficListView(
                connections: filteredTraffic,
                fallbackChain: dashboardFallbackProxyChain(model.dashboard),
                onTemporaryAction: { connection, action in
                    model.createTemporaryRuleFromConnection(connection, action: action)
                },
                onPermanentRule: { connection, rule in
                    model.createRuleFromConnection(connection, rule: rule)
                }
            )
        }
    }

    private var blockDecisionsList: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Blocked")
                .font(.headline)
            ForEach(model.dashboard.traffic.blockDecisions) { decision in
                VStack(alignment: .leading, spacing: 3) {
                    Text(emptyDash(decision.targetHost.isEmpty ? decision.target : decision.targetHost))
                        .fontWeight(.medium)
                    Text([decision.profile, decision.ruleName, decision.action].filter { !$0.isEmpty }.joined(separator: " · "))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private var cleanupList: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Rule Cleanup")
                .font(.headline)
            ForEach(model.dashboard.traffic.cleanupSuggestions) { suggestion in
                HStack(alignment: .top, spacing: 12) {
                    VStack(alignment: .leading, spacing: 3) {
                        Text(suggestion.targetRuleName.isEmpty ? suggestion.ruleName : suggestion.targetRuleName)
                            .fontWeight(.medium)
                        Text(suggestion.message)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    Spacer(minLength: 8)
                    Button(suggestion.operation == "move_rule_to_end" ? "Move to End" : "Delete") {
                        model.applyCleanupSuggestion(suggestion)
                    }
                    .disabled(suggestion.operation.isEmpty)
                }
            }
        }
    }
}

// MARK: - HTTP Capture

struct MacHTTPCaptureSection: View {
    @ObservedObject var model: AppleAppModel
    @State private var captureFilter: CaptureFilterKind = .all
    @State private var captureSearch = ""

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                captureStatus
                Divider()
                HStack(spacing: 10) {
                    Picker("Filter", selection: $captureFilter) {
                        Text("All").tag(CaptureFilterKind.all)
                        Text("HTTP").tag(CaptureFilterKind.http)
                        Text("HTTPS").tag(CaptureFilterKind.https)
                        Text("Pinned").tag(CaptureFilterKind.pinned)
                    }
                    .labelsHidden()
                    .pickerStyle(.segmented)
                    TextField("Search method, host, path, rule", text: $captureSearch)
                        .textFieldStyle(.roundedBorder)
                }
                captureGroups
                Text("HTTPS rows remain CONNECT metadata unless opt-in HTTPS Body Capture is enabled in developer config.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .padding(20)
        }
    }

    private var captureEntries: [CaptureEntryPayload] {
        CaptureSupport.captureEntries(from: model.dashboard.traffic)
    }

    private var filteredCaptureEntries: [CaptureEntryPayload] {
        CaptureSupport.filteredEntries(
            captureEntries,
            filter: captureFilter,
            query: captureSearch,
            pinnedIDs: model.pinnedConnectionIDs
        )
    }

    private var captureStatus: some View {
        HStack(spacing: 12) {
            Label("\(captureEntries.count) metadata requests", systemImage: "list.bullet.rectangle")
                .foregroundStyle(captureEntries.isEmpty ? Color.secondary : Color.blue)
            Label("\(CaptureSupport.groupEntriesByHost(filteredCaptureEntries, pinnedIDs: model.pinnedConnectionIDs).count) hosts", systemImage: "rectangle.stack")
                .foregroundStyle(.secondary)
            Spacer()
            ShareLink(
                item: CaptureSupport.exportString(traffic: model.dashboard.traffic, entries: filteredCaptureEntries),
                subject: Text("ClambHook HTTP metadata export"),
                message: Text("Local metadata-only JSON export.")
            ) {
                Image(systemName: "square.and.arrow.up")
            }
            .disabled(filteredCaptureEntries.isEmpty)
        }
        .font(.subheadline)
    }

    private var captureGroups: some View {
        let groups = CaptureSupport.groupEntriesByHost(filteredCaptureEntries, pinnedIDs: model.pinnedConnectionIDs)
        return VStack(alignment: .leading, spacing: 10) {
            if groups.isEmpty {
                Text("No HTTP metadata")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(groups) { group in
                    MacCaptureGroupCard(group: group, pinnedIDs: model.pinnedConnectionIDs, onTogglePin: toggleCapturePin)
                }
            }
        }
    }

    private func toggleCapturePin(_ entry: CaptureMetadataEntryPayload) {
        var ids = model.pinnedConnectionIDs
        if ids.contains(entry.pinID) {
            ids.remove(entry.pinID)
        } else {
            ids.insert(entry.pinID)
        }
        model.settingsStore.settings.pinnedConnectionIDs = ids.sorted()
    }
}

private struct MacCaptureGroupCard: View {
    var group: CaptureGroupPayload
    var pinnedIDs: Set<String>
    var onTogglePin: (CaptureMetadataEntryPayload) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(emptyDash(group.host))
                    .font(.headline)
                Spacer()
                let schemes = group.schemes.map { $0.uppercased() }.joined(separator: ", ")
                Text(schemes.isEmpty ? "\(group.count)" : "\(group.count) / \(schemes)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            ForEach(group.entries) { entry in
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text(entry.method.isEmpty ? "--" : entry.method)
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(entry.scheme.lowercased() == "https" ? .blue : .green)
                        .frame(minWidth: 46, alignment: .leading)
                    Text(emptyDash(entry.displayTarget))
                        .font(.caption)
                        .lineLimit(1)
                    Spacer()
                    Button(action: { onTogglePin(entry) }) {
                        Image(systemName: pinnedIDs.contains(entry.pinID) ? "pin.slash.fill" : "pin.fill")
                            .font(.caption)
                    }
                    .buttonStyle(.plain)
                }
                Text([entry.ruleName, entry.chainName, entry.ruleAction].filter { !$0.isEmpty }.joined(separator: " / "))
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(12)
        .background(Color.secondary.opacity(0.05), in: RoundedRectangle(cornerRadius: 8))
    }
}

// MARK: - Logs

struct MacLogsSection: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 2) {
                    if model.dashboard.logs.isEmpty {
                        Text("No logs yet")
                            .foregroundStyle(.secondary)
                            .padding(20)
                    } else {
                        ForEach(Array(model.dashboard.logs.enumerated()), id: \.offset) { index, line in
                            Text(line)
                                .font(.system(.caption, design: .monospaced))
                                .foregroundStyle(.secondary)
                                .textSelection(.enabled)
                                .id(index)
                        }
                    }
                }
                .padding(12)
            }
            .onChange(of: model.dashboard.logs.count) {
                if !model.dashboard.logs.isEmpty {
                    proxy.scrollTo(model.dashboard.logs.count - 1, anchor: .bottom)
                }
            }
        }
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
                Label("Buy ClambHook USD \(MobileLicenseCommercialTerms.lifetimePriceUSD)", systemImage: "cart")
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
