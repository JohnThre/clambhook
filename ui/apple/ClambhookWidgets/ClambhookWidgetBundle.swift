import AppIntents
import ClambhookShared
import SwiftUI
import WidgetKit

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
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Label(entry.snapshot.running ? "Running" : "Stopped", systemImage: entry.snapshot.running ? "checkmark.circle.fill" : "pause.circle")
                    .font(.caption)
                    .foregroundStyle(entry.snapshot.running ? .green : .secondary)
                Spacer()
                Circle()
                    .fill(entry.snapshot.apiOnline ? .green : .red)
                    .frame(width: 8, height: 8)
            }
            Text(emptyDash(entry.snapshot.profile))
                .font(.headline)
                .lineLimit(1)
            HStack {
                VStack(alignment: .leading) {
                    Text("Rx \(formatRate(entry.snapshot.rxBps))")
                    Text("Tx \(formatRate(entry.snapshot.txBps))")
                }
                .font(.caption2)
                Spacer()
            }
            HStack {
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
            .font(.caption)
        }
        .containerBackground(.background, for: .widget)
    }
}

struct ConnectIntent: AppIntent {
    static var title: LocalizedStringResource = "Connect clambhook"

    func perform() async throws -> some IntentResult {
        try await WidgetEnvironment.client().connect()
        WidgetCenter.shared.reloadAllTimelines()
        return .result()
    }
}

struct DisconnectIntent: AppIntent {
    static var title: LocalizedStringResource = "Disconnect clambhook"

    func perform() async throws -> some IntentResult {
        try await WidgetEnvironment.client().disconnect()
        WidgetCenter.shared.reloadAllTimelines()
        return .result()
    }
}

struct NextProfileIntent: AppIntent {
    static var title: LocalizedStringResource = "Switch to next clambhook profile"

    func perform() async throws -> some IntentResult {
        let client = WidgetEnvironment.client()
        let payload = try await client.profiles()
        guard !payload.profiles.isEmpty else {
            return .result()
        }
        let active = payload.active
        let index = payload.profiles.firstIndex(of: active) ?? 0
        let next = payload.profiles[(index + 1) % payload.profiles.count]
        try await client.setActiveProfile(next)
        WidgetCenter.shared.reloadAllTimelines()
        return .result()
    }
}

enum WidgetEnvironment {
    static func snapshot() -> DashboardSnapshot {
        FileSnapshotStore.loadSync(fileURL: FileSnapshotStore.appGroupURL(groupIdentifier: settings().appGroupIdentifier))
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
