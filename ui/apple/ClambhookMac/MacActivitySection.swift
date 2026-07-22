import AppKit
import ClambhookShared
import SwiftUI

// MARK: - Activity

struct MacActivitySection: View {
    @ObservedObject var model: AppleAppModel
    @State private var filterKind: InspectionFilterKind = .all
    @State private var searchQuery = ""
    @State private var selectedID: String?
    @State private var draftRule: RulePayload?
    @State private var sourceConnection: TrafficConnectionPayload?

    private var filteredConnections: [TrafficConnectionPayload] {
        model.dashboard.traffic.inspectionConnections(
            filter: filterKind,
            query: searchQuery,
            pinnedIDs: model.pinnedConnectionIDs
        )
    }

    private var selectedConnection: TrafficConnectionPayload? {
        guard let id = selectedID else { return nil }
        return filteredConnections.first { $0.connID == id }
            ?? model.dashboard.traffic.connections.first { $0.connID == id }
    }

    private var activeCount: Int {
        model.dashboard.traffic.connections.filter { $0.state.lowercased() == "active" }.count
    }

    var body: some View {
        HSplitView {
            connectionListPanel
                .frame(minWidth: 360)
            if let conn = selectedConnection {
                ActivityDetailPanel(
                    connection: conn,
                    fallbackChain: dashboardFallbackProxyChain(model.dashboard),
                    onTemporaryAction: { connection, action in
                        model.createTemporaryRuleFromConnection(connection, action: action)
                    },
                    onPermanentRule: { connection, rule in
                        sourceConnection = connection
                        draftRule = rule
                    }
                )
                .frame(minWidth: 280)
            }
        }
        .sheet(item: $draftRule) { rule in
            MacRuleCreateSheet(model: model, initialRule: rule, sourceConnection: sourceConnection)
        }
    }

    // MARK: - Connection list panel

    private var connectionListPanel: some View {
        VStack(spacing: 0) {
            headerBar
            Divider()
            connectionList
        }
    }

    private var headerBar: some View {
        VStack(spacing: 8) {
            HStack(spacing: 10) {
                Picker("Filter", selection: $filterKind) {
                    Text("All").tag(InspectionFilterKind.all)
                    Text("Active").tag(InspectionFilterKind.active)
                    Text("Proxy").tag(InspectionFilterKind.proxy)
                    Text("Direct").tag(InspectionFilterKind.direct)
                    Text("Block").tag(InspectionFilterKind.block)
                }
                .labelsHidden()
                .pickerStyle(.segmented)
                .frame(maxWidth: 360)
                Spacer()
                statsLabel
            }
            TextField("Search app, host, rule, chain…", text: $searchQuery)
                .textFieldStyle(.roundedBorder)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 10)
    }

    private var statsLabel: some View {
        HStack(spacing: 8) {
            if activeCount > 0 {
                Label("\(activeCount) active", systemImage: "circle.fill")
                    .foregroundStyle(.green)
                    .font(.caption.weight(.medium))
            }
            let summary = model.dashboard.traffic.summary
            if summary.rxBps > 0 || summary.txBps > 0 {
                Text("↓ \(formatRate(summary.rxBps))  ↑ \(formatRate(summary.txBps))")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var connectionList: some View {
        let connections = filteredConnections
        return Group {
            if connections.isEmpty {
                emptyState
            } else {
                List(connections, selection: $selectedID) { connection in
                    ActivityConnectionRow(
                        connection: connection,
                        attributedApp: model.attributedApplication(for: connection)
                    )
                        .tag(connection.connID)
                }
                .listStyle(.plain)
            }
        }
    }

    private var emptyState: some View {
        VStack(spacing: 8) {
            Spacer()
            Image(systemName: "antenna.radiowaves.left.and.right.slash")
                .font(.system(size: 36))
                .foregroundStyle(.quaternary)
            Text(searchQuery.isEmpty ? "No connections" : "No matches")
                .foregroundStyle(.secondary)
            Spacer()
        }
        .frame(maxWidth: .infinity)
    }
}

// MARK: - Activity connection row

private struct ActivityConnectionRow: View {
    var connection: TrafficConnectionPayload
    var attributedApp: String?

    private var appLabel: String {
        if let app = attributedApp, !app.isEmpty { return app }
        if !connection.application.isEmpty { return connection.application }
        return connection.listener.protocol.uppercased()
    }

    private var destinationLabel: String {
        let host = connection.targetHost.isEmpty ? connection.target : connection.targetHost
        if !connection.targetPort.isEmpty && connection.targetPort != "0" {
            return "\(host):\(connection.targetPort)"
        }
        return host
    }

    private var isActive: Bool { connection.state.lowercased() == "active" }

    var body: some View {
        HStack(spacing: 8) {
            ActivityDecisionBadge(actionFamily: connection.actionFamily, compact: true)
            VStack(alignment: .leading, spacing: 2) {
                Text(appLabel)
                    .font(.subheadline.weight(.medium))
                    .lineLimit(1)
                Text(emptyDash(destinationLabel))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            VStack(alignment: .trailing, spacing: 2) {
                if isActive {
                    HStack(spacing: 3) {
                        Circle()
                            .fill(Color.green)
                            .frame(width: 6, height: 6)
                        Text("active")
                            .font(.caption2)
                            .foregroundStyle(.green)
                    }
                    if connection.rxBps > 0 || connection.txBps > 0 {
                        Text("↓ \(formatRate(connection.rxBps))")
                            .font(.caption2.monospacedDigit())
                            .foregroundStyle(.secondary)
                    }
                } else {
                    Text(formatDurationNs(connection.durationNs))
                        .font(.caption2.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
            }
        }
        .padding(.vertical, 2)
    }
}

// MARK: - Activity decision badge

private struct ActivityDecisionBadge: View {
    var actionFamily: String
    var compact: Bool = false

    private var label: String {
        switch actionFamily {
        case "block": return "Block"
        case "direct": return "Direct"
        default: return "Proxy"
        }
    }

    private var icon: String {
        switch actionFamily {
        case "block": return "hand.raised.fill"
        case "direct": return "arrow.up.right"
        default: return "shield.lefthalf.filled"
        }
    }

    private var tint: Color {
        switch actionFamily {
        case "block": return .red
        case "direct": return .blue
        default: return .green
        }
    }

    var body: some View {
        if compact {
            Circle()
                .fill(tint)
                .frame(width: 8, height: 8)
        } else {
            Label(label, systemImage: icon)
                .font(.caption.weight(.semibold))
                .foregroundStyle(tint)
                .padding(.horizontal, 8)
                .padding(.vertical, 4)
                .background(tint.opacity(0.12), in: Capsule())
        }
    }
}

// MARK: - Activity detail panel

private struct ActivityDetailPanel: View {
    var connection: TrafficConnectionPayload
    var fallbackChain: String
    var onTemporaryAction: ((TrafficConnectionPayload, String) -> Void)?
    var onPermanentRule: ((TrafficConnectionPayload, RulePayload) -> Void)?

    private var isActive: Bool { connection.state.lowercased() == "active" }

    private var canCreateRule: Bool {
        !connection.connID.isEmpty && !connection.monitorHost.isEmpty
    }

    private var proxyAction: String {
        connection.temporaryProxyAction(fallbackChain: fallbackChain)
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                detailHeader
                Divider()
                attributionGroup
                decisionGroup
                if !connection.geo.country.isEmpty || !connection.geo.city.isEmpty {
                    geoGroup
                }
                bandwidthGroup
                if !connection.hops.isEmpty {
                    hopsGroup
                }
                if !connection.timeline.isEmpty {
                    timelineGroup
                }
                Divider()
                actionsGroup
            }
            .padding(16)
        }
        .background(Color(NSColor.controlBackgroundColor))
    }

    private var detailHeader: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .top, spacing: 8) {
                VStack(alignment: .leading, spacing: 3) {
                    Text(emptyDash(connection.targetHost.isEmpty ? connection.target : connection.targetHost))
                        .font(.title3.weight(.semibold))
                        .lineLimit(2)
                    if !connection.targetPort.isEmpty && connection.targetPort != "0" {
                        Text("Port \(connection.targetPort)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
                Spacer()
                ActivityDecisionBadge(actionFamily: connection.actionFamily, compact: false)
            }
            HStack(spacing: 8) {
                Label(
                    connection.network.uppercased().isEmpty ? "TCP" : connection.network.uppercased(),
                    systemImage: "network"
                )
                .font(.caption)
                .foregroundStyle(.secondary)
                if isActive {
                    Label("Active", systemImage: "circle.fill")
                        .font(.caption)
                        .foregroundStyle(.green)
                } else {
                    Label("Closed", systemImage: "circle")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private var attributionGroup: some View {
        GroupBox("Attribution") {
            VStack(alignment: .leading, spacing: 5) {
                if !connection.application.isEmpty {
                    LabeledContent("App", value: connection.application)
                }
                if !connection.source.isEmpty {
                    LabeledContent("Source", value: connection.source)
                }
                if !connection.clientAddr.isEmpty {
                    LabeledContent("Client", value: connection.clientAddr)
                }
                LabeledContent(
                    "Listener",
                    value: "\(connection.listener.protocol.uppercased()) \(connection.listener.addr)"
                )
                if !connection.profile.isEmpty {
                    LabeledContent("Profile", value: connection.profile)
                }
            }
            .font(.caption)
        }
    }

    private var decisionGroup: some View {
        GroupBox("Decision") {
            VStack(alignment: .leading, spacing: 5) {
                LabeledContent("Action") {
                    ActivityDecisionBadge(actionFamily: connection.actionFamily, compact: false)
                }
                if !connection.ruleName.isEmpty {
                    LabeledContent("Rule", value: connection.ruleName)
                }
                if !connection.ruleAction.isEmpty {
                    LabeledContent("Rule action", value: connection.ruleAction)
                }
                if !connection.chainName.isEmpty {
                    LabeledContent("Chain", value: connection.chainName)
                }
                if !connection.groupName.isEmpty {
                    LabeledContent("Group", value: connection.groupName)
                }
            }
            .font(.caption)
        }
    }

    private var geoGroup: some View {
        GroupBox("Geography") {
            VStack(alignment: .leading, spacing: 5) {
                if !connection.geo.country.isEmpty {
                    LabeledContent("Country") {
                        HStack(spacing: 4) {
                            Text(countryFlag(connection.geo.countryCode))
                            Text(connection.geo.country)
                        }
                    }
                }
                if !connection.geo.city.isEmpty {
                    LabeledContent("City", value: connection.geo.city)
                }
            }
            .font(.caption)
        }
    }

    private var bandwidthGroup: some View {
        GroupBox("Bandwidth") {
            VStack(alignment: .leading, spacing: 5) {
                LabeledContent("Downloaded", value: formatBytes(connection.rxTotal))
                LabeledContent("Uploaded", value: formatBytes(connection.txTotal))
                if isActive && (connection.rxBps > 0 || connection.txBps > 0) {
                    LabeledContent("Rate ↓ / ↑") {
                        Text("\(formatRate(connection.rxBps)) / \(formatRate(connection.txBps))")
                            .monospacedDigit()
                    }
                }
                LabeledContent("Duration", value: formatDurationNs(connection.durationNs))
            }
            .font(.caption)
        }
    }

    private var hopsGroup: some View {
        GroupBox("Proxy Hops") {
            VStack(alignment: .leading, spacing: 6) {
                ForEach(Array(connection.hops.enumerated()), id: \.offset) { idx, hop in
                    HStack(spacing: 8) {
                        Text("\(idx + 1)")
                            .font(.caption2.weight(.bold))
                            .foregroundStyle(.secondary)
                            .frame(width: 16, alignment: .center)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(hop.name.isEmpty ? hop.address : hop.name)
                                .font(.caption.weight(.medium))
                            Text(
                                [hop.`protocol`, hop.state, hop.error]
                                    .filter { !$0.isEmpty }.joined(separator: " · ")
                            )
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                        }
                        Spacer(minLength: 4)
                        if hop.elapsedNs > 0 {
                            Text(formatDurationNs(hop.elapsedNs))
                                .font(.caption2.monospacedDigit())
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }
            .font(.caption)
        }
    }

    private var timelineGroup: some View {
        GroupBox("Timeline") {
            VStack(alignment: .leading, spacing: 4) {
                ForEach(connection.timeline) { entry in
                    HStack(alignment: .top, spacing: 8) {
                        Text(entry.type)
                            .font(.caption2.weight(.semibold))
                            .foregroundStyle(.secondary)
                            .frame(width: 60, alignment: .leading)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(entry.title)
                                .font(.caption2)
                            if !entry.detail.isEmpty {
                                Text(entry.detail)
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
        }
    }

    private var actionsGroup: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Quick Actions")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
            HStack(spacing: 8) {
                Button("Allow") {
                    onTemporaryAction?(connection, "allow")
                }
                .disabled(!canCreateRule)
                Button("Block", role: .destructive) {
                    onTemporaryAction?(connection, "block")
                }
                .disabled(!canCreateRule)
                if !proxyAction.isEmpty {
                    Button("Proxy") {
                        onTemporaryAction?(connection, proxyAction)
                    }
                    .disabled(!canCreateRule)
                }
                Button("Create Rule…") {
                    if let rule = connection.ruleDraft() {
                        onPermanentRule?(connection, rule)
                    }
                }
                .disabled(connection.ruleDraft() == nil)
            }
            .buttonStyle(.borderless)
            .font(.caption)
        }
    }
}

@MainActor
func dashboardFallbackProxyChain(_ dashboard: DashboardStore) -> String {
    for group in dashboard.policyGroups.groups {
        if !group.selectedChain.isEmpty { return group.selectedChain }
        if !group.selected.isEmpty { return group.selected }
    }
    return dashboard.servers.chains.first?.name ?? ""
}

func timeAgoShort(_ startTsNs: Int64) -> String {
    guard startTsNs > 0 else { return "--" }
    let nowNs = Int64(Date().timeIntervalSince1970 * 1_000_000_000)
    let elapsed = max(0, nowNs - startTsNs)
    let secs = elapsed / 1_000_000_000
    if secs < 60 { return "\(secs)s ago" }
    let mins = secs / 60
    if mins < 60 { return "\(mins)m ago" }
    return "\(mins / 60)h ago"
}
