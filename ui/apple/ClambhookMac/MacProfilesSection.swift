// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

import AppKit
import ClambhookShared
import SwiftUI

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
    @State private var pendingImportReview: TunnelImportReviewPayload?
    @State private var outlineKey = ""
    @State private var outlineProfileName = "Outline"
    @State private var outlineReview: OutlineReviewPayload?
    @State private var reviewedOutlineKey = ""
    @State private var outlineError = ""
    @State private var outlineBusy = false
    @State private var converterSource = ""
    @State private var converterFormat = "auto"
    @State private var converterProfileName = "Imported Profile"
    @State private var converterReview: ProfileConversionReviewPayload?
    @State private var converterError = ""
    @State private var converterBusy = false
    @State private var converterActivate = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                profileHeader
                Divider()
                outlineSection
                Divider()
                converterSection
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
            "Merge Reviewed Profiles?",
            isPresented: $showImportConfirm,
            titleVisibility: .visible
        ) {
            Button("Merge Profiles") {
                if let review = pendingImportReview {
                    let request = ReviewedTunnelImportRequest(
                        importText: pendingImportText,
                        profiles: review.profiles.map {
                            ReviewedTunnelImportProfile(
                                sourceName: $0.name, targetName: $0.name)
                        })
                    Task {
                        do {
                            try await model.applyReviewedConfigImport(request)
                            refreshPathValidation()
                        } catch { importError = error.localizedDescription }
                    }
                }
                pendingImportText = ""
                pendingImportReview = nil
            }
            Button("Cancel", role: .cancel) {
                pendingImportText = ""
                pendingImportReview = nil
            }
        } message: {
            Text("This preserves unrelated profiles and root settings. Profiles found: \(importPreviewProfiles.joined(separator: ", "))")
        }
        .onAppear {
            refreshPathValidation()
            consumePendingOutlineKey()
        }
        .onChange(of: model.pendingOutlineAccessKey) { _, _ in
            consumePendingOutlineKey()
        }
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
            if model.dashboard.status.outlineRefresh.dynamic {
                Label("Dynamic Outline profile", systemImage: "arrow.triangle.2.circlepath")
                    .font(.caption)
                if model.dashboard.status.outlineRefresh.lastSuccessTimestampNS > 0 {
                    Text("Last refreshed \(outlineRefreshDate.formatted(date: .abbreviated, time: .shortened))")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                if model.dashboard.status.outlineRefresh.stale {
                    Label(model.dashboard.status.outlineRefresh.warning,
                          systemImage: "exclamationmark.triangle.fill")
                        .font(.caption)
                        .foregroundStyle(.orange)
                }
            } else {
                Text("Static profile")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var outlineRefreshDate: Date {
        Date(timeIntervalSince1970: Double(
            model.dashboard.status.outlineRefresh.lastSuccessTimestampNS) / 1_000_000_000)
    }

    private var outlineSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Import Outline Access Key")
                .font(.headline)
            Text("Paste or open a standard ss:// or basic dynamic ssconf:// key. ClambHook always reviews it before import and never auto-connects.")
                .font(.caption)
                .foregroundStyle(.secondary)
            TextEditor(text: $outlineKey)
                .font(.system(.caption, design: .monospaced))
                .frame(minHeight: 72)
                .accessibilityLabel("Outline access key")
                .onChange(of: outlineKey) { _, _ in outlineReview = nil }
            TextField("Profile name", text: $outlineProfileName)
                .accessibilityLabel("Outline profile name")
            if let review = outlineReview {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Compatibility preview (credentials hidden)")
                        .font(.subheadline.weight(.semibold))
                    Text("\(review.kind.capitalized) · TCP \(review.tcp.address) · \(review.tcp.cipher) · prefix \(review.tcp.prefixLength) byte(s)")
                    Text("UDP \(review.udp.address) · \(review.udp.cipher) · prefix \(review.udp.prefixLength) byte(s)")
                    if review.lowEntropyWarning {
                        Label("The prefix leaves little random salt entropy.",
                              systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.orange)
                    }
                    Text("Will create profile: \(outlineProfileName)")
                        .fontWeight(.semibold)
                }
                .font(.caption)
            }
            if !outlineError.isEmpty {
                Text(outlineError).font(.caption).foregroundStyle(.red)
            }
            HStack {
                Button("Paste") {
                    outlineKey = NSPasteboard.general.string(forType: .string) ?? ""
                }
                Button("Review") { reviewOutlineKey() }
                    .disabled(outlineBusy || outlineKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                Button("Import") { importOutlineKey() }
                    .buttonStyle(.borderedProminent)
                    .disabled(outlineBusy || outlineReview == nil ||
                              reviewedOutlineKey != outlineKey ||
                              outlineProfileName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                Spacer()
                Button("Refresh Dynamic Profile") { refreshOutlineProfile() }
                    .disabled(outlineBusy || model.dashboard.status.running ||
                              !model.dashboard.status.outlineRefresh.dynamic)
            }
        }
    }

    private func consumePendingOutlineKey() {
        guard !model.pendingOutlineAccessKey.isEmpty else { return }
        outlineKey = model.pendingOutlineAccessKey
        model.pendingOutlineAccessKey = ""
        outlineReview = nil
    }

    private func reviewOutlineKey() {
        outlineBusy = true
        outlineError = ""
        let key = outlineKey
        Task {
            do {
                let review = try await model.reviewOutlineAccessKey(key)
                outlineReview = review
                reviewedOutlineKey = key
                if outlineProfileName == "Outline" || outlineProfileName.isEmpty {
                    outlineProfileName = review.suggestedName
                }
            } catch {
                outlineReview = nil
                outlineError = error.localizedDescription
            }
            outlineBusy = false
        }
    }

    private func importOutlineKey() {
        outlineBusy = true
        outlineError = ""
        Task {
            do {
                try await model.importOutlineAccessKey(
                    outlineKey, profileName: outlineProfileName)
                outlineKey = ""
                outlineReview = nil
            } catch {
                outlineError = error.localizedDescription
            }
            outlineBusy = false
        }
    }

    private func refreshOutlineProfile() {
        outlineBusy = true
        outlineError = ""
        Task {
            do {
                try await model.refreshOutlineProfile(model.dashboard.activeProfile)
            } catch {
                outlineError = error.localizedDescription
            }
            outlineBusy = false
        }
    }

    private var converterSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Convert Mihomo or Surge Profile")
                .font(.headline)
            Text("Conversion is offline. Unsupported items are omitted only after review and are listed below; exported TOML can contain credentials.")
                .font(.caption)
                .foregroundStyle(.secondary)
            Picker("Source format", selection: $converterFormat) {
                Text("Auto-detect").tag("auto")
                Text("Mihomo YAML").tag("mihomo")
                Text("Surge profile").tag("surge")
            }
            .pickerStyle(.segmented)
            .onChange(of: converterFormat) { _, _ in converterReview = nil }
            TextField("Converted profile name", text: $converterProfileName)
                .accessibilityLabel("Converted profile name")
                .onChange(of: converterProfileName) { _, _ in converterReview = nil }
            TextEditor(text: $converterSource)
                .font(.system(.caption, design: .monospaced))
                .frame(minHeight: 110)
                .accessibilityLabel("Mihomo or Surge source profile")
                .onChange(of: converterSource) { _, _ in converterReview = nil }
            if let review = converterReview, let summary = review.profiles.first {
                VStack(alignment: .leading, spacing: 4) {
                    Text("\(review.format.capitalized) · \(summary.chainCount) chains · \(summary.groupCount) groups · \(summary.ruleCount) rules")
                        .font(.caption.weight(.semibold))
                    ForEach(review.warnings) { warning in
                        Label("\(warning.path): \(warning.message)",
                              systemImage: "exclamationmark.triangle.fill")
                            .font(.caption)
                            .foregroundStyle(.orange)
                    }
                }
            }
            if !converterError.isEmpty {
                Text(converterError).font(.caption).foregroundStyle(.red)
            }
            Toggle("Activate after import", isOn: $converterActivate)
                .font(.caption)
            HStack {
                Button("Open File") { openConverterFile() }
                Button("Review") { reviewConversion() }
                    .disabled(converterBusy || converterSource.isEmpty || converterProfileName.isEmpty)
                Button("Merge") { importConversion() }
                    .buttonStyle(.borderedProminent)
                    .disabled(converterBusy || converterReview == nil)
                Button("Export TOML") { exportConversion() }
                    .disabled(converterReview == nil)
            }
        }
    }

    private func openConverterFile() {
        let panel = NSOpenPanel()
        panel.title = "Open Mihomo or Surge profile"
        panel.allowedContentTypes = ["yaml", "yml", "conf"].compactMap {
            .init(filenameExtension: $0)
        }
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = false
        if panel.runModal() == .OK, let url = panel.url {
            do {
                converterSource = try String(contentsOf: url, encoding: .utf8)
                converterProfileName = url.deletingPathExtension().lastPathComponent
                converterFormat = url.pathExtension.lowercased() == "conf" ? "surge" : "mihomo"
                converterReview = nil
                converterError = ""
            } catch { converterError = error.localizedDescription }
        }
    }

    private func reviewConversion() {
        converterBusy = true
        converterError = ""
        let source = converterSource
        let name = converterProfileName
        let format = converterFormat
        Task {
            do {
                let review = try await model.reviewProfileConversion(
                    source: source, format: format, profileName: name)
                if source == converterSource && name == converterProfileName {
                    converterReview = review
                }
            } catch { converterReview = nil; converterError = error.localizedDescription }
            converterBusy = false
        }
    }

    private func importConversion() {
        guard let review = converterReview else { return }
        converterBusy = true
        converterError = ""
        Task {
            do {
                try await model.importProfileConversion(
                    source: converterSource, format: converterFormat,
                    profileName: converterProfileName,
                    expectedSHA256: review.sha256, activate: converterActivate)
                converterSource = ""
                converterReview = nil
            } catch { converterError = error.localizedDescription }
            converterBusy = false
        }
    }

    private func exportConversion() {
        guard let review = converterReview else { return }
        let panel = NSSavePanel()
        panel.title = "Export converted ClambHook profile"
        panel.nameFieldStringValue = "\(converterProfileName).toml"
        panel.allowedContentTypes = [.init(filenameExtension: "toml") ?? .data]
        if panel.runModal() == .OK, let url = panel.url {
            do { try review.toml.write(to: url, atomically: true, encoding: .utf8) }
            catch { converterError = error.localizedDescription }
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
                        message: Text("Sensitive clambhook TOML export; it may contain passwords and dynamic Outline URLs.")
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
        panel.title = "Import ClambHook, Mihomo, or Surge profile"
        panel.allowedContentTypes = ["toml", "yaml", "yml", "conf"].compactMap {
            .init(filenameExtension: $0)
        }
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = false
        if panel.runModal() == .OK, let url = panel.url {
            do {
                let text = try String(contentsOf: url, encoding: .utf8)
                if url.pathExtension.lowercased() != "toml" {
                    converterSource = text
                    converterProfileName = url.deletingPathExtension().lastPathComponent
                    converterFormat = url.pathExtension.lowercased() == "conf" ?
                        "surge" : "mihomo"
                    converterReview = nil
                    converterError = ""
                    reviewConversion()
                    return
                }
                _ = try TunnelImportDecoder.decode(text)
                Task {
                    do {
                        let review = try await model.reviewConfigImport(text)
                        pendingImportReview = review
                        importPreviewProfiles = review.profiles.map(\.name)
                        pendingImportText = text
                        importError = ""
                        showImportConfirm = true
                    } catch { importError = error.localizedDescription }
                }
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
