import ClambhookShared
import SwiftUI

struct IOSOperationsTrafficView: View {
    @ObservedObject var model: AppleAppModel
    @State private var filter: IOSTrafficFilter = .all
    @State private var searchText = ""

    var body: some View {
        List {
            Section("Summary") {
                IOSTrafficSummaryView(traffic: model.dashboard.traffic)
                    .listRowInsets(EdgeInsets(top: 10, leading: 16, bottom: 10, trailing: 16))
            }

            Section("Controls") {
                Picker("Traffic Filter", selection: $filter) {
                    ForEach(IOSTrafficFilter.allCases) { filter in
                        Text(filter.title).tag(filter)
                    }
                }
                .pickerStyle(.segmented)

                NavigationLink {
                    IOSRuleEditorView(model: model)
                } label: {
                    Label("Rule Editor", systemImage: "slider.horizontal.3")
                }
            }

            Section("Connections") {
                if filteredConnections.isEmpty {
                    ContentUnavailableView(
                        searchText.isEmpty ? "No matching traffic" : "No matching connections",
                        systemImage: "point.3.connected.trianglepath.dotted",
                        description: Text("Recent connection decisions and timelines will appear here.")
                    )
                } else {
                    ForEach(filteredConnections) { connection in
                        NavigationLink {
                            IOSOperationsConnectionDetailView(connection: connection)
                        } label: {
                            IOSOperationsConnectionRow(connection: connection)
                        }
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .searchable(text: $searchText, prompt: "Search host, rule, app, decision")
        .refreshable {
            await model.refreshNow()
        }
    }

    private var filteredConnections: [TrafficConnectionPayload] {
        let query = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return model.dashboard.traffic.connections.filter { connection in
            filter.matches(connection) && (query.isEmpty || connectionSearchFields(connection).contains { $0.lowercased().contains(query) })
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

private struct IOSOperationsConnectionRow: View {
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

private struct IOSOperationsConnectionDetailView: View {
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

            Section("Traffic") {
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
