import ClambhookShared
import Combine
import Foundation
import SwiftUI

@MainActor
final class AppleAppModel: ObservableObject {
    @Published var settingsStore: AppSettingsStore
    @Published private(set) var dashboard: DashboardStore
    @Published private(set) var attention: AttentionStore
    @Published private(set) var profileMetadata: ProfileMetadataStore
    @Published private(set) var developerStatus = DeveloperStatusPayload()
    @Published private(set) var developerEntries: [DeveloperEntryPayload] = []
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
    private var licenseChangeCancellable: AnyCancellable?
    private var started = false

    let daemonSupervisor = DaemonSupervisor()
    @Published private(set) var licenseManager: MacLicenseManager

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
        self.licenseManager = MacLicenseManager(
            defaults: UserDefaults(suiteName: settingsStore.settings.appGroupIdentifier) ?? .standard,
            credentialStore: KeychainCredentialStore(service: "org.jpfchang.clambhook.license"),
            licenseValidationEndpoint: settingsStore.settings.licenseValidationEndpoint
        )
        let initialToken = (try? credentialStore.readToken(account: settingsStore.settings.apiEndpoint.absoluteString)) ?? ""
        self.apiToken = initialToken
        let initialAPIClient = ClambhookAPIClient(baseURL: settingsStore.settings.apiEndpoint, tokenProvider: { initialToken })
        self.apiClient = initialAPIClient
        self.dashboardAPI = initialAPIClient
        self.dashboard = DashboardStore(
            api: dashboardAPI,
            snapshotStore: snapshotStore,
            logRetention: settingsStore.settings.logRetention
        )
        self.attention = AttentionStore.appGroupStore(groupIdentifier: settingsStore.settings.appGroupIdentifier)
        self.profileMetadata = ProfileMetadataStore.appGroupStore(groupIdentifier: settingsStore.settings.appGroupIdentifier)
        bindChildStores()
        if platform == .macOS {
            Task { @MainActor [weak self] in
                self?.start()
            }
        }
    }

    func start() {
        guard !started else {
            refresh()
            return
        }
        started = true
        licenseManager.start()
        reloadClient()
        if settingsStore.settings.launchDaemonOnStart {
            launchDaemon()
        }
        if let apiClient {
            dashboard.startEventStream(from: apiClient)
        }
        startPolling()
        Task {
            await dashboard.refreshDashboard()
            await refreshDeveloperCaptureNow()
            syncProfileRecoveryIssue()
            enforceLicenseState()
        }
    }

    func stop() {
        pollingTask?.cancel()
        pollingTask = nil
        dashboard.stopEventStream()
        if settingsStore.settings.stopDaemonOnQuit {
            daemonSupervisor.stop()
        }
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
            await refreshDeveloperCaptureNow()
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
        await refreshDeveloperCaptureNow()
        syncProfileRecoveryIssue()
    }

    func connectOrDisconnect() {
        Task {
            if dashboard.status.running {
                await dashboard.disconnect()
            } else {
                guard canUseLicensedFeature(.tunnelRouting) else {
                    daemonMessage = AppleAppModelError.licenseLocked.errorDescription ?? ""
                    return
                }
                guard !syncProfileRecoveryIssue() else {
                    return
                }
                await dashboard.connect()
                syncProfileRecoveryIssue()
            }
        }
    }

    func selectProfile(_ profile: String) {
        guard canUseLicensedFeature(.profileManagement) else {
            daemonMessage = AppleAppModelError.licenseLocked.errorDescription ?? ""
            return
        }
        Task {
            await dashboard.setActiveProfile(profile)
            syncProfileRecoveryIssue()
        }
    }

    func selectPolicyGroup(group: String, chain: String) {
        guard canUseLicensedFeature(.profileManagement) else {
            daemonMessage = AppleAppModelError.licenseLocked.errorDescription ?? ""
            return
        }
        Task {
            await dashboard.selectPolicyGroup(profile: dashboard.activeProfile, group: group, chain: chain)
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
            break
        case .rebuildVPNProfile:
            refresh()
        case .openProfiles, .importProfile:
            daemonMessage = action == .importProfile ? "open imports" : "open profiles"
        }
    }

    func createRule(_ rule: RulePayload) {
        guard canUseLicensedFeature(.routingRules) else {
            daemonMessage = AppleAppModelError.licenseLocked.errorDescription ?? ""
            return
        }
        Task {
            do {
                guard let apiClient else {
                    throw APIClientError.invalidURL("missing API client")
                }
                _ = try await apiClient.createRule(rule)
                await dashboard.refreshDashboard()
                daemonMessage = "rule created"
            } catch {
                daemonMessage = error.localizedDescription
            }
        }
    }

    func createRuleFromConnection(_ connection: TrafficConnectionPayload, rule: RulePayload) {
        guard canUseLicensedFeature(.routingRules) else {
            daemonMessage = AppleAppModelError.licenseLocked.errorDescription ?? ""
            return
        }
        Task {
            do {
                guard let apiClient else {
                    throw APIClientError.invalidURL("missing API client")
                }
                if connection.connID.isEmpty {
                    _ = try await apiClient.createRule(rule)
                } else {
                    _ = try await apiClient.createRuleFromConnection(
                        connID: connection.connID,
                        profile: connection.profile,
                        name: rule.name,
                        action: rule.action,
                        scope: "auto"
                    )
                }
                await dashboard.refreshDashboard()
                daemonMessage = "rule created"
            } catch {
                daemonMessage = error.localizedDescription
            }
        }
    }

    func createTemporaryRuleFromConnection(_ connection: TrafficConnectionPayload, action: String, ttlSeconds: Int = 900) {
        guard canUseLicensedFeature(.routingRules) else {
            daemonMessage = AppleAppModelError.licenseLocked.errorDescription ?? ""
            return
        }
        Task {
            do {
                guard !connection.connID.isEmpty else {
                    throw APIClientError.invalidURL("missing connection id")
                }
                guard let apiClient else {
                    throw APIClientError.invalidURL("missing API client")
                }
                _ = try await apiClient.createTemporaryRuleFromConnection(
                    connID: connection.connID,
                    profile: connection.profile,
                    action: action,
                    scope: "auto",
                    ttlSeconds: ttlSeconds
                )
                await dashboard.refreshDashboard()
                daemonMessage = "temporary rule created"
            } catch {
                daemonMessage = error.localizedDescription
            }
        }
    }

    func applyCleanupSuggestion(_ suggestion: TrafficCleanupSuggestionPayload) {
        guard canUseLicensedFeature(.routingRules) else {
            daemonMessage = AppleAppModelError.licenseLocked.errorDescription ?? ""
            return
        }
        Task {
            do {
                guard let apiClient else {
                    throw APIClientError.invalidURL("missing API client")
                }
                _ = try await apiClient.cleanupRule(suggestion)
                await dashboard.refreshDashboard()
                daemonMessage = "rule cleanup applied"
            } catch {
                daemonMessage = error.localizedDescription
            }
        }
    }

    func testRule(network: String, target: String) async throws -> RuleTestResponse {
        guard canUseLicensedFeature(.routingRules) else {
            throw AppleAppModelError.licenseLocked
        }
        return try await dashboardAPI.testRule(network: network, target: target, profile: dashboard.activeProfile)
    }

    var mobileLicenseDecision: MobileLicenseDecision {
        #if os(macOS)
        return licenseManager.decision
        #else
        return MobileLicenseEvaluator.evaluate(snapshot: MobileLicenseSnapshot(trialStartDate: Date()))
        #endif
    }

    func canUseLicensedFeature(_ featureID: MobileLicenseFeatureID) -> Bool {
        mobileLicenseDecision.canUseFeature(featureID)
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

    func refreshDeveloperCapture() {
        Task {
            await refreshDeveloperCaptureNow()
        }
    }

    func refreshDeveloperCaptureNow() async {
        guard let provider = dashboardAPI as? DeveloperCaptureProviding else {
            developerStatus = DeveloperStatusPayload()
            developerEntries = []
            return
        }
        do {
            developerStatus = try await provider.developerStatus()
            developerEntries = try await provider.developerEntries().entries
        } catch {
            developerStatus = DeveloperStatusPayload()
            developerEntries = []
            daemonMessage = error.localizedDescription
        }
    }

    func developerCAPEM() async throws -> String {
        guard let provider = dashboardAPI as? DeveloperCaptureProviding else {
            throw APIClientError.invalidURL("developer capture unavailable")
        }
        return try await provider.developerCAPEM()
    }

    func developerHAR() async throws -> String {
        guard let provider = dashboardAPI as? DeveloperCaptureProviding else {
            throw APIClientError.invalidURL("developer capture unavailable")
        }
        return try await provider.developerHAR()
    }

    func clearDeveloperEntries() {
        Task {
            guard let provider = dashboardAPI as? DeveloperCaptureProviding else {
                return
            }
            do {
                try await provider.clearDeveloperEntries()
                await refreshDeveloperCaptureNow()
            } catch {
                daemonMessage = error.localizedDescription
            }
        }
    }

    func refreshActiveProfileRuleSets() {
        guard canUseLicensedFeature(.routingRules) else {
            daemonMessage = AppleAppModelError.licenseLocked.errorDescription ?? ""
            return
        }
        Task {
            do {
                guard let apiClient else {
                    throw APIClientError.invalidURL("missing API client")
                }
                _ = try await apiClient.refreshRuleSets(profile: dashboard.activeProfile)
                await dashboard.refreshDashboard()
                daemonMessage = "rule sets refreshed"
            } catch {
                daemonMessage = error.localizedDescription
            }
        }
    }

    func replaceActiveProfilePolicyGroups(_ groups: [PolicyGroupPayload]) throws {
        guard canUseLicensedFeature(.routingRules) else {
            throw AppleAppModelError.licenseLocked
        }
        Task {
            do {
                guard let apiClient else {
                    throw APIClientError.invalidURL("missing API client")
                }
                _ = try await apiClient.replacePolicyGroups(groups, profile: dashboard.activeProfile)
                await dashboard.refreshDashboard()
            } catch {
                daemonMessage = error.localizedDescription
            }
        }
    }

    func replaceActiveProfileRuleSubscriptions(_ subscriptions: [RuleSubscriptionPayload]) throws {
        guard canUseLicensedFeature(.routingRules) else {
            throw AppleAppModelError.licenseLocked
        }
        Task {
            do {
                guard let apiClient else {
                    throw APIClientError.invalidURL("missing API client")
                }
                _ = try await apiClient.replaceRuleSubscriptions(subscriptions, profile: dashboard.activeProfile)
                await dashboard.refreshDashboard()
            } catch {
                daemonMessage = error.localizedDescription
            }
        }
    }

    func refreshActiveProfileRuleSubscriptions() {
        guard canUseLicensedFeature(.routingRules) else {
            daemonMessage = AppleAppModelError.licenseLocked.errorDescription ?? ""
            return
        }
        Task {
            do {
                guard let apiClient else {
                    throw APIClientError.invalidURL("missing API client")
                }
                _ = try await apiClient.refreshRuleSubscriptions(profile: dashboard.activeProfile)
                await dashboard.refreshDashboard()
                daemonMessage = "subscriptions refreshed"
            } catch {
                daemonMessage = error.localizedDescription
            }
        }
    }

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

    private func reloadClient() {
        let settings = settingsStore.settings.normalized()
        let endpoint = settings.apiEndpoint
        let token = apiToken
        snapshotStore = FileSnapshotStore.appGroupStore(groupIdentifier: settings.appGroupIdentifier)
        let nextAPIClient = ClambhookAPIClient(baseURL: endpoint, tokenProvider: { token.isEmpty ? nil : token })
        apiClient = nextAPIClient
        dashboardAPI = nextAPIClient
        dashboard.stopEventStream()
        dashboard = DashboardStore(api: dashboardAPI, snapshotStore: snapshotStore, logRetention: settings.logRetention)
        attention = AttentionStore.appGroupStore(groupIdentifier: settings.appGroupIdentifier)
        profileMetadata = ProfileMetadataStore.appGroupStore(groupIdentifier: settings.appGroupIdentifier)
        developerStatus = DeveloperStatusPayload()
        developerEntries = []
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
        #if os(macOS)
        bindLicenseManager()
        #endif
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

    #if os(macOS)
    private func bindLicenseManager() {
        licenseChangeCancellable = licenseManager.objectWillChange.sink { [weak self] _ in
            Task { @MainActor in
                self?.objectWillChange.send()
                self?.enforceLicenseState()
            }
        }
    }
    #endif

    private func enforceLicenseState() {
        #if os(macOS)
        licenseManager.refreshDecision()
        guard !licenseManager.decision.canUseApp, dashboard.status.running else {
            return
        }
        Task {
            await dashboard.disconnect()
        }
        #endif
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
                    self?.enforceLicenseState()
                }
            }
        }
    }

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
}

enum AppPlatform {
    case macOS
    case visionOS
}

enum AppleAppModelError: Error, LocalizedError {
    case mobileConfigEditorUnavailable
    case invalidProfileRequest
    case invalidRules
    case licenseLocked

    var errorDescription: String? {
        switch self {
        case .mobileConfigEditorUnavailable:
            return "The embedded mobile config editor is unavailable in this build."
        case .invalidProfileRequest:
            return "The profile request could not be encoded."
        case .invalidRules:
            return "The rule changes could not be encoded."
        case .licenseLocked:
            return "Free access has ended. Purchase or restore the lifetime unlock to keep using clambhook."
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
