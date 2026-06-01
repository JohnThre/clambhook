import ClambhookShared
import SwiftUI

struct IOSActivityView: View {
    @ObservedObject var model: AppleAppModel
    @State private var mode: IOSActivityMode = .connections
    @State private var connectionFilter: IOSTrafficFilter = .all
    @State private var logFilter: IOSActivityLogFilter = .all
    @State private var searchText = ""

    var body: some View {
        List {
            Section("Now") {
                IOSTrafficSummaryView(traffic: model.dashboard.traffic)
                    .listRowInsets(EdgeInsets(top: 10, leading: 16, bottom: 10, trailing: 16))
            }

            Section {
                Picker("Activity", selection: $mode) {
                    ForEach(IOSActivityMode.allCases) { mode in
                        Text(mode.title).tag(mode)
                    }
                }
                .pickerStyle(.segmented)

                if mode == .connections {
                    Picker("Connection Filter", selection: $connectionFilter) {
                        ForEach(IOSTrafficFilter.allCases) { filter in
                            Text(filter.title).tag(filter)
                        }
                    }
                    .pickerStyle(.segmented)
                } else {
                    Picker("Log Filter", selection: $logFilter) {
                        ForEach(IOSActivityLogFilter.allCases) { filter in
                            Text(filter.title).tag(filter)
                        }
                    }
                    .pickerStyle(.segmented)
                }
            }

            if mode == .connections {
                Section("Connections") {
                    if filteredConnections.isEmpty {
                        ContentUnavailableView(
                            "No matching activity",
                            systemImage: "waveform.path.ecg",
                            description: Text("Connection decisions appear here.")
                        )
                    } else {
                        ForEach(filteredConnections) { connection in
                            NavigationLink {
                                IOSActivityConnectionDetailView(connection: connection)
                            } label: {
                                IOSActivityConnectionRow(connection: connection)
                            }
                        }
                    }
                }
            } else {
                Section("Logs") {
                    if filteredLogs.isEmpty {
                        ContentUnavailableView(
                            "No matching logs",
                            systemImage: "doc.text.magnifyingglass",
                            description: Text("Recent events appear here.")
                        )
                    } else {
                        ForEach(Array(filteredLogs.enumerated()), id: \.offset) { _, line in
                            IOSActivityLogLineRow(line: line)
                        }
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .searchable(text: $searchText, prompt: mode.searchPrompt)
        .refreshable {
            await model.refreshNow()
        }
    }

    private var filteredConnections: [TrafficConnectionPayload] {
        let query = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return model.dashboard.traffic.connections.filter { connection in
            connectionFilter.matches(connection) && (query.isEmpty || connectionSearchFields(connection).contains { $0.lowercased().contains(query) })
        }
    }

    private var filteredLogs: [String] {
        let query = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return model.dashboard.logs.filter { line in
            logFilter.matches(line) && (query.isEmpty || line.lowercased().contains(query))
        }
    }

    private func connectionSearchFields(_ connection: TrafficConnectionPayload) -> [String] {
        [
            connection.target,
            connection.targetHost,
            connection.ruleName,
            connection.ruleAction,
            connection.chainName,
            connection.application,
            connection.displayVisibility,
            connection.network,
        ]
    }
}

private enum IOSActivityMode: String, CaseIterable, Identifiable {
    case connections
    case logs

    var id: Self { self }

    var title: String {
        switch self {
        case .connections:
            return "Connections"
        case .logs:
            return "Logs"
        }
    }

    var searchPrompt: String {
        switch self {
        case .connections:
            return "Search activity"
        case .logs:
            return "Search logs"
        }
    }
}

private enum IOSActivityLogFilter: String, CaseIterable, Identifiable {
    case all
    case errors
    case warnings

    var id: Self { self }

    var title: String {
        switch self {
        case .all:
            return "All"
        case .errors:
            return "Errors"
        case .warnings:
            return "Warn"
        }
    }

    func matches(_ line: String) -> Bool {
        let lower = line.lowercased()
        switch self {
        case .all:
            return true
        case .errors:
            return lower.contains("error") || lower.contains("failed")
        case .warnings:
            return lower.contains("warn")
        }
    }
}

private struct IOSActivityLogLineRow: View {
    var line: String

    var body: some View {
        Text(line)
            .font(.system(.caption, design: .monospaced))
            .foregroundStyle(tint)
            .textSelection(.enabled)
            .lineLimit(5)
    }

    private var tint: Color {
        let lower = line.lowercased()
        if lower.contains("error") || lower.contains("failed") {
            return .red
        }
        if lower.contains("warn") {
            return .orange
        }
        return .secondary
    }
}

private struct IOSTrafficSummaryView: View {
    var traffic: TrafficSnapshotPayload

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            IOSMetricsGrid(metrics: [
                IOSMetric(title: "Active", value: "\(traffic.summary.activeConnections)", systemImage: "bolt.horizontal.circle"),
                IOSMetric(title: "Down", value: formatRate(traffic.summary.rxBps), systemImage: "arrow.down"),
                IOSMetric(title: "Up", value: formatRate(traffic.summary.txBps), systemImage: "arrow.up"),
                IOSMetric(title: "Total", value: "\(formatBytes(traffic.summary.rxTotal)) / \(formatBytes(traffic.summary.txTotal))", systemImage: "sum"),
            ])

            if !traffic.summary.persistError.isEmpty {
                Label(traffic.summary.persistError, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.red)
                    .lineLimit(2)
            }
        }
    }
}

private struct IOSActivityConnectionRow: View {
    var connection: TrafficConnectionPayload

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            IOSActionChip(action: connection.ruleAction.isEmpty ? "proxy" : connection.ruleAction)
            VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .firstTextBaseline) {
                    Text(emptyDash(connection.target))
                        .font(.body.weight(.medium))
                        .lineLimit(1)
                    Spacer(minLength: 8)
                    Text(emptyDash(connection.state).capitalized)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Text([connection.displayVisibility, connection.ruleName, connection.chainName].filter { !$0.isEmpty }.joined(separator: " / "))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                Text("\(formatBytes(connection.rxTotal)) down / \(formatBytes(connection.txTotal)) up / \(formatDurationNs(connection.durationNs))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, 2)
    }
}

private struct IOSActivityConnectionDetailView: View {
    var connection: TrafficConnectionPayload

    var body: some View {
        List {
            Section("Connection") {
                LabeledContent("Target", value: emptyDash(connection.target))
                LabeledContent("State", value: emptyDash(connection.state).capitalized)
                LabeledContent("Network", value: emptyDash(connection.network))
                LabeledContent("Application", value: emptyDash(connection.application))
                LabeledContent("Client", value: emptyDash(connection.clientAddr))
                LabeledContent("Listener", value: iosListenerDescription(connection.listener))
            }

            Section("Decision") {
                LabeledContent("Action", value: emptyDash(connection.ruleAction))
                LabeledContent("Rule", value: emptyDash(connection.ruleName))
                LabeledContent("Chain", value: emptyDash(connection.chainName))
                LabeledContent("Decision time", value: formatDurationNs(connection.decisionNs))
            }

            if let visibility = connection.visibility {
                Section("Visibility") {
                    LabeledContent("Kind", value: emptyDash(visibility.kind))
                    if !visibility.method.isEmpty {
                        LabeledContent("Method", value: visibility.method)
                    }
                    if !visibility.host.isEmpty {
                        LabeledContent("Host", value: visibility.host)
                    }
                    if !visibility.port.isEmpty {
                        LabeledContent("Port", value: visibility.port)
                    }
                    if !visibility.path.isEmpty {
                        LabeledContent("Path", value: visibility.path)
                    }
                    if !visibility.queryType.isEmpty {
                        LabeledContent("Query", value: visibility.queryType)
                    }
                }
            }

            if !connection.timeline.isEmpty {
                Section("Timeline") {
                    ForEach(connection.timeline) { item in
                        IOSTimelineRow(item: item)
                    }
                }
            }

            if !connection.hops.isEmpty {
                Section("Hops") {
                    ForEach(Array(connection.hops.enumerated()), id: \.offset) { _, hop in
                        IOSHopRow(hop: hop)
                    }
                }
            }

            Section("Data") {
                LabeledContent("Down", value: formatBytes(connection.rxTotal))
                LabeledContent("Up", value: formatBytes(connection.txTotal))
                LabeledContent("Down rate", value: formatRate(connection.rxBps))
                LabeledContent("Up rate", value: formatRate(connection.txBps))
                LabeledContent("Duration", value: formatDurationNs(connection.durationNs))
                LabeledContent("Dial time", value: formatDurationNs(connection.totalDialNs))
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle(emptyDash(connection.targetHost))
        .navigationBarTitleDisplayMode(.inline)
    }
}

private struct IOSTimelineRow: View {
    var item: TrafficTimelinePayload

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: timelineIcon)
                .foregroundStyle(.secondary)
                .frame(width: 22)
            VStack(alignment: .leading, spacing: 3) {
                Text(item.title.isEmpty ? item.type : item.title)
                    .font(.body.weight(.medium))
                if !item.detail.isEmpty {
                    Text(item.detail)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(3)
                }
            }
        }
        .padding(.vertical, 2)
    }

    private var timelineIcon: String {
        if item.type.contains("rule") {
            return "arrow.triangle.branch"
        }
        if item.type.contains("hop") {
            return "point.3.connected.trianglepath.dotted"
        }
        if item.type.contains("closed") {
            return "xmark.circle"
        }
        return "circle"
    }
}

private struct IOSHopRow: View {
    var hop: TrafficHopPayload

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline) {
                Text(emptyDash(hop.name))
                    .font(.body.weight(.medium))
                    .lineLimit(1)
                Spacer(minLength: 8)
                Text(emptyDash(hop.state).capitalized)
                    .font(.caption)
                    .foregroundStyle(hop.error.isEmpty ? Color.secondary : Color.red)
            }
            Text("\(emptyDash(hop.protocol).uppercased()) / \(emptyDash(hop.address))")
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            if !hop.error.isEmpty {
                Text(hop.error)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .lineLimit(2)
            } else {
                Text(formatDurationNs(hop.elapsedNs))
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 2)
    }
}

private enum IOSTrafficFilter: String, CaseIterable, Identifiable {
    case all
    case active
    case blocked
    case direct
    case proxy

    var id: Self { self }

    var title: String {
        switch self {
        case .all: return "All"
        case .active: return "Active"
        case .blocked: return "Block"
        case .direct: return "Direct"
        case .proxy: return "Proxy"
        }
    }

    func matches(_ connection: TrafficConnectionPayload) -> Bool {
        switch self {
        case .all:
            return true
        case .active:
            return connection.state.lowercased() == "active"
        case .blocked:
            return connection.ruleAction.lowercased() == "block" || connection.ruleAction.lowercased() == "reject"
        case .direct:
            return connection.ruleAction.lowercased() == "direct"
        case .proxy:
            return connection.ruleAction.isEmpty || connection.ruleAction.lowercased() == "chain"
        }
    }
}
