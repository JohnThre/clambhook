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
        self.dashboard = DashboardStore(api: apiClient, snapshotStore: snapshotStore)
    }

    func start() {
        reloadClient()
        #if os(macOS)
        if settingsStore.settings.launchDaemonOnStart {
            launchDaemon()
        }
        #endif
        dashboard.startEventStream(from: apiClient)
        Task { await dashboard.refreshDashboard() }
    }

    func stop() {
        dashboard.stopEventStream()
        #if os(macOS)
        if settingsStore.settings.stopDaemonOnQuit {
            daemonSupervisor.stop()
        }
        #endif
    }

    func applySettings() {
        try? credentialStore.saveToken(apiToken, account: settingsStore.settings.apiEndpoint.absoluteString)
        settingsStore.save()
        reloadClient()
        dashboard.startEventStream(from: apiClient)
        Task { await dashboard.refreshDashboard() }
    }

    func refresh() {
        Task { await dashboard.refreshDashboard() }
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
        do {
            try daemonSupervisor.launch(settings: settingsStore.settings, token: apiToken)
            daemonMessage = "daemon launched"
        } catch {
            daemonMessage = error.localizedDescription
        }
    }

    func stopDaemon() {
        daemonSupervisor.stop()
        daemonMessage = "daemon stopped"
    }
    #endif

    private func reloadClient() {
        let endpoint = settingsStore.settings.apiEndpoint
        let token = apiToken
        snapshotStore = FileSnapshotStore.appGroupStore(groupIdentifier: settingsStore.settings.appGroupIdentifier)
        apiClient = ClambhookAPIClient(baseURL: endpoint, tokenProvider: { token.isEmpty ? nil : token })
        dashboard.stopEventStream()
        dashboard = DashboardStore(api: apiClient, snapshotStore: snapshotStore)
    }
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
