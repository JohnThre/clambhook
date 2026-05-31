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
        return .result()
        #else
        try await WidgetEnvironment.client().connect()
        WidgetCenter.shared.reloadAllTimelines()
        return .result()
        #endif
    }
}

struct DisconnectIntent: AppIntent {
    static var title: LocalizedStringResource = "Disconnect clambhook"

    func perform() async throws -> some IntentResult {
        #if os(iOS)
        return .result()
        #else
        try await WidgetEnvironment.client().disconnect()
        WidgetCenter.shared.reloadAllTimelines()
        return .result()
        #endif
    }
}

struct NextProfileIntent: AppIntent {
    static var title: LocalizedStringResource = "Switch to next clambhook profile"

    func perform() async throws -> some IntentResult {
        #if os(iOS)
        return .result()
        #else
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
        #endif
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
