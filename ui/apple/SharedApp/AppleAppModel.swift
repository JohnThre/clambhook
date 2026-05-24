import ClambhookShared
import Foundation
import SwiftUI

@MainActor
final class AppleAppModel: ObservableObject {
    @Published var settingsStore: AppSettingsStore
    @Published private(set) var dashboard: DashboardStore
    @Published var apiToken = ""
    @Published var daemonMessage = ""

    let platform: AppPlatform
    private let credentialStore: CredentialStoring
    private var apiClient: ClambhookAPIClient
    private var snapshotStore: FileSnapshotStore
    private var pollingTask: Task<Void, Never>?
    private var started = false

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
        self.apiClient = ClambhookAPIClient(baseURL: settingsStore.settings.apiEndpoint, tokenProvider: { initialToken })
        self.dashboard = DashboardStore(
            api: apiClient,
            snapshotStore: snapshotStore,
            logRetention: settingsStore.settings.logRetention
        )
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
        dashboard.startEventStream(from: apiClient)
        startPolling()
        Task { await dashboard.refreshDashboard() }
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
            dashboard.startEventStream(from: apiClient)
            startPolling()
        }
        Task { await dashboard.refreshDashboard() }
    }

    func refresh() {
        Task { await dashboard.refreshDashboard() }
    }

    func refreshNow() async {
        await dashboard.refreshDashboard()
    }

    func connectOrDisconnect() {
        Task {
            if dashboard.status.running {
                await dashboard.disconnect()
            } else {
                await dashboard.connect()
            }
        }
    }

    func selectProfile(_ profile: String) {
        Task { await dashboard.setActiveProfile(profile) }
    }

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
        apiClient = ClambhookAPIClient(baseURL: endpoint, tokenProvider: { token.isEmpty ? nil : token })
        dashboard.stopEventStream()
        dashboard = DashboardStore(api: apiClient, snapshotStore: snapshotStore, logRetention: settings.logRetention)
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
}

private func defaultCredentialStore() -> CredentialStoring {
    #if canImport(Security)
    return KeychainCredentialStore()
    #else
    return InMemoryCredentialStore()
    #endif
}
