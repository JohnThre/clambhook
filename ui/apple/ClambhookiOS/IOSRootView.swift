import ClambhookShared
import SwiftUI
import UniformTypeIdentifiers
import UIKit

struct IOSRootView: View {
    @ObservedObject var model: AppleAppModel
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass
    @Environment(\.scenePhase) private var scenePhase
    @State private var selectedDestination: IOSAppDestination = .dashboard
    @State private var compactPath: [IOSAppDestination] = []
    @State private var showingOnboarding = false
    @AppStorage("org.jpfchang.clambhook.onboardingComplete") private var onboardingComplete = false
    @StateObject private var inspectionLock = InspectionLockState()

    var body: some View {
        Group {
            if !model.mobileLicenseDecision.canUseApp {
                IOSLicenseGateView(model: model)
            } else if horizontalSizeClass == .regular {
                splitView
            } else {
                compactNavigationView
            }
        }
        .fullScreenCover(isPresented: $showingOnboarding) {
            IOSOnboardingView(model: model) {
                onboardingComplete = true
                showingOnboarding = false
                model.refresh()
            }
        }
        .overlay {
            if shouldShowInspectionLock {
                IOSInspectionLockOverlay(state: inspectionLock) {
                    Task { await authenticateInspectionLock() }
                }
            }
        }
        .task {
            if model.mobileLicenseDecision.canUseApp, !onboardingComplete || model.shouldShowOnboarding() {
                showingOnboarding = true
            }
            engageInspectionLockIfNeeded()
        }
        .onChange(of: scenePhase) { _, phase in
            switch phase {
            case .active:
                engageInspectionLockIfNeeded()
            case .background:
                inspectionLock.lockIfNeeded(enabled: model.settingsStore.settings.inspectionLockEnabled)
            default:
                break
            }
        }
        .onChange(of: model.settingsStore.settings.inspectionLockEnabled) { _, enabled in
            if enabled {
                engageInspectionLockIfNeeded()
            } else {
                inspectionLock.clearLock()
            }
        }
        .onChange(of: model.mobileLicenseDecision.canUseApp) { _, canUseApp in
            if canUseApp {
                if !onboardingComplete || model.shouldShowOnboarding() {
                    showingOnboarding = true
                }
            } else {
                showingOnboarding = false
            }
        }
    }

    private var compactNavigationView: some View {
        NavigationStack(path: $compactPath) {
            IOSStatusView(model: model, onRecoveryAction: handleRecoveryAction)
                .navigationTitle(IOSAppDestination.dashboard.title)
                .navigationDestination(for: IOSAppDestination.self) { destination in
                    destinationView(destination)
                        .navigationTitle(destination.title)
                }
                .toolbar {
                    ToolbarItem(placement: .topBarTrailing) {
                        Menu {
                            ForEach(IOSAppDestination.shellCases) { destination in
                                Button {
                                    if destination == .dashboard {
                                        compactPath.removeAll()
                                    } else {
                                        compactPath = [destination]
                                    }
                                } label: {
                                    Label(destination.title, systemImage: destination.systemImage)
                                }
                            }
                        } label: {
                            Image(systemName: "ellipsis.circle")
                        }
                        .accessibilityLabel("Navigate")
                    }
                }
            }
    }

    private var splitView: some View {
        NavigationSplitView {
            List {
                Section {
                    ForEach(IOSAppDestination.shellCases) { destination in
                        Button {
                            selectedDestination = destination
                        } label: {
                            HStack {
                                Label(destination.title, systemImage: destination.systemImage)
                                Spacer()
                                if destination == selectedDestination {
                                    Image(systemName: "checkmark")
                                        .foregroundStyle(.tint)
                                }
                            }
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
            .navigationTitle("clambhook")
        } detail: {
            NavigationStack {
                destinationView(selectedDestination)
                    .navigationTitle(selectedDestination.title)
            }
        }
    }

    @ViewBuilder
    private func destinationView(_ destination: IOSAppDestination) -> some View {
        switch destination {
        case .dashboard:
            IOSStatusView(model: model, onRecoveryAction: handleRecoveryAction)
        case .profiles:
            IOSProfilesView(model: model)
        case .rules:
            IOSRulesView(model: model)
        case .activity:
            IOSActivityView(model: model)
        case .settings:
            AppSettingsView(model: model)
        }
    }

    private func badgeCount(for destination: IOSAppDestination) -> Int? {
        switch destination {
        case .dashboard:
            return model.attention.dueScheduledItems().count + todayIncidentCount
        case .profiles:
            return model.dashboard.profiles.profiles.count
        case .rules:
            return model.dashboard.rules.rules.count
        case .activity:
            return model.dashboard.traffic.connections.count
                + CaptureSupport.captureEntries(from: model.dashboard.traffic).count
        case .settings:
            return nil
        }
    }

    private var todayIncidentCount: Int {
        var count = 0
        if !model.dashboard.errorText.isEmpty {
            count += 1
        }
        if !model.dashboard.traffic.summary.persistError.isEmpty {
            count += 1
        }
        count += model.dashboard.passiveServerHealth.values.filter { !$0.lastError.isEmpty }.count
        return count
    }

    private var shouldShowInspectionLock: Bool {
        model.mobileLicenseDecision.canUseApp && model.settingsStore.settings.inspectionLockEnabled && inspectionLock.isLocked
    }

    private func engageInspectionLockIfNeeded() {
        inspectionLock.lockIfNeeded(enabled: model.settingsStore.settings.inspectionLockEnabled)
        Task { await authenticateInspectionLock() }
    }

    private func handleRecoveryAction(_ action: TunnelRecoveryAction) {
        if action == .openProfiles || action == .importProfile {
            selectedDestination = .profiles
            compactPath = [selectedDestination]
        }
        model.performRecoveryAction(action)
    }

    private func authenticateInspectionLock() async {
        await inspectionLock.authenticateIfNeeded(enabled: model.settingsStore.settings.inspectionLockEnabled)
    }
}

private struct IOSInspectionLockOverlay: View {
    @ObservedObject var state: InspectionLockState
    var onUnlock: () -> Void

    var body: some View {
        ZStack {
            Color(.systemBackground)
                .ignoresSafeArea()
            VStack(spacing: 18) {
                Image(systemName: "lock.shield")
                    .font(.system(size: 52, weight: .semibold))
                    .foregroundStyle(.tint)
                VStack(spacing: 6) {
                    Text("Activity Locked")
                        .font(.title2.weight(.semibold))
                    Text("Use \(state.status.label) to view local inspection details.")
                        .font(.body)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                }
                if !state.message.isEmpty {
                    Text(state.message)
                        .font(.footnote)
                        .foregroundStyle(.red)
                        .multilineTextAlignment(.center)
                }
                Button {
                    onUnlock()
                } label: {
                    if state.isAuthenticating {
                        ProgressView()
                    } else {
                        Label("Unlock", systemImage: "faceid")
                    }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(state.isAuthenticating || !state.status.isAvailable)
            }
            .padding(28)
            .frame(maxWidth: 360)
        }
        .accessibilityIdentifier("inspection-lock")
    }
}

private struct IOSLicenseGateView: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    VStack(alignment: .leading, spacing: 10) {
                        Label("Purchase required", systemImage: "lock.fill")
                            .font(.headline)
                            .foregroundStyle(.red)
                        Text(licenseDetail)
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    Link("Support", destination: defaultSupportURL)
                    Link("Privacy Policy", destination: defaultPrivacyPolicyURL)
                }
                PremiumPurchasesSection(manager: model.licenseManager)
                Section("Settings") {
                    NavigationLink {
                        AppSettingsView(model: model)
                    } label: {
                        Label("Open Settings", systemImage: "gearshape")
                    }
                }
            }
            .formStyle(.grouped)
            .navigationTitle("clambhook")
        }
    }

    private var licenseDetail: String {
        let decision = model.mobileLicenseDecision
        if let trialEndsAt = decision.trialEndsAt {
            return "The free trial ended \(trialEndsAt.formatted(date: .abbreviated, time: .omitted)). Purchase or restore the lifetime unlock to continue."
        }
        return "Purchase or restore the lifetime unlock to continue."
    }
}

private struct IOSDestinationRow: View {
    var destination: IOSAppDestination
    var count: Int?

    var body: some View {
        HStack(spacing: 12) {
            Label(destination.title, systemImage: destination.systemImage)
            Spacer()
            if let count, count > 0 {
                Text("\(count)")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .monospacedDigit()
            }
        }
    }
}

private enum IOSAppDestination: String, CaseIterable, Identifiable, Hashable {
    case dashboard
    case profiles
    case rules
    case activity
    case settings

    var id: Self { self }

    static var shellCases: [IOSAppDestination] {
        [.dashboard, .profiles, .rules, .activity, .settings]
    }

    var title: String {
        switch self {
        case .dashboard:
            return "Dashboard"
        case .profiles:
            return "Profiles"
        case .rules:
            return "Rules"
        case .activity:
            return "Activity"
        case .settings:
            return "Settings"
        }
    }

    var systemImage: String {
        switch self {
        case .dashboard:
            return "network"
        case .profiles:
            return "person.crop.rectangle.stack"
        case .rules:
            return "slider.horizontal.3"
        case .activity:
            return "clock.arrow.circlepath"
        case .settings:
            return "gearshape"
        }
    }
}

private struct IOSAnytimeView: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        List {
            Section("Profiles") {
                NavigationLink {
                    IOSProfilesView(model: model)
                } label: {
                    IOSLibraryRow(
                        title: "Profiles, Policies, Rules",
                        detail: "\(model.dashboard.profiles.profiles.count) profiles",
                        systemImage: "slider.horizontal.3"
                    )
                }
            }

            Section("Rules") {
                NavigationLink {
                    IOSRulesView(model: model)
                } label: {
                    IOSLibraryRow(
                        title: "Rule Order",
                        detail: "\(model.dashboard.rules.rules.count) rules",
                        systemImage: "list.number"
                    )
                }
            }

            Section("Maintenance") {
                NavigationLink {
                    IOSUpcomingView(model: model)
                } label: {
                    IOSLibraryRow(
                        title: "Maintenance Checks",
                        detail: "\(model.attention.upcomingScheduledItems().count) planned checks",
                        systemImage: "calendar"
                    )
                }

                NavigationLink {
                    IOSSomedayView(model: model)
                } label: {
                    IOSLibraryRow(
                        title: "Config Drafts",
                        detail: "\(model.attention.state.someday.count) saved drafts",
                        systemImage: "archivebox"
                    )
                }
            }
        }
        .listStyle(.insetGrouped)
    }
}

private struct IOSLogbookView: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        List {
            Section("Traffic") {
                NavigationLink {
                    IOSActivityView(model: model, logbookOnly: true)
                } label: {
                    IOSLibraryRow(
                        title: "Historical Traffic",
                        detail: "\(model.dashboard.traffic.connections.count) connections",
                        systemImage: "waveform.path.ecg"
                    )
                }
            }

            Section("Capture") {
                NavigationLink {
                    IOSHTTPCaptureView(model: model)
                } label: {
                    IOSLibraryRow(
                        title: "HTTP Capture",
                        detail: "\(CaptureSupport.captureEntries(from: model.dashboard.traffic).count) metadata requests",
                        systemImage: "network"
                    )
                }
            }

            Section("Decisions") {
                IOSLibraryRow(
                    title: "Routes and Alerts",
                    detail: "\(model.dashboard.rules.rules.count) ordered rules, \(model.dashboard.passiveServerHealth.count) server checks",
                    systemImage: "checklist"
                )
            }
        }
        .listStyle(.insetGrouped)
    }
}

private struct IOSLibraryRow: View {
    var title: String
    var detail: String
    var systemImage: String

    var body: some View {
        Label {
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.body.weight(.medium))
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        } icon: {
            Image(systemName: systemImage)
                .foregroundStyle(.secondary)
        }
    }
}

struct IOSProfileImportsView: View {
    @ObservedObject var model: AppleAppModel
    @State private var showingFileImporter = false
    @State private var showingQRScanner = false
    @State private var message = ""
    @State private var stagedReviewItem: InboxImportItem?

    init(model: AppleAppModel) {
        self.model = model
    }

    var body: some View {
        List {
            if !message.isEmpty {
                Section {
                    Text(message)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }

            Section("Add Profiles") {
                Button {
                    showingFileImporter = true
                } label: {
                    Label("Import from Files", systemImage: "doc.badge.plus")
                }

                Button {
                    stageFromClipboard()
                } label: {
                    Label("Import from Clipboard", systemImage: "doc.on.clipboard")
                }

                Button {
                    showingQRScanner = true
                } label: {
                    Label("Scan QR", systemImage: "qrcode.viewfinder")
                }
            }

            Section("Pending Review") {
                if model.attention.state.inbox.isEmpty {
                    ContentUnavailableView(
                        "No staged profiles",
                        systemImage: "tray.and.arrow.down",
                        description: Text("Imported configs wait here until you review or delete them.")
                    )
                } else {
                    ForEach(model.attention.state.inbox) { item in
                        NavigationLink {
                            IOSInboxItemDetailView(model: model, itemID: item.id)
                        } label: {
                            IOSInboxItemRow(item: item)
                        }
                        .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                            Button(role: .destructive) {
                                model.attention.removeInboxItem(id: item.id)
                            } label: {
                                Label("Delete", systemImage: "trash")
                            }
                        }
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .fileImporter(
            isPresented: $showingFileImporter,
            allowedContentTypes: [.text, .plainText, .data],
            allowsMultipleSelection: false
        ) { result in
            stageFromFile(result)
        }
        .sheet(isPresented: $showingQRScanner) {
            IOSInboxQRScannerSheet(message: $message) { rawValue in
                stage(rawValue, source: .qr, title: "QR import")
            }
        }
        .sheet(item: $stagedReviewItem) { item in
            NavigationStack {
                IOSInboxItemDetailView(model: model, itemID: item.id)
            }
        }
        .navigationTitle("Profile Imports")
    }

    @discardableResult
    private func stage(_ rawValue: String, source: InboxImportSource, title: String = "") -> Bool {
        do {
            let item = try model.attention.captureImport(rawValue: rawValue, source: source, title: title)
            message = "Staged \(item.title)."
            stagedReviewItem = item
            return true
        } catch {
            message = error.localizedDescription
            return false
        }
    }

    private func stageFromClipboard() {
        guard let text = UIPasteboard.general.string, !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            message = "Clipboard does not contain profile text."
            return
        }
        _ = stage(text, source: .clipboard, title: "Clipboard import")
    }

    private func stageFromFile(_ result: Result<[URL], Error>) {
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
            _ = stage(try String(contentsOf: url, encoding: .utf8), source: .file, title: url.lastPathComponent)
        } catch {
            message = error.localizedDescription
        }
    }
}

private struct IOSInboxItemRow: View {
    var item: InboxImportItem

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: item.sourceIcon)
                .foregroundStyle(.secondary)
                .frame(width: 24)
            VStack(alignment: .leading, spacing: 4) {
                Text(item.title)
                    .font(.body.weight(.medium))
                    .lineLimit(1)
                Text(item.preview.summary)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                if !item.lastError.isEmpty {
                    Label(item.lastError, systemImage: "exclamationmark.triangle.fill")
                        .font(.caption)
                        .foregroundStyle(.red)
                        .lineLimit(2)
                }
            }
        }
        .padding(.vertical, 2)
    }
}

struct IOSInboxItemDetailView: View {
    @ObservedObject var model: AppleAppModel
    var itemID: UUID
    @Environment(\.dismiss) private var dismiss
    @State private var reviewPayload: TunnelImportReviewPayload?
    @State private var drafts: [IOSReviewedProfileDraft] = []
    @State private var activateProfile = ""
    @State private var validationMessage = ""
    @State private var validationError = ""

    var body: some View {
        if let item {
            List {
                Section("Import") {
                    LabeledContent("Source", value: item.source.displayName)
                    LabeledContent("Captured", value: item.createdAt.formatted(date: .abbreviated, time: .shortened))
                    LabeledContent("Summary", value: item.preview.summary)
                }

                if !item.lastError.isEmpty {
                    Section("Status") {
                        Label(item.lastError, systemImage: "exclamationmark.triangle.fill")
                            .foregroundStyle(.red)
                    }
                }

                if let reviewPayload {
                    Section("Profiles") {
                        ForEach($drafts) { $draft in
                            IOSReviewedProfileRow(draft: $draft)
                        }
                    }

                    Section("Activation") {
                        Picker("Activate", selection: $activateProfile) {
                            Text("Keep current").tag("")
                            ForEach(selectedTargetNames, id: \.self) { profile in
                                Text(profile).tag(profile)
                            }
                        }
                        .disabled(selectedTargetNames.isEmpty)
                        LabeledContent("Imported active", value: emptyDash(reviewPayload.activeProfile))
                    }

                    if !validationError.isEmpty || !validationMessage.isEmpty {
                        Section("Validation") {
                            if !validationError.isEmpty {
                                Label(validationError, systemImage: "exclamationmark.triangle.fill")
                                    .foregroundStyle(.red)
                            } else {
                                Label(validationMessage, systemImage: "checkmark.circle.fill")
                                    .foregroundStyle(.green)
                            }
                        }
                    }
                } else {
                    Section {
                        IOSInlineEmptyState(text: "Preparing review.", systemImage: "tray.and.arrow.down")
                    }
                }

                Section("Preview") {
                    Text(item.preview.redactedSnippet)
                        .font(.system(.caption, design: .monospaced))
                        .textSelection(.enabled)
                }

                Section {
                    Button {
                        importReviewed(item)
                        if model.attention.state.inbox.first(where: { $0.id == itemID }) == nil {
                            dismiss()
                        }
                    } label: {
                        Label("Import Selected", systemImage: "tray.and.arrow.down")
                    }
                    .disabled(!canImportReviewed)

                    Button(role: .destructive) {
                        model.attention.removeInboxItem(id: itemID)
                        dismiss()
                    } label: {
                        Label("Delete", systemImage: "trash")
                    }
                }
            }
            .listStyle(.insetGrouped)
            .navigationTitle(item.title)
            .navigationBarTitleDisplayMode(.inline)
            .onAppear {
                loadReviewIfNeeded(item)
            }
            .onChange(of: drafts) { _, _ in
                validateReview(item)
            }
            .onChange(of: activateProfile) { _, _ in
                validateReview(item)
            }
        } else {
            ContentUnavailableView("Import removed", systemImage: "tray")
        }
    }

    private var item: InboxImportItem? {
        model.attention.state.inbox.first { $0.id == itemID }
    }

    private var selectedTargetNames: [String] {
        drafts
            .filter(\.included)
            .map { $0.targetName.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
    }

    private var canImportReviewed: Bool {
        reviewPayload != nil && !selectedTargetNames.isEmpty && validationError.isEmpty
    }

    private func loadReviewIfNeeded(_ item: InboxImportItem) {
        guard reviewPayload == nil else {
            return
        }
        do {
            let payload = try model.importReviewPayload(for: item)
            let nextDrafts = makeDrafts(from: payload)
            reviewPayload = payload
            drafts = nextDrafts
            activateProfile = defaultActivation(for: payload, drafts: nextDrafts)
            validateReview(item)
        } catch {
            validationError = error.localizedDescription
        }
    }

    private func makeDrafts(from payload: TunnelImportReviewPayload) -> [IOSReviewedProfileDraft] {
        var used = Set(model.dashboard.profiles.profiles)
        return payload.profiles.map { profile in
            let targetName = uniqueTargetName(from: profile.name, used: &used)
            return IOSReviewedProfileDraft(profile: profile, targetName: targetName)
        }
    }

    private func uniqueTargetName(from sourceName: String, used: inout Set<String>) -> String {
        let base = sourceName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "imported" : sourceName
        var candidate = base
        var suffix = 2
        while used.contains(candidate) {
            candidate = "\(base)-\(suffix)"
            suffix += 1
        }
        used.insert(candidate)
        return candidate
    }

    private func defaultActivation(for payload: TunnelImportReviewPayload, drafts: [IOSReviewedProfileDraft]) -> String {
        guard model.tunnelOnboardingReadinessMessage() != nil else {
            return ""
        }
        if !payload.activeProfile.isEmpty,
           let draft = drafts.first(where: { $0.sourceName == payload.activeProfile && $0.included }) {
            return draft.targetName
        }
        return drafts.first(where: \.included)?.targetName ?? ""
    }

    private func validateReview(_ item: InboxImportItem) {
        if !selectedTargetNames.contains(activateProfile) {
            activateProfile = ""
        }
        guard reviewPayload != nil else {
            validationMessage = ""
            return
        }
        guard !selectedTargetNames.isEmpty else {
            validationMessage = ""
            validationError = "Select at least one profile."
            return
        }
        do {
            try model.validateReviewedTunnelImport(makeRequest(item))
            validationError = ""
            validationMessage = "Ready to import \(selectedTargetNames.count) profile\(selectedTargetNames.count == 1 ? "" : "s")."
        } catch {
            validationMessage = ""
            validationError = error.localizedDescription
        }
    }

    private func importReviewed(_ item: InboxImportItem) {
        validateReview(item)
        guard canImportReviewed else {
            return
        }
        model.applyReviewedTunnelImport(
            item: item,
            request: makeRequest(item),
            tagsByProfile: Dictionary(uniqueKeysWithValues: drafts
                .filter(\.included)
                .map { ($0.targetName, tags(from: $0.tagsText)) })
        )
    }

    private func makeRequest(_ item: InboxImportItem) -> ReviewedTunnelImportRequest {
        ReviewedTunnelImportRequest(
            importText: item.decodedConfigText,
            profiles: drafts
                .filter(\.included)
                .map { ReviewedTunnelImportProfile(sourceName: $0.sourceName, targetName: $0.targetName) },
            activateProfile: activateProfile
        )
    }

    private func tags(from text: String) -> [String] {
        text.components(separatedBy: CharacterSet(charactersIn: ",\n"))
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
    }
}

private struct IOSReviewedProfileDraft: Identifiable, Equatable {
    var id: String { sourceName }
    var sourceName: String
    var targetName: String
    var tagsText: String
    var included: Bool
    var chainCount: Int
    var serverCount: Int
    var ruleCount: Int
    var protocols: [String]

    init(profile: TunnelImportReviewProfile, targetName: String) {
        self.sourceName = profile.name
        self.targetName = targetName
        self.tagsText = ""
        self.included = true
        self.chainCount = profile.chainCount
        self.serverCount = profile.serverCount
        self.ruleCount = profile.ruleCount
        self.protocols = profile.protocols
    }

    var summary: String {
        var parts: [String] = []
        parts.append(chainCount == 1 ? "1 chain" : "\(chainCount) chains")
        parts.append(serverCount == 1 ? "1 server" : "\(serverCount) servers")
        if ruleCount > 0 {
            parts.append(ruleCount == 1 ? "1 rule" : "\(ruleCount) rules")
        }
        if !protocols.isEmpty {
            parts.append(protocols.joined(separator: ", "))
        }
        return parts.joined(separator: " / ")
    }
}

private struct IOSReviewedProfileRow: View {
    @Binding var draft: IOSReviewedProfileDraft

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Toggle(isOn: $draft.included) {
                VStack(alignment: .leading, spacing: 3) {
                    Text(emptyDash(draft.sourceName))
                        .font(.body.weight(.medium))
                    Text(draft.summary)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }

            TextField("Profile name", text: $draft.targetName)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .disabled(!draft.included)

            TextField("Tags", text: $draft.tagsText, prompt: Text("work, backup"))
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .disabled(!draft.included)
        }
        .padding(.vertical, 4)
    }
}

private struct IOSInboxQRScannerSheet: View {
    @Binding var message: String
    var onStage: (String) -> Bool
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                IOSQRCodeScannerView { value in
                    if onStage(value) {
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

private struct IOSTodayView: View {
    @ObservedObject var model: AppleAppModel
    var onRecoveryAction: (TunnelRecoveryAction) -> Void

    var body: some View {
        List {
            Section("Tunnel") {
                VStack(alignment: .leading, spacing: 12) {
                    HStack(spacing: 12) {
                        Image(systemName: model.dashboard.status.running ? "network" : "network.slash")
                            .foregroundStyle(model.dashboard.status.running ? .green : .secondary)
                            .frame(width: 28)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(model.dashboard.status.running ? "Connected" : "Disconnected")
                                .font(.headline)
                            Text(emptyDash(model.dashboard.activeProfile))
                                .font(.subheadline)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }
                        Spacer()
                        IOSStatusBadge(
                            text: model.dashboard.apiOnline ? "Ready" : "Unavailable",
                            systemImage: "network",
                            tint: model.dashboard.apiOnline ? .green : .red
                        )
                    }

                    if let issue = model.dashboard.recoveryIssue {
                        IOSRecoveryBanner(issue: issue, onAction: onRecoveryAction)
                    }

                    ViewThatFits(in: .horizontal) {
                        HStack(spacing: 10) {
                            actionButtons
                        }
                        VStack(spacing: 10) {
                            actionButtons
                        }
                    }
                }
            }

            Section("Now") {
                IOSMetricsGrid(metrics: metrics)
                    .listRowInsets(EdgeInsets(top: 10, leading: 16, bottom: 10, trailing: 16))
            }

            Section("Incidents") {
                if incidents.isEmpty {
                    IOSInlineEmptyState(text: "No incidents need attention.", systemImage: "checkmark.circle")
                } else {
                    ForEach(incidents) { incident in
                        Label(incident.message, systemImage: incident.systemImage)
                            .foregroundStyle(incident.tint)
                    }
                }
            }

            Section("Due") {
                let due = model.attention.dueScheduledItems()
                if due.isEmpty {
                    IOSInlineEmptyState(text: "No scheduled checks are due.", systemImage: "calendar")
                } else {
                    ForEach(due) { item in
                        IOSScheduledItemRow(item: item)
                            .swipeActions {
                                Button {
                                    model.attention.completeScheduledItem(id: item.id)
                                } label: {
                                    Label("Complete", systemImage: "checkmark")
                                }
                                .tint(.green)
                            }
                    }
                }
            }

            Section("Active Connections") {
                let active = model.dashboard.traffic.inspectionConnections(
                    filter: .active,
                    pinnedIDs: model.pinnedConnectionIDs
                )
                if active.isEmpty {
                    IOSInlineEmptyState(text: "No active connections.", systemImage: "point.3.connected.trianglepath.dotted")
                } else {
                    ForEach(active.prefix(5)) { connection in
                        IOSConnectionSummaryRow(connection: connection)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .refreshable {
            await model.refreshNow()
        }
    }

    private var actionButtons: some View {
        Button {
            model.connectOrDisconnect()
        } label: {
            Label(
                model.dashboard.status.running ? "Disconnect" : "Connect",
                systemImage: model.dashboard.status.running ? "stop.fill" : "play.fill"
            )
            .frame(maxWidth: .infinity)
        }
        .buttonStyle(.borderedProminent)
        .controlSize(.large)
        .disabled(!model.dashboard.apiOnline && !model.dashboard.status.running)
    }

    private var metrics: [IOSMetric] {
        let sample = model.dashboard.currentBandwidth
        return [
            IOSMetric(title: "Down", value: formatRate(sample.rxBps), systemImage: "arrow.down"),
            IOSMetric(title: "Up", value: formatRate(sample.txBps), systemImage: "arrow.up"),
            IOSMetric(title: "Active", value: "\(model.dashboard.traffic.summary.activeConnections)", systemImage: "bolt.horizontal.circle"),
            IOSMetric(title: "Imports", value: "\(model.attention.state.inbox.count)", systemImage: "tray"),
        ]
    }

    private var incidents: [IOSTodayIncident] {
        var rows: [IOSTodayIncident] = []
        if let issue = model.dashboard.recoveryIssue {
            rows.append(IOSTodayIncident(message: issue.title, systemImage: "network.slash", tint: .red))
        } else if !model.dashboard.errorText.isEmpty {
            rows.append(IOSTodayIncident(message: model.dashboard.errorText, systemImage: "network.slash", tint: .red))
        }
        if !model.dashboard.traffic.summary.persistError.isEmpty {
            rows.append(IOSTodayIncident(message: model.dashboard.traffic.summary.persistError, systemImage: "externaldrive.badge.exclamationmark", tint: .red))
        }
        for health in model.dashboard.passiveServerHealth.values where !health.lastError.isEmpty {
            rows.append(IOSTodayIncident(message: health.lastError, systemImage: "exclamationmark.triangle.fill", tint: .orange))
        }
        return rows
    }
}

private struct IOSTodayIncident: Identifiable {
    var id = UUID()
    var message: String
    var systemImage: String
    var tint: Color
}

private struct IOSUpcomingView: View {
    @ObservedObject var model: AppleAppModel
    @State private var editingItem: ScheduledAttentionItem?
    @State private var showingEditor = false

    var body: some View {
        List {
            Section("Due") {
                let due = model.attention.dueScheduledItems()
                if due.isEmpty {
                    IOSInlineEmptyState(text: "Nothing is due.", systemImage: "calendar")
                } else {
                    ForEach(due) { item in
                        scheduledRowButton(item)
                    }
                }
            }

            Section("Future") {
                let upcoming = model.attention.upcomingScheduledItems()
                if upcoming.isEmpty {
                    ContentUnavailableView(
                        "No planned checks",
                        systemImage: "calendar",
                        description: Text("Plan server tests and credential renewals here.")
                    )
                } else {
                    ForEach(upcoming) { item in
                        scheduledRowButton(item)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button {
                    editingItem = nil
                    showingEditor = true
                } label: {
                    Image(systemName: "plus")
                }
                .accessibilityLabel("Add Maintenance Check")
            }
        }
        .sheet(isPresented: $showingEditor) {
            IOSScheduledEditorSheet(item: editingItem) { saved in
                if editingItem == nil {
                    _ = model.attention.addScheduledItem(
                        title: saved.title,
                        detail: saved.detail,
                        kind: saved.kind,
                        dueAt: saved.dueAt,
                        recurrence: saved.recurrence
                    )
                } else {
                    model.attention.updateScheduledItem(saved)
                }
            }
        }
    }

    private func scheduledRowButton(_ item: ScheduledAttentionItem) -> some View {
        Button {
            editingItem = item
            showingEditor = true
        } label: {
            IOSScheduledItemRow(item: item)
        }
        .buttonStyle(.plain)
        .swipeActions(edge: .trailing, allowsFullSwipe: false) {
            Button(role: .destructive) {
                model.attention.removeScheduledItem(id: item.id)
            } label: {
                Label("Delete", systemImage: "trash")
            }
            Button {
                model.attention.completeScheduledItem(id: item.id)
            } label: {
                Label("Complete", systemImage: "checkmark")
            }
            .tint(.green)
        }
    }
}

private struct IOSScheduledItemRow: View {
    var item: ScheduledAttentionItem

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: item.kind.systemImage)
                .foregroundStyle(.secondary)
                .frame(width: 24)
            VStack(alignment: .leading, spacing: 4) {
                Text(item.title)
                    .font(.body.weight(.medium))
                    .lineLimit(1)
                Text([item.kind.displayName, item.dueAt.formatted(date: .abbreviated, time: .shortened), item.recurrenceText].joined(separator: " / "))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                if !item.detail.isEmpty {
                    Text(item.detail)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
            }
        }
        .padding(.vertical, 2)
    }
}

private struct IOSScheduledEditorSheet: View {
    var item: ScheduledAttentionItem?
    var onSave: (ScheduledAttentionItem) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var title: String
    @State private var detail: String
    @State private var dueAt: Date
    @State private var kind: ScheduledAttentionKind
    @State private var recurrence: ScheduledRecurrence

    init(item: ScheduledAttentionItem?, onSave: @escaping (ScheduledAttentionItem) -> Void) {
        self.item = item
        self.onSave = onSave
        self._title = State(initialValue: item?.title ?? "")
        self._detail = State(initialValue: item?.detail ?? "")
        self._dueAt = State(initialValue: item?.dueAt ?? Date())
        self._kind = State(initialValue: item?.kind ?? .serverTest)
        self._recurrence = State(initialValue: item?.recurrence ?? .none)
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Item") {
                    TextField("Title", text: $title)
                    TextField("Detail", text: $detail, axis: .vertical)
                        .lineLimit(2...4)
                    Picker("Kind", selection: $kind) {
                        ForEach(ScheduledAttentionKind.allCases) { kind in
                            Text(kind.displayName).tag(kind)
                        }
                    }
                    DatePicker("Due", selection: $dueAt)
                    Picker("Repeat", selection: $recurrence) {
                        ForEach(ScheduledRecurrence.allCases) { recurrence in
                            Text(recurrence.displayName).tag(recurrence)
                        }
                    }
                }
            }
            .navigationTitle(item == nil ? "Maintenance Check" : "Edit")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Save") {
                        onSave(ScheduledAttentionItem(
                            id: item?.id ?? UUID(),
                            createdAt: item?.createdAt ?? Date(),
                            dueAt: dueAt,
                            completedAt: item?.completedAt,
                            kind: kind,
                            recurrence: recurrence,
                            title: title.trimmingCharacters(in: .whitespacesAndNewlines),
                            detail: detail.trimmingCharacters(in: .whitespacesAndNewlines)
                        ))
                        dismiss()
                    }
                    .fontWeight(.semibold)
                    .disabled(title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
    }
}

private struct IOSSomedayView: View {
    @ObservedObject var model: AppleAppModel
    @State private var showingEditor = false

    var body: some View {
        List {
            Section("Draft Profiles") {
                if model.attention.state.someday.isEmpty {
                    ContentUnavailableView(
                        "No saved profile drafts",
                        systemImage: "archivebox",
                        description: Text("Inactive profile configs can be saved here until you are ready to review them.")
                    )
                } else {
                    ForEach(model.attention.state.someday) { item in
                        NavigationLink {
                            IOSSomedayDetailView(model: model, itemID: item.id)
                        } label: {
                            IOSSomedayItemRow(item: item)
                        }
                        .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                            Button(role: .destructive) {
                                model.attention.removeSomedayItem(id: item.id)
                            } label: {
                                Label("Delete", systemImage: "trash")
                            }
                            Button {
                                model.attention.restoreSomedayItemToInbox(id: item.id)
                            } label: {
                                Label("Profile Imports", systemImage: "tray")
                            }
                            .tint(.blue)
                        }
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button {
                    showingEditor = true
                } label: {
                    Image(systemName: "plus")
                }
                .accessibilityLabel("Add Profile Draft")
            }
        }
        .sheet(isPresented: $showingEditor) {
            IOSSomedayEditorSheet { title, detail in
                _ = model.attention.addSomedayItem(title: title, detail: detail)
            }
        }
    }
}

private struct IOSSomedayItemRow: View {
    var item: SomedayExperimentItem

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: "archivebox")
                .foregroundStyle(.secondary)
                .frame(width: 24)
            VStack(alignment: .leading, spacing: 4) {
                Text(item.title)
                    .font(.body.weight(.medium))
                    .lineLimit(1)
                Text(item.detail.isEmpty ? item.createdAt.formatted(date: .abbreviated, time: .shortened) : item.detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 2)
    }
}

private struct IOSSomedayDetailView: View {
    @ObservedObject var model: AppleAppModel
    var itemID: UUID
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        if let item {
            List {
                Section("Profile Draft") {
                    LabeledContent("Created", value: item.createdAt.formatted(date: .abbreviated, time: .shortened))
                    if !item.detail.isEmpty {
                        Text(item.detail)
                    }
                }

                if !item.configText.isEmpty {
                    Section("Preview") {
                        Text(InboxImportPreview(configText: item.configText).redactedSnippet)
                            .font(.system(.caption, design: .monospaced))
                            .textSelection(.enabled)
                    }
                }

                Section {
                    Button {
                        model.attention.restoreSomedayItemToInbox(id: itemID)
                        dismiss()
                    } label: {
                        Label("Move to Profile Imports", systemImage: "tray")
                    }

                    Button(role: .destructive) {
                        model.attention.removeSomedayItem(id: itemID)
                        dismiss()
                    } label: {
                        Label("Delete", systemImage: "trash")
                    }
                }
            }
            .listStyle(.insetGrouped)
            .navigationTitle(item.title)
            .navigationBarTitleDisplayMode(.inline)
        } else {
            ContentUnavailableView("Profile draft removed", systemImage: "archivebox")
        }
    }

    private var item: SomedayExperimentItem? {
        model.attention.state.someday.first { $0.id == itemID }
    }
}

private struct IOSSomedayEditorSheet: View {
    var onSave: (String, String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var title = ""
    @State private var detail = ""

    var body: some View {
        NavigationStack {
            Form {
                Section("Profile Draft") {
                    TextField("Title", text: $title)
                    TextField("Detail", text: $detail, axis: .vertical)
                        .lineLimit(2...4)
                }
            }
            .navigationTitle("Add Profile Draft")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Save") {
                        onSave(
                            title.trimmingCharacters(in: .whitespacesAndNewlines),
                            detail.trimmingCharacters(in: .whitespacesAndNewlines)
                        )
                        dismiss()
                    }
                    .fontWeight(.semibold)
                    .disabled(title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
    }
}

private struct IOSConnectionSummaryRow: View {
    var connection: TrafficConnectionPayload

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                IOSActionChip(action: connection.ruleAction.isEmpty ? "proxy" : connection.ruleAction)
                Text(emptyDash(connection.target))
                    .font(.body.weight(.medium))
                    .lineLimit(1)
                Spacer(minLength: 8)
            }
            Text([connection.displayVisibility, connection.ruleName, connection.chainName].filter { !$0.isEmpty }.joined(separator: " / "))
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(2)
        }
        .padding(.vertical, 2)
    }
}

private extension InboxImportItem {
    var sourceIcon: String {
        switch source {
        case .file:
            return "doc"
        case .clipboard:
            return "doc.on.clipboard"
        case .qr:
            return "qrcode"
        case .manual:
            return "square.and.pencil"
        }
    }
}

private extension ScheduledAttentionKind {
    var systemImage: String {
        switch self {
        case .serverTest:
            return "checkmark.seal"
        case .credentialRenewal:
            return "key"
        }
    }
}

private extension ScheduledAttentionItem {
    var recurrenceText: String {
        recurrence == .none ? "No repeat" : recurrence.displayName
    }
}
