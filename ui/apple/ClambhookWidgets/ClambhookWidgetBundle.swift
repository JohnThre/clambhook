import AppIntents
import ClambhookShared
import SwiftUI
import WidgetKit
#if os(iOS)
import NetworkExtension
#if !DEBUG && !canImport(ClambhookMobile)
#error("Mobile must be importable for iOS Release/App Store widget builds. Run make build-ios-mobile-xcframework before building the release app.")
#endif
#if canImport(ClambhookMobile)
import ClambhookMobile
#endif
#endif

@main
struct ClambhookWidgetBundle: WidgetBundle {
    var body: some Widget {
        ClambhookStatusWidget()
    }
}

struct ClambhookStatusWidget: Widget {
    let kind = "ClambhookStatusWidget"

    var body: some WidgetConfiguration {
        StaticConfiguration(kind: kind, provider: StatusTimelineProvider()) { entry in
            StatusWidgetView(entry: entry)
        }
        .configurationDisplayName("clambhook")
        .description("Daemon status, active profile, bandwidth, and quick actions.")
        .supportedFamilies([.systemSmall, .systemMedium])
    }
}

struct StatusEntry: TimelineEntry {
    var date: Date
    var snapshot: DashboardSnapshot
}

struct StatusTimelineProvider: TimelineProvider {
    func placeholder(in context: Context) -> StatusEntry {
        StatusEntry(date: Date(), snapshot: DashboardSnapshot(apiOnline: true, running: true, profile: "default", rxBps: 2048, txBps: 1024))
    }

    func getSnapshot(in context: Context, completion: @escaping (StatusEntry) -> Void) {
        completion(StatusEntry(date: Date(), snapshot: WidgetEnvironment.snapshot()))
    }

    func getTimeline(in context: Context, completion: @escaping (Timeline<StatusEntry>) -> Void) {
        let entry = StatusEntry(date: Date(), snapshot: WidgetEnvironment.snapshot())
        completion(Timeline(entries: [entry], policy: .after(Date().addingTimeInterval(5 * 60))))
    }
}

struct StatusWidgetView: View {
    var entry: StatusEntry
    @Environment(\.widgetFamily) private var family

    var body: some View {
        VStack(alignment: .leading, spacing: family == .systemSmall ? 7 : 9) {
            header
            profile
            metrics
            Spacer(minLength: 0)
            actions
        }
        .containerBackground(.background, for: .widget)
    }

    private var header: some View {
        HStack(spacing: 8) {
            WidgetStatusBadge(
                text: entry.snapshot.running ? "Running" : "Stopped",
                systemImage: entry.snapshot.running ? "checkmark.circle.fill" : "pause.circle",
                tint: entry.snapshot.running ? .green : .secondary
            )
            Spacer(minLength: 0)
            Circle()
                .fill(entry.snapshot.apiOnline ? .green : .red)
                .frame(width: 8, height: 8)
                .accessibilityLabel(entry.snapshot.apiOnline ? "API online" : "API offline")
        }
    }

    private var profile: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(emptyDash(entry.snapshot.profile))
                .font(.headline)
                .lineLimit(1)
                .minimumScaleFactor(0.8)
            if family == .systemMedium {
                Text("Updated \(entry.snapshot.updatedAt, style: .time)")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
    }

    @ViewBuilder
    private var metrics: some View {
        if family == .systemMedium {
            HStack(spacing: 8) {
                WidgetMetricTile(title: "Down", value: formatRate(entry.snapshot.rxBps), systemImage: "arrow.down")
                WidgetMetricTile(title: "Up", value: formatRate(entry.snapshot.txBps), systemImage: "arrow.up")
                WidgetMetricTile(title: "Active", value: "\(entry.snapshot.activeConnections)", systemImage: "bolt.horizontal.circle")
            }
        } else {
            VStack(alignment: .leading, spacing: 2) {
                WidgetMetricLine(title: "Down", value: formatRate(entry.snapshot.rxBps))
                WidgetMetricLine(title: "Up", value: formatRate(entry.snapshot.txBps))
                WidgetMetricLine(title: "Active", value: "\(entry.snapshot.activeConnections)")
            }
        }
    }

    private var actions: some View {
        HStack(spacing: 6) {
            if entry.snapshot.running {
                Button(intent: DisconnectIntent()) {
                    Label("Stop", systemImage: "stop.fill")
                }
            } else {
                Button(intent: ConnectIntent()) {
                    Label("Start", systemImage: "play.fill")
                }
            }
            if family == .systemMedium {
                Button(intent: NextProfileIntent()) {
                    Label("Next", systemImage: "arrow.right.circle")
                }
            }
        }
        .font(.caption.weight(.medium))
        .buttonStyle(.bordered)
    }
}

private struct WidgetStatusBadge: View {
    var text: String
    var systemImage: String
    var tint: Color

    var body: some View {
        Label(text, systemImage: systemImage)
            .font(.caption.weight(.medium))
            .lineLimit(1)
            .foregroundStyle(tint)
    }
}

private struct WidgetMetricTile: View {
    var title: String
    var value: String
    var systemImage: String

    var body: some View {
        HStack(spacing: 5) {
            Image(systemName: systemImage)
                .foregroundStyle(.secondary)
                .frame(width: 14)
            VStack(alignment: .leading, spacing: 1) {
                Text(title)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                Text(value)
                    .font(.caption.weight(.semibold))
                    .monospacedDigit()
                    .lineLimit(1)
                    .minimumScaleFactor(0.75)
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 7)
        .padding(.vertical, 6)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
    }
}

private struct WidgetMetricLine: View {
    var title: String
    var value: String

    var body: some View {
        HStack(spacing: 4) {
            Text(title)
                .foregroundStyle(.secondary)
            Text(value)
                .monospacedDigit()
                .lineLimit(1)
                .minimumScaleFactor(0.75)
        }
        .font(.caption2)
    }
}

struct ConnectIntent: AppIntent {
    static var title: LocalizedStringResource = "Connect clambhook"

    func perform() async throws -> some IntentResult {
        #if os(iOS)
        try await IOSTunnelWidgetClient().connect()
        #else
        let client = WidgetEnvironment.client()
        try await client.connect()
        await WidgetEnvironment.refreshSnapshot(from: client)
        #endif
        WidgetCenter.shared.reloadAllTimelines()
        return .result()
    }
}

struct DisconnectIntent: AppIntent {
    static var title: LocalizedStringResource = "Disconnect clambhook"

    func perform() async throws -> some IntentResult {
        #if os(iOS)
        try await IOSTunnelWidgetClient().disconnect()
        #else
        let client = WidgetEnvironment.client()
        try await client.disconnect()
        await WidgetEnvironment.refreshSnapshot(from: client)
        #endif
        WidgetCenter.shared.reloadAllTimelines()
        return .result()
    }
}

struct NextProfileIntent: AppIntent {
    static var title: LocalizedStringResource = "Switch to next clambhook profile"

    func perform() async throws -> some IntentResult {
        #if os(iOS)
        try await IOSTunnelWidgetClient().nextProfile()
        #else
        let client = WidgetEnvironment.client()
        let payload = try await client.profiles()
        guard !payload.profiles.isEmpty else {
            await WidgetEnvironment.refreshSnapshot(from: client)
            WidgetCenter.shared.reloadAllTimelines()
            return .result()
        }
        let active = payload.active
        let index = payload.profiles.firstIndex(of: active) ?? 0
        let next = payload.profiles[(index + 1) % payload.profiles.count]
        try await client.setActiveProfile(next)
        await WidgetEnvironment.refreshSnapshot(from: client)
        #endif
        WidgetCenter.shared.reloadAllTimelines()
        return .result()
    }
}

enum WidgetEnvironment {
    static func snapshot() -> DashboardSnapshot {
        FileSnapshotStore.loadSync(fileURL: snapshotURL())
    }

    static func snapshotURL() -> URL? {
        FileSnapshotStore.appGroupURL(groupIdentifier: settings().appGroupIdentifier)
    }

    static func snapshotStore() -> FileSnapshotStore {
        FileSnapshotStore.appGroupStore(groupIdentifier: settings().appGroupIdentifier)
    }

    static func saveSnapshot(_ snapshot: DashboardSnapshot) async {
        try? await snapshotStore().save(snapshot)
    }

    static func saveSnapshot(status: StatusPayload, profiles: ProfilesPayload, traffic: TrafficSnapshotPayload, apiOnline: Bool) async {
        let activeConnections = status.listeners.reduce(0) { $0 + $1.activeConns }
        await saveSnapshot(DashboardSnapshot(
            updatedAt: Date(),
            apiOnline: apiOnline,
            running: status.running,
            profile: profiles.active.isEmpty ? status.profile : profiles.active,
            listenerCount: status.listeners.count,
            activeConnections: max(activeConnections, traffic.summary.activeConnections),
            rxBps: traffic.summary.rxBps,
            txBps: traffic.summary.txBps,
            logs: snapshot().logs
        ))
    }

    static func saveSnapshot(dashboard: TunnelDashboardPayload, apiOnline: Bool = true) async {
        await saveSnapshot(status: dashboard.status, profiles: dashboard.profiles, traffic: dashboard.traffic, apiOnline: apiOnline)
    }

    static func refreshSnapshot(from client: ClambhookAPIClient) async {
        do {
            let status = try await client.status()
            let profiles = try await client.profiles()
            let traffic = try await client.traffic()
            await saveSnapshot(status: status, profiles: profiles, traffic: traffic, apiOnline: true)
        } catch {
            var current = snapshot()
            current.updatedAt = Date()
            current.apiOnline = false
            await saveSnapshot(current)
        }
    }

    static func client() -> ClambhookAPIClient {
        let settings = settings()
        let token = (try? KeychainCredentialStore().readToken(account: settings.apiEndpoint.absoluteString)) ?? ""
        return ClambhookAPIClient(baseURL: settings.apiEndpoint, tokenProvider: { token.isEmpty ? nil : token })
    }

    static func settings() -> AppSettings {
        let defaults = UserDefaults(suiteName: defaultAppGroupIdentifier) ?? .standard
        guard
            let data = defaults.data(forKey: "clambhook.apple.settings"),
            let settings = try? JSONDecoder().decode(AppSettings.self, from: data)
        else {
            return AppSettings()
        }
        return settings
    }
}

#if os(iOS)
private let widgetTunnelProviderBundleIdentifier = "org.jpfchang.clambhook.tunnel"

private final class IOSTunnelWidgetClient {
    private let decoder = JSONDecoder()
    private let encoder = JSONEncoder()

    private var groupIdentifier: String {
        WidgetEnvironment.settings().appGroupIdentifier
    }

    private var configURL: URL {
        TunnelConfigStore.configURL(groupIdentifier: groupIdentifier)
    }

    func connect() async throws {
        _ = try TunnelConfigStore.loadOrCreateConfig(groupIdentifier: groupIdentifier)
        let manager = try await configuredManager()
        guard let session = manager.connection as? NETunnelProviderSession else {
            throw IOSTunnelWidgetError.invalidSession
        }
        try session.startTunnel(options: [
            "configPath": configURL.path
        ])
        await saveCurrentSnapshot(manager: manager, assumedRunning: true)
    }

    func disconnect() async throws {
        guard let manager = try await loadManager(createIfMissing: false) else {
            await saveCurrentSnapshot(manager: nil, assumedRunning: false)
            return
        }
        guard let session = manager.connection as? NETunnelProviderSession else {
            throw IOSTunnelWidgetError.invalidSession
        }
        session.stopTunnel()
        await saveCurrentSnapshot(manager: manager, assumedRunning: false)
    }

    func nextProfile() async throws {
        guard let manager = try await loadManager(createIfMissing: false),
              canSendProviderMessage(status: manager.connection.status)
        else {
            try await nextProfileInConfig()
            return
        }

        let payload = try await dashboard(manager: manager)
        guard payload.profiles.profiles.count > 1 else {
            await WidgetEnvironment.saveSnapshot(dashboard: payload)
            return
        }

        let next = nextProfileName(in: payload.profiles)
        let data = try await send(.init(action: .setActiveProfile, profile: next), manager: manager)
        let updated = try decoder.decode(TunnelDashboardPayload.self, from: data)
        await WidgetEnvironment.saveSnapshot(dashboard: updated)
    }

    private func nextProfileInConfig() async throws {
        let payload = try disconnectedDashboard()
        guard payload.profiles.profiles.count > 1 else {
            await WidgetEnvironment.saveSnapshot(dashboard: payload)
            return
        }

        let next = nextProfileName(in: payload.profiles)
        #if canImport(ClambhookMobile)
        try mobileBool {
            MobileSetActiveTunnelProfileConfig(configURL.path, next, $0)
        }
        let updated = try disconnectedDashboard()
        await WidgetEnvironment.saveSnapshot(dashboard: updated)
        #else
        throw IOSTunnelWidgetError.mobileRuntimeUnavailable
        #endif
    }

    private func nextProfileName(in payload: ProfilesPayload) -> String {
        let active = payload.active
        let index = payload.profiles.firstIndex(of: active) ?? 0
        return payload.profiles[(index + 1) % payload.profiles.count]
    }

    private func dashboard(manager: NETunnelProviderManager) async throws -> TunnelDashboardPayload {
        if canSendProviderMessage(status: manager.connection.status) {
            let data = try await send(.init(action: .dashboard), manager: manager)
            return try decoder.decode(TunnelDashboardPayload.self, from: data)
        }
        return try disconnectedDashboard()
    }

    private func disconnectedDashboard() throws -> TunnelDashboardPayload {
        #if canImport(ClambhookMobile)
        _ = try TunnelConfigStore.loadOrCreateConfig(groupIdentifier: groupIdentifier)
        let json = try mobileString {
            MobileTunnelConfigDashboardJSON(configURL.path, $0)
        }
        return try decoder.decode(TunnelDashboardPayload.self, from: Data(json.utf8))
        #else
        throw IOSTunnelWidgetError.mobileRuntimeUnavailable
        #endif
    }

    private func saveCurrentSnapshot(manager: NETunnelProviderManager?, assumedRunning: Bool?) async {
        do {
            let payload = try await currentDashboard(manager: manager, assumedRunning: assumedRunning)
            await WidgetEnvironment.saveSnapshot(dashboard: payload)
        } catch {
            var current = WidgetEnvironment.snapshot()
            current.updatedAt = Date()
            current.apiOnline = false
            if let assumedRunning {
                current.running = assumedRunning
            }
            await WidgetEnvironment.saveSnapshot(current)
        }
    }

    private func currentDashboard(manager: NETunnelProviderManager?, assumedRunning: Bool?) async throws -> TunnelDashboardPayload {
        let payload: TunnelDashboardPayload
        if let manager {
            payload = try await dashboard(manager: manager)
        } else {
            payload = try disconnectedDashboard()
        }
        guard let assumedRunning else {
            return payload
        }
        var updated = payload
        updated.status.running = assumedRunning
        return updated
    }

    private func configuredManager() async throws -> NETunnelProviderManager {
        let manager = try await loadManager(createIfMissing: true) ?? NETunnelProviderManager()
        let proto = (manager.protocolConfiguration as? NETunnelProviderProtocol) ?? NETunnelProviderProtocol()
        proto.providerBundleIdentifier = widgetTunnelProviderBundleIdentifier
        proto.serverAddress = "clambhook"
        proto.providerConfiguration = [
            "configPath": configURL.path
        ]
        manager.localizedDescription = "clambhook"
        manager.protocolConfiguration = proto
        manager.isEnabled = true
        try await save(manager)
        try await reload(manager)
        return manager
    }

    private func loadManager(createIfMissing: Bool) async throws -> NETunnelProviderManager? {
        let managers = try await loadAllManagers()
        if let existing = managers.first(where: { manager in
            (manager.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == widgetTunnelProviderBundleIdentifier
        }) {
            return existing
        }
        return createIfMissing ? NETunnelProviderManager() : nil
    }

    private func loadAllManagers() async throws -> [NETunnelProviderManager] {
        try await withCheckedThrowingContinuation { continuation in
            NETunnelProviderManager.loadAllFromPreferences { managers, error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume(returning: managers ?? [])
                }
            }
        }
    }

    private func save(_ manager: NETunnelProviderManager) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            manager.saveToPreferences { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            }
        }
    }

    private func reload(_ manager: NETunnelProviderManager) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            manager.loadFromPreferences { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            }
        }
    }

    private func send(_ command: TunnelCommand, manager: NETunnelProviderManager) async throws -> Data {
        guard let session = manager.connection as? NETunnelProviderSession else {
            throw IOSTunnelWidgetError.invalidSession
        }
        let message = try encoder.encode(command)
        return try await withCheckedThrowingContinuation { continuation in
            do {
                try session.sendProviderMessage(message) { data in
                    if let data {
                        continuation.resume(returning: data)
                    } else {
                        continuation.resume(throwing: IOSTunnelWidgetError.emptyProviderResponse)
                    }
                }
            } catch {
                continuation.resume(throwing: error)
            }
        }
    }

    private func canSendProviderMessage(status: NEVPNStatus) -> Bool {
        status == .connected || status == .connecting || status == .reasserting
    }
}

private enum IOSTunnelWidgetError: Error, LocalizedError {
    case invalidSession
    case emptyProviderResponse
    case mobileRuntimeUnavailable

    var errorDescription: String? {
        switch self {
        case .invalidSession:
            return "packet tunnel session is unavailable"
        case .emptyProviderResponse:
            return "packet tunnel returned no response"
        case .mobileRuntimeUnavailable:
            return "embedded mobile runtime is unavailable"
        }
    }
}

#if canImport(ClambhookMobile)
private func mobileString(_ operation: (NSErrorPointer) -> String) throws -> String {
    var error: NSError?
    let value = operation(&error)
    if let error {
        throw error
    }
    return value
}

private func mobileBool(_ operation: (NSErrorPointer) -> Bool) throws {
    var error: NSError?
    if !operation(&error) {
        throw error ?? IOSTunnelWidgetError.mobileRuntimeUnavailable
    }
}
#endif
#endif
