import ClambhookShared
import Combine
import Foundation
import SwiftUI
#if os(iOS)
import UIKit
#endif
#if os(iOS) && !DEBUG && !canImport(ClambhookMobile)
#error("Mobile must be importable for iOS Release/App Store builds. Run make build-ios-mobile-xcframework before building the release app.")
#endif
#if os(iOS) && canImport(ClambhookMobile)
import ClambhookMobile
#endif

#if os(iOS) && canImport(ClambhookMobile)
private func mobileConfigError(_ description: String) -> NSError {
    NSError(
        domain: "org.jpfchang.clambhook.mobile",
        code: 1,
        userInfo: [NSLocalizedDescriptionKey: description]
    )
}

private func mobileBool(_ operation: (NSErrorPointer) -> Bool) throws {
    var error: NSError?
    if !operation(&error) {
        throw error ?? mobileConfigError("Mobile config operation failed")
    }
}

private func mobileString(_ operation: (NSErrorPointer) -> String) throws -> String {
    var error: NSError?
    let value = operation(&error)
    if let error {
        throw error
    }
    return value
}
#endif

@MainActor
final class AppleAppModel: ObservableObject {
    @Published var settingsStore: AppSettingsStore
    @Published private(set) var dashboard: DashboardStore
    @Published private(set) var attention: AttentionStore
    @Published private(set) var profileMetadata: ProfileMetadataStore
    @Published var apiToken = ""
    @Published var daemonMessage = ""

    let platform: AppPlatform
    private let credentialStore: CredentialStoring
    private var apiClient: ClambhookAPIClient?
    private var dashboardAPI: ClambhookAPIProviding
    private var snapshotStore: FileSnapshotStore
    private var pollingTask: Task<Void, Never>?
    private var dashboardChangeCancellable: AnyCancellable?
    private var attentionChangeCancellable: AnyCancellable?
    private var profileMetadataChangeCancellable: AnyCancellable?
    private var settingsChangeCancellable: AnyCancellable?
    private var started = false

    #if os(iOS)
    let tunnelController = IOSTunnelController()
    #endif

    #if os(macOS)
    let daemonSupervisor = DaemonSupervisor()
    #endif

    convenience init(platform: AppPlatform) {
        self.init(
            platform: platform,
            settingsStore: AppSettingsStore(defaults: UserDefaults(suiteName: defaultAppGroupIdentifier) ?? .standard),
            credentialStore: defaultCredentialStore()
        )
    }

    init(platform: AppPlatform, settingsStore: AppSettingsStore, credentialStore: CredentialStoring) {
        self.platform = platform
        self.settingsStore = settingsStore
        self.credentialStore = credentialStore
        self.snapshotStore = FileSnapshotStore.appGroupStore(groupIdentifier: settingsStore.settings.appGroupIdentifier)
        let initialToken = (try? credentialStore.readToken(account: settingsStore.settings.apiEndpoint.absoluteString)) ?? ""
        self.apiToken = initialToken
        #if os(iOS)
        let initialDashboardAPI = TunnelDashboardClient(controller: tunnelController)
        self.apiClient = nil
        self.dashboardAPI = initialDashboardAPI
        #else
        let initialAPIClient = ClambhookAPIClient(baseURL: settingsStore.settings.apiEndpoint, tokenProvider: { initialToken })
        self.apiClient = initialAPIClient
        self.dashboardAPI = initialAPIClient
        #endif
        self.dashboard = DashboardStore(
            api: dashboardAPI,
            snapshotStore: snapshotStore,
            logRetention: settingsStore.settings.logRetention
        )
        self.attention = AttentionStore.appGroupStore(groupIdentifier: settingsStore.settings.appGroupIdentifier)
        self.profileMetadata = ProfileMetadataStore.appGroupStore(groupIdentifier: settingsStore.settings.appGroupIdentifier)
        bindChildStores()
        #if os(macOS)
        if platform == .macOS {
            Task { @MainActor [weak self] in
                self?.start()
            }
        }
        #endif
    }

    func start() {
        guard !started else {
            refresh()
            return
        }
        started = true
        reloadClient()
        #if os(macOS)
        if settingsStore.settings.launchDaemonOnStart {
            launchDaemon()
        }
        #endif
        if let apiClient {
            dashboard.startEventStream(from: apiClient)
        }
        startPolling()
        Task {
            await dashboard.refreshDashboard()
            syncProfileRecoveryIssue()
        }
    }

    func stop() {
        pollingTask?.cancel()
        pollingTask = nil
        dashboard.stopEventStream()
        #if os(macOS)
        if settingsStore.settings.stopDaemonOnQuit {
            daemonSupervisor.stop()
        }
        #endif
        started = false
    }

    func applySettings() {
        settingsStore.settings = settingsStore.settings.normalized()
        try? credentialStore.saveToken(apiToken, account: settingsStore.settings.apiEndpoint.absoluteString)
        settingsStore.save()
        reloadClient()
        if started {
            if let apiClient {
                dashboard.startEventStream(from: apiClient)
            }
            startPolling()
        }
        Task {
            await dashboard.refreshDashboard()
            syncProfileRecoveryIssue()
        }
    }

    func refresh() {
        Task {
            await dashboard.refreshDashboard()
            syncProfileRecoveryIssue()
        }
    }

    func refreshNow() async {
        await dashboard.refreshDashboard()
        syncProfileRecoveryIssue()
    }

    func connectOrDisconnect() {
        Task {
            if dashboard.status.running {
                await dashboard.disconnect()
            } else {
                guard !syncProfileRecoveryIssue() else {
                    return
                }
                await dashboard.connect()
                syncProfileRecoveryIssue()
            }
        }
    }

    func selectProfile(_ profile: String) {
        Task {
            await dashboard.setActiveProfile(profile)
            syncProfileRecoveryIssue()
        }
    }

    @discardableResult
    func syncProfileRecoveryIssue(now: Date = Date()) -> Bool {
        let profile = dashboard.activeProfile
        guard !profile.isEmpty, let expiresAt = profileMetadata.expiration(for: profile) else {
            dashboard.clearRecoveryIssue(kind: .demoProfileExpired)
            return false
        }
        guard expiresAt <= now else {
            dashboard.clearRecoveryIssue(kind: .demoProfileExpired)
            return false
        }
        dashboard.setRecoveryIssue(TunnelRecoveryClassifier.expiredDemoProfile(profile: profile, expiresAt: expiresAt))
        return true
    }

    func performRecoveryAction(_ action: TunnelRecoveryAction) {
        switch action {
        case .retry:
            connectOrDisconnect()
        case .refresh:
            refresh()
        case .openAppSettings:
            #if os(iOS)
            if let url = URL(string: UIApplication.openSettingsURLString) {
                UIApplication.shared.open(url)
            }
            #endif
        case .rebuildVPNProfile:
            #if os(iOS)
            Task {
                do {
                    try await tunnelController.resetVPNProfile()
                    dashboard.clearRecoveryIssue(kind: .invalidEntitlementOrProfile)
                    await dashboard.refreshDashboard()
                    syncProfileRecoveryIssue()
                } catch {
                    dashboard.setRecoveryIssue(TunnelRecoveryClassifier.issue(for: error))
                }
            }
            #else
            refresh()
            #endif
        case .openProfiles, .importProfile:
            daemonMessage = action == .importProfile ? "open imports" : "open profiles"
        }
    }

    func createRule(_ rule: RulePayload) {
        Task {
            do {
                #if os(iOS)
                try replaceActiveProfileRules(dashboard.rules.rules + [rule])
                #else
                guard let apiClient else {
                    throw APIClientError.invalidURL("missing API client")
                }
                _ = try await apiClient.createRule(rule)
                #endif
                await dashboard.refreshDashboard()
                daemonMessage = "rule created"
            } catch {
                daemonMessage = error.localizedDescription
            }
        }
    }

    var pinnedConnectionIDs: Set<String> {
        Set(settingsStore.settings.pinnedConnectionIDs)
    }

    func isConnectionPinned(_ connection: TrafficConnectionPayload) -> Bool {
        pinnedConnectionIDs.contains(connection.connID)
    }

    func togglePinned(_ connection: TrafficConnectionPayload) {
        setConnection(connection, pinned: !isConnectionPinned(connection))
    }

    func setConnection(_ connection: TrafficConnectionPayload, pinned: Bool) {
        var ids = pinnedConnectionIDs
        if pinned {
            ids.insert(connection.connID)
        } else {
            ids.remove(connection.connID)
        }
        settingsStore.settings.pinnedConnectionIDs = ids.sorted()
    }

    func inspectionExportString(
        scope: String,
        connections: [TrafficConnectionPayload],
        logs: [String] = []
    ) -> String {
        InspectionExportBuilder.jsonString(
            scope: scope,
            traffic: dashboard.traffic,
            connections: connections,
            logs: logs
        )
    }

    #if os(iOS)
    func importReviewPayload(for item: InboxImportItem) throws -> TunnelImportReviewPayload {
        #if canImport(ClambhookMobile)
        let raw = try mobileString {
            MobileTunnelImportReviewJSON(item.decodedConfigText, $0)
        }
        return try JSONDecoder().decode(TunnelImportReviewPayload.self, from: Data(raw.utf8))
        #else
        return TunnelImportReviewPayload(
            activeProfile: item.preview.activeProfile,
            profiles: item.preview.profileNames.map {
                TunnelImportReviewProfile(name: $0, serverCount: item.preview.serverCount)
            }
        )
        #endif
    }

    func validateReviewedTunnelImport(_ request: ReviewedTunnelImportRequest) throws {
        #if canImport(ClambhookMobile)
        let data = try JSONEncoder().encode(request)
        guard let raw = String(data: data, encoding: .utf8) else {
            throw AppleAppModelError.invalidProfileRequest
        }
        try mobileBool {
            MobileValidateReviewedTunnelImportJSON(
                TunnelConfigStore.configURL(groupIdentifier: settingsStore.settings.appGroupIdentifier).path,
                raw,
                $0
            )
        }
        #endif
    }

    func applyReviewedTunnelImport(
        item: InboxImportItem,
        request: ReviewedTunnelImportRequest,
        tagsByProfile: [String: [String]]
    ) {
        do {
            #if canImport(ClambhookMobile)
            let data = try JSONEncoder().encode(request)
            guard let raw = String(data: data, encoding: .utf8) else {
                throw AppleAppModelError.invalidProfileRequest
            }
            try mobileBool {
                MobileApplyReviewedTunnelImportJSON(
                    TunnelConfigStore.configURL(groupIdentifier: settingsStore.settings.appGroupIdentifier).path,
                    raw,
                    $0
                )
            }
            #else
            try validateAndSaveTunnelConfig(item.decodedConfigText)
            #endif
            profileMetadata.setTagsByProfile(tagsByProfile)
            attention.removeInboxItem(id: item.id)
            applySettings()
            reloadTunnelConfiguration()
            daemonMessage = "imported reviewed profiles"
        } catch {
            attention.markInboxImportError(id: item.id, error: error.localizedDescription)
            daemonMessage = error.localizedDescription
        }
    }

    func importTunnelConfigText(_ rawText: String) throws {
        let text = try TunnelImportDecoder.decode(rawText)
        try validateAndSaveTunnelConfig(text)
        applySettings()
        reloadTunnelConfiguration()
    }

    func createTunnelProfile(_ request: TunnelProfileCreateRequest) throws {
        #if canImport(ClambhookMobile)
        let data = try JSONEncoder().encode(request)
        guard let raw = String(data: data, encoding: .utf8) else {
            throw AppleAppModelError.invalidProfileRequest
        }
        try mobileBool {
            MobileCreateTunnelProfileConfigJSON(
                TunnelConfigStore.configURL(groupIdentifier: settingsStore.settings.appGroupIdentifier).path,
                raw,
                $0
            )
        }
        applySettings()
        reloadTunnelConfiguration()
        #else
        throw AppleAppModelError.mobileConfigEditorUnavailable
        #endif
    }

    func replaceActiveProfileRules(_ rules: [RulePayload]) throws {
        #if canImport(ClambhookMobile)
        let data = try JSONEncoder().encode(rules)
        guard let raw = String(data: data, encoding: .utf8) else {
            throw AppleAppModelError.invalidRules
        }
        try mobileBool {
            MobileReplaceTunnelRulesJSON(
                TunnelConfigStore.configURL(groupIdentifier: settingsStore.settings.appGroupIdentifier).path,
                dashboard.activeProfile,
                raw,
                $0
            )
        }
        applySettings()
        reloadTunnelConfiguration()
        #else
        throw AppleAppModelError.mobileConfigEditorUnavailable
        #endif
    }

    func tunnelOnboardingReadinessMessage() -> String? {
        do {
            let text = try TunnelConfigStore.loadOrCreateConfig(groupIdentifier: settingsStore.settings.appGroupIdentifier)
            if TunnelConfigStore.isPlaceholderConfigText(text) {
                return "Replace the placeholder profile before continuing."
            }
            #if canImport(ClambhookMobile)
            try mobileBool {
                MobileValidateUsableTunnelConfig(
                    TunnelConfigStore.configURL(groupIdentifier: settingsStore.settings.appGroupIdentifier).path,
                    $0
                )
            }
            #else
            guard TunnelImportDecoder.looksLikeTOML(text), text.lowercased().contains("[[profile]]") else {
                return "Import or create a tunnel profile before continuing."
            }
            #endif
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    func shouldShowOnboarding() -> Bool {
        tunnelOnboardingReadinessMessage() != nil
    }

    func reloadTunnelConfiguration() {
        Task {
            do {
                try await (dashboardAPI as? TunnelDashboardClient)?.reloadConfiguration()
                await dashboard.refreshDashboard()
                syncProfileRecoveryIssue()
            } catch {
                dashboard.stopEventStream()
                await dashboard.refreshDashboard()
                syncProfileRecoveryIssue()
            }
        }
    }

    private func validateAndSaveTunnelConfig(_ text: String) throws {
        #if canImport(ClambhookMobile)
        let tempURL = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString)
            .appendingPathExtension("toml")
        try text.write(to: tempURL, atomically: true, encoding: .utf8)
        defer { try? FileManager.default.removeItem(at: tempURL) }
        try mobileBool { MobileValidateTunnelConfig(tempURL.path, $0) }
        #else
        guard TunnelImportDecoder.looksLikeTOML(text) else {
            throw TunnelImportError.unsupported
        }
        #endif
        try TunnelConfigStore.save(text, groupIdentifier: settingsStore.settings.appGroupIdentifier)
    }
    #endif

    #if os(macOS)
    func launchDaemon() {
        Task {
            do {
                daemonMessage = "daemon starting"
                try daemonSupervisor.launch(settings: settingsStore.settings, token: apiToken)
                let ready = await waitForAPIReady()
                daemonMessage = ready ? "daemon launched" : "daemon launched; waiting for API"
            } catch {
                daemonMessage = error.localizedDescription
            }
        }
    }

    func stopDaemon() {
        daemonSupervisor.stop()
        daemonMessage = "daemon stopped"
    }
    #endif

    private func reloadClient() {
        let settings = settingsStore.settings.normalized()
        let endpoint = settings.apiEndpoint
        let token = apiToken
        snapshotStore = FileSnapshotStore.appGroupStore(groupIdentifier: settings.appGroupIdentifier)
        #if os(iOS)
        apiClient = nil
        dashboardAPI = TunnelDashboardClient(controller: tunnelController)
        #else
        let nextAPIClient = ClambhookAPIClient(baseURL: endpoint, tokenProvider: { token.isEmpty ? nil : token })
        apiClient = nextAPIClient
        dashboardAPI = nextAPIClient
        #endif
        dashboard.stopEventStream()
        dashboard = DashboardStore(api: dashboardAPI, snapshotStore: snapshotStore, logRetention: settings.logRetention)
        attention = AttentionStore.appGroupStore(groupIdentifier: settings.appGroupIdentifier)
        profileMetadata = ProfileMetadataStore.appGroupStore(groupIdentifier: settings.appGroupIdentifier)
        bindDashboardStore()
        bindAttentionStore()
        bindProfileMetadataStore()
    }

    private func bindChildStores() {
        bindDashboardStore()
        bindAttentionStore()
        bindProfileMetadataStore()
        settingsChangeCancellable = settingsStore.objectWillChange.sink { [weak self] _ in
            Task { @MainActor in
                self?.objectWillChange.send()
            }
        }
    }

    private func bindAttentionStore() {
        attentionChangeCancellable = attention.objectWillChange.sink { [weak self] _ in
            Task { @MainActor in
                self?.objectWillChange.send()
            }
        }
    }

    private func bindProfileMetadataStore() {
        profileMetadataChangeCancellable = profileMetadata.objectWillChange.sink { [weak self] _ in
            Task { @MainActor in
                self?.objectWillChange.send()
            }
        }
    }

    private func bindDashboardStore() {
        dashboardChangeCancellable = dashboard.objectWillChange.sink { [weak self] _ in
            Task { @MainActor in
                self?.objectWillChange.send()
            }
        }
    }

    private func startPolling() {
        pollingTask?.cancel()
        let interval = settingsStore.settings.normalized().refreshIntervalSeconds
        let nanoseconds = UInt64(interval * 1_000_000_000)
        pollingTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: nanoseconds)
                if Task.isCancelled {
                    break
                }
                await self?.dashboard.refreshStatus()
                await MainActor.run {
                    _ = self?.syncProfileRecoveryIssue()
                }
            }
        }
    }

    #if os(macOS)
    private func waitForAPIReady(timeout: TimeInterval = 3) async -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            await dashboard.refreshStatus()
            if dashboard.apiOnline {
                return true
            }
            try? await Task.sleep(nanoseconds: 250_000_000)
        }
        return false
    }
    #endif
}

enum AppPlatform {
    case macOS
    case iOS
    case visionOS
}

enum AppleAppModelError: Error, LocalizedError {
    case mobileConfigEditorUnavailable
    case invalidProfileRequest
    case invalidRules

    var errorDescription: String? {
        switch self {
        case .mobileConfigEditorUnavailable:
            return "The embedded mobile config editor is unavailable in this build."
        case .invalidProfileRequest:
            return "The profile request could not be encoded."
        case .invalidRules:
            return "The rule changes could not be encoded."
        }
    }
}

private func defaultCredentialStore() -> CredentialStoring {
    #if canImport(Security)
    return KeychainCredentialStore()
    #else
    return InMemoryCredentialStore()
    #endif
}
