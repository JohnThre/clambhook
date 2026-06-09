import ClambhookShared
import SwiftUI

struct IOSActivityView: View {
    @ObservedObject var model: AppleAppModel
    var logbookOnly = false
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass
    @State private var mode: IOSActivityMode = .connections
    @State private var connectionFilter: IOSTrafficFilter = .all
    @State private var logFilter: IOSActivityLogFilter = .all
    @State private var searchText = ""
    @State private var pendingCleanup: TrafficCleanupSuggestionPayload?
    @State private var selectedConnectionID: String?

    var body: some View {
        Group {
            if horizontalSizeClass == .regular, mode == .connections {
                regularConnectionLayout
            } else {
                compactActivityLayout
            }
        }
        .background(Color(.systemGroupedBackground))
        .searchable(text: $searchText, prompt: mode.searchPrompt)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                ShareLink(
                    item: activityExportString,
                    subject: Text("ClambHook inspection export"),
                    message: Text("Redacted metadata-only JSON export.")
                ) {
                    Label("Export", systemImage: "square.and.arrow.up")
                }
            }
        }
        .refreshable {
            await model.refreshNow()
        }
        .confirmationDialog(
            "Apply Rule Cleanup",
            isPresented: Binding(
                get: { pendingCleanup != nil },
                set: { if !$0 { pendingCleanup = nil } }
            ),
            presenting: pendingCleanup
        ) { suggestion in
            Button(cleanupActionTitle(suggestion), role: suggestion.operation == "delete_rule" ? .destructive : nil) {
                model.applyCleanupSuggestion(suggestion)
                pendingCleanup = nil
            }
        } message: { suggestion in
            Text(suggestion.message)
        }
    }

    private var compactActivityLayout: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 12) {
            IOSSurfaceSection(logbookOnly ? "History" : "Now") {
                IOSTrafficSummaryView(traffic: model.dashboard.traffic)
            }

                IOSSurfaceSection("Filters", detail: mode.title) {
                    VStack(spacing: 8) {
                Picker("Activity", selection: $mode) {
                    ForEach(IOSActivityMode.allCases) { mode in
                        Text(mode.title).tag(mode)
                    }
                }
                .pickerStyle(.segmented)

                if mode == .connections {
                    Picker("Connection Filter", selection: $connectionFilter) {
                        ForEach(IOSTrafficFilter.cases(logbookOnly: logbookOnly)) { filter in
                            Text(filter.title).tag(filter)
                        }
                    }
                    .pickerStyle(.menu)
                } else {
                    Picker("Log Filter", selection: $logFilter) {
                        ForEach(IOSActivityLogFilter.allCases) { filter in
                            Text(filter.title).tag(filter)
                        }
                    }
                    .pickerStyle(.segmented)
                }
            }
                }

            if mode == .connections {
                    IOSSurfaceSection("Connections", detail: "\(filteredConnections.count) rows") {
                    if filteredConnections.isEmpty {
                        ContentUnavailableView(
                            "No matching activity",
                            systemImage: "waveform.path.ecg",
                            description: Text("Connection decisions appear here.")
                        )
                    } else {
                            VStack(spacing: 8) {
                                ForEach(filteredConnections) { connection in
                                    NavigationLink {
                                        IOSActivityConnectionDetailView(model: model, connection: connection)
                                    } label: {
                                        IOSActivityConnectionRow(
                                            connection: connection,
                                            pinned: model.isConnectionPinned(connection),
                                            onTogglePin: { model.togglePinned(connection) }
                                        )
                                    }
                                    .buttonStyle(.plain)
                                }
                            }
                        }
                    }
                if !model.dashboard.ruleHitSummaries.isEmpty {
                        IOSSurfaceSection("Rule Hits", detail: "\(model.dashboard.ruleHitSummaries.count) rules") {
                            VStack(spacing: 6) {
                                ForEach(model.dashboard.ruleHitSummaries.prefix(8)) { hit in
                                    HStack {
                                        IOSActionChip(action: hit.action)
                                        Text(hit.ruleName.isEmpty ? "Default" : hit.ruleName)
                                            .font(.caption.weight(.semibold))
                                            .lineLimit(1)
                                        Spacer()
                                        Text("\(hit.count)")
                                            .font(.caption.monospacedDigit())
                                            .foregroundStyle(.secondary)
                                    }
                                }
                            }
                        }
                    }
                if !model.dashboard.traffic.blockDecisions.isEmpty {
                        IOSSurfaceSection("Blocked", detail: "\(model.dashboard.traffic.blockDecisions.count) recent") {
                            VStack(spacing: 6) {
                                ForEach(model.dashboard.traffic.blockDecisions.prefix(8)) { decision in
                                    HStack(alignment: .top, spacing: 10) {
                                        IOSActionChip(action: decision.action)
                                        VStack(alignment: .leading, spacing: 2) {
                                            Text(emptyDash(decision.targetHost.isEmpty ? decision.target : decision.targetHost))
                                                .font(.caption.weight(.semibold))
                                                .lineLimit(1)
                                            Text([decision.profile, decision.ruleName, decision.network].filter { !$0.isEmpty }.joined(separator: " / "))
                                                .font(.caption2)
                                                .foregroundStyle(.secondary)
                                                .lineLimit(1)
                                        }
                                        Spacer(minLength: 0)
                                    }
                                }
                            }
                        }
                    }
                if !model.dashboard.traffic.temporaryRules.isEmpty {
                        IOSSurfaceSection("Temporary Rules", detail: "\(model.dashboard.traffic.temporaryRules.count) active") {
                            VStack(spacing: 6) {
                                ForEach(model.dashboard.traffic.temporaryRules.prefix(6)) { rule in
                                    HStack(alignment: .top, spacing: 10) {
                                        IOSActionChip(action: rule.rule.action)
                                        VStack(alignment: .leading, spacing: 2) {
                                            Text(rule.rule.name.isEmpty ? "Temporary rule" : rule.rule.name)
                                                .font(.caption.weight(.semibold))
                                                .lineLimit(1)
                                            Text([rule.profile, rule.sourceTargetHost, rule.rule.displayMatch].filter { !$0.isEmpty }.joined(separator: " / "))
                                                .font(.caption2)
                                                .foregroundStyle(.secondary)
                                                .lineLimit(1)
                                        }
                                        Spacer(minLength: 0)
                                    }
                                }
                            }
                        }
                    }
                if !model.dashboard.traffic.destinationGroups.isEmpty {
                        IOSSurfaceSection("Noisy Domains", detail: "\(model.dashboard.traffic.destinationGroups.count) groups") {
                            VStack(spacing: 6) {
                                ForEach(model.dashboard.traffic.destinationGroups.prefix(8)) { group in
                                    HStack(alignment: .top, spacing: 10) {
                                        Text("\(group.count)")
                                            .font(.caption.monospacedDigit().weight(.semibold))
                                            .foregroundStyle(.secondary)
                                            .frame(width: 34, alignment: .trailing)
                                        VStack(alignment: .leading, spacing: 2) {
                                            Text(group.domainSuffix.isEmpty ? group.displayHost : group.domainSuffix)
                                                .font(.caption.weight(.semibold))
                                                .lineLimit(1)
                                            Text([group.actions.joined(separator: ","), group.topRuleName, group.topChainName].filter { !$0.isEmpty }.joined(separator: " / "))
                                                .font(.caption2)
                                                .foregroundStyle(.secondary)
                                                .lineLimit(1)
                                        }
                                        Spacer(minLength: 0)
                                    }
                                }
                            }
                        }
                    }
                if !model.dashboard.traffic.cleanupSuggestions.isEmpty {
                        IOSSurfaceSection("Rule Cleanup", detail: "\(model.dashboard.traffic.cleanupSuggestions.count) suggestions") {
                            VStack(spacing: 8) {
                                ForEach(model.dashboard.traffic.cleanupSuggestions.prefix(6)) { suggestion in
                                    HStack(alignment: .top, spacing: 10) {
                                        VStack(alignment: .leading, spacing: 3) {
                                            Text(cleanupTargetName(suggestion))
                                                .font(.caption.weight(.semibold))
                                                .lineLimit(1)
                                            Text(suggestion.message)
                                                .font(.caption2)
                                                .foregroundStyle(.secondary)
                                                .lineLimit(2)
                                        }
                                        Spacer(minLength: 8)
                                        Button(cleanupActionTitle(suggestion)) {
                                            pendingCleanup = suggestion
                                        }
                                        .buttonStyle(.bordered)
                                        .controlSize(.small)
                                        .disabled(suggestion.operation.isEmpty)
                                    }
                                }
                            }
                        }
                    }
            } else {
                    IOSSurfaceSection("Logs", detail: "\(filteredLogs.count) lines") {
                    if filteredLogs.isEmpty {
                        ContentUnavailableView(
                            "No matching logs",
                            systemImage: "doc.text.magnifyingglass",
                            description: Text("Recent events appear here.")
                        )
                    } else {
                            VStack(spacing: 6) {
                                ForEach(Array(filteredLogs.enumerated()), id: \.offset) { _, line in
                                    IOSActivityLogLineRow(line: line)
                                }
                            }
                        }
                    }
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
        }
    }

    private var regularConnectionLayout: some View {
        HStack(spacing: 0) {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 12) {
                    IOSSurfaceSection(logbookOnly ? "History" : "Now") {
                        IOSTrafficSummaryView(traffic: model.dashboard.traffic)
                    }

                    IOSSurfaceSection("Filters", detail: connectionFilter.title) {
                        VStack(spacing: 8) {
                            Picker("Activity", selection: $mode) {
                                ForEach(IOSActivityMode.allCases) { mode in
                                    Text(mode.title).tag(mode)
                                }
                            }
                            .pickerStyle(.segmented)

                            Picker("Connection Filter", selection: $connectionFilter) {
                                ForEach(IOSTrafficFilter.cases(logbookOnly: logbookOnly)) { filter in
                                    Text(filter.title).tag(filter)
                                }
                            }
                            .pickerStyle(.menu)
                        }
                    }

                    IOSSurfaceSection("Connections", detail: "\(filteredConnections.count) rows") {
                        if filteredConnections.isEmpty {
                            ContentUnavailableView(
                                "No matching activity",
                                systemImage: "waveform.path.ecg",
                                description: Text("Connection decisions appear here.")
                            )
                        } else {
                            VStack(spacing: 8) {
                                ForEach(filteredConnections) { connection in
                                    IOSActivityConnectionRow(
                                        connection: connection,
                                        pinned: model.isConnectionPinned(connection),
                                        selected: selectedConnection?.connID == connection.connID,
                                        onTogglePin: { model.togglePinned(connection) }
                                    )
                                    .contentShape(Rectangle())
                                    .onTapGesture {
                                        selectedConnectionID = connection.connID
                                    }
                                }
                            }
                        }
                    }
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 12)
            }
            .frame(minWidth: 340, idealWidth: 400, maxWidth: 460)

            Divider()

            NavigationStack {
                if let connection = selectedConnection {
                    IOSActivityConnectionDetailView(model: model, connection: connection)
                } else {
                    IOSInspectionPlaceholderView(
                        title: "Select a Connection",
                        message: "Connection route metadata appears here.",
                        systemImage: "waveform.path.ecg"
                    )
                }
            }
        }
    }

    private var filteredConnections: [TrafficConnectionPayload] {
        let rows = model.dashboard.traffic.inspectionConnections(
            filter: connectionFilter.inspectionKind,
            query: searchText,
            pinnedIDs: model.pinnedConnectionIDs
        )
        guard logbookOnly else {
            return rows
        }
        return rows.filter { $0.state.lowercased() == "closed" }
    }

    private var selectedConnection: TrafficConnectionPayload? {
        if let selectedConnectionID,
           let connection = filteredConnections.first(where: { $0.connID == selectedConnectionID }) {
            return connection
        }
        return filteredConnections.first
    }

    private var filteredLogs: [String] {
        let query = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return model.dashboard.logs.filter { line in
            logFilter.matches(line) && (query.isEmpty || line.lowercased().contains(query))
        }
    }

    private var activityExportString: String {
        switch mode {
        case .connections:
            return model.inspectionExportString(
                scope: logbookOnly ? "logbook.connections.filtered" : "activity.connections.filtered",
                connections: filteredConnections,
                logs: model.dashboard.logs
            )
        case .logs:
            return model.inspectionExportString(
                scope: logbookOnly ? "logbook.logs.filtered" : "activity.logs.filtered",
                connections: [],
                logs: filteredLogs
            )
        }
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
            HStack(spacing: 8) {
                IOSActionChip(action: "proxy")
                Text("\(traffic.connections.filter { $0.actionFamily == "proxy" }.count)")
                IOSActionChip(action: "direct")
                Text("\(traffic.connections.filter { $0.actionFamily == "direct" }.count)")
                IOSActionChip(action: "block")
                Text("\(traffic.connections.filter { $0.actionFamily == "block" }.count)")
            }
            .font(.caption)

            if !traffic.summary.persistError.isEmpty {
                Label(traffic.summary.persistError, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.red)
                    .lineLimit(2)
            }
        }
    }
}

private func cleanupTargetName(_ suggestion: TrafficCleanupSuggestionPayload) -> String {
    suggestion.targetRuleName.isEmpty ? suggestion.ruleName : suggestion.targetRuleName
}

private func cleanupActionTitle(_ suggestion: TrafficCleanupSuggestionPayload) -> String {
    suggestion.operation == "move_rule_to_end" ? "Move to End" : "Delete"
}

private struct IOSActivityConnectionRow: View {
    var connection: TrafficConnectionPayload
    var pinned: Bool
    var selected = false
    var onTogglePin: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            IOSActionChip(action: connection.ruleAction.isEmpty ? "proxy" : connection.ruleAction)
            VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .firstTextBaseline) {
                    if pinned {
                        Image(systemName: "pin.fill")
                            .font(.caption)
                            .foregroundStyle(.yellow)
                    }
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
            Spacer(minLength: 8)
            Button(action: onTogglePin) {
                Image(systemName: pinned ? "pin.slash.fill" : "pin.fill")
                    .frame(width: 28, height: 28)
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
            .accessibilityLabel(pinned ? "Unpin connection" : "Pin connection")
        }
        .padding(10)
        .background(selected ? Color.accentColor.opacity(0.14) : Color(.tertiarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 7, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 7, style: .continuous)
                .stroke(selected ? Color.accentColor.opacity(0.45) : Color.clear, lineWidth: 1)
        )
    }
}

private struct IOSActivityConnectionDetailView: View {
    @ObservedObject var model: AppleAppModel
    var connection: TrafficConnectionPayload
    @State private var draftRule: RulePayload?
    @State private var sourceConnection: TrafficConnectionPayload?

    var body: some View {
        List {
            Section("Actions") {
                Button {
                    sourceConnection = connection
                    draftRule = connection.ruleDraft(actionOverride: "proxy")
                } label: {
                    Label("Allow Permanently", systemImage: "checkmark.circle")
                }
                .disabled(connection.ruleDraft(actionOverride: "proxy") == nil)

                Button(role: .destructive) {
                    sourceConnection = connection
                    draftRule = connection.ruleDraft(actionOverride: "block")
                } label: {
                    Label("Block Permanently", systemImage: "hand.raised")
                }
                .disabled(connection.ruleDraft(actionOverride: "block") == nil)

                Button {
                    model.createTemporaryRuleFromConnection(connection, action: connection.temporaryAllowAction)
                } label: {
                    Label("Allow Temporarily", systemImage: "timer")
                }
                .disabled(connection.connID.isEmpty)

                Button(role: .destructive) {
                    model.createTemporaryRuleFromConnection(connection, action: "block")
                } label: {
                    Label("Block Temporarily", systemImage: "timer")
                }
                .disabled(connection.connID.isEmpty)

                Button {
                    sourceConnection = connection
                    draftRule = connection.ruleDraft()
                } label: {
                    Label("Create Rule from Connection", systemImage: "plus.circle")
                }
                .disabled(connection.ruleDraft() == nil)
            }

            Section("Connection") {
                LabeledContent("ID", value: emptyDash(connection.connID))
                LabeledContent("Target", value: emptyDash(connection.target))
                LabeledContent("Profile", value: emptyDash(connection.profile))
                LabeledContent("State", value: emptyDash(connection.state).capitalized)
                LabeledContent("Network", value: emptyDash(connection.network))
                LabeledContent("Application", value: emptyDash(connection.application))
                LabeledContent("Pinned", value: model.isConnectionPinned(connection) ? "Yes" : "No")
                LabeledContent("Client", value: emptyDash(connection.clientAddr))
                LabeledContent("Listener", value: iosListenerDescription(connection.listener))
            }

            Section("Decision") {
                LabeledContent("Action", value: emptyDash(connection.ruleAction))
                LabeledContent("Rule", value: emptyDash(connection.ruleName))
                LabeledContent("Chain", value: emptyDash(connection.chainName))
                LabeledContent("Default", value: connection.isDefault ? "Yes" : "No")
                LabeledContent("Decision time", value: formatDurationNs(connection.decisionNs))
            }

            if let explanation = connection.explanation {
                Section("Why This Route") {
                    if !explanation.summary.isEmpty {
                        Text(explanation.summary)
                    }
                    LabeledContent("Source", value: emptyDash(explanation.source.replacingOccurrences(of: "_", with: " ").capitalized))
                    if !explanation.matcherKind.isEmpty || !explanation.matcherValue.isEmpty {
                        LabeledContent("Match", value: [explanation.matcherKind, explanation.matcherValue].filter { !$0.isEmpty }.joined(separator: " "))
                    }
                    if !explanation.policyGroup.isEmpty {
                        LabeledContent("Group", value: explanation.policyGroup)
                    }
                    if !explanation.finalChain.isEmpty {
                        LabeledContent("Route", value: explanation.finalChain)
                    } else if !explanation.defaultChain.isEmpty {
                        LabeledContent("Route", value: explanation.defaultChain)
                    }
                }
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

            if hasGeo {
                Section("Location") {
                    if !connection.geo.city.isEmpty {
                        LabeledContent("City", value: connection.geo.city)
                    }
                    if !connection.geo.country.isEmpty {
                        LabeledContent("Country", value: connection.geo.country)
                    }
                    if !connection.geo.countryCode.isEmpty {
                        LabeledContent("Code", value: connection.geo.countryCode)
                    }
                    if !connection.geoError.isEmpty {
                        LabeledContent("Geo Error", value: connection.geoError)
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
        .toolbar {
            ToolbarItemGroup(placement: .topBarTrailing) {
                Button {
                    model.togglePinned(connection)
                } label: {
                    Image(systemName: model.isConnectionPinned(connection) ? "pin.slash.fill" : "pin.fill")
                }
                ShareLink(
                    item: model.inspectionExportString(scope: "connection.\(connection.connID)", connections: [connection]),
                    subject: Text("ClambHook connection export"),
                    message: Text("Redacted metadata-only JSON export.")
                ) {
                    Image(systemName: "square.and.arrow.up")
                }
            }
        }
        .sheet(item: $draftRule) { rule in
            IOSRuleCreateSheet(model: model, initialRule: rule, sourceConnection: sourceConnection)
        }
    }

    private var hasGeo: Bool {
        !connection.geo.city.isEmpty ||
            !connection.geo.country.isEmpty ||
            !connection.geo.countryCode.isEmpty ||
            !connection.geoError.isEmpty
    }
}

struct IOSInspectionPlaceholderView: View {
    var title: String
    var message: String
    var systemImage: String

    var body: some View {
        ContentUnavailableView {
            Label(title, systemImage: systemImage)
        } description: {
            Text(message)
        }
        .navigationTitle("Details")
    }
}

private struct IOSRuleCreateSheet: View {
    @ObservedObject var model: AppleAppModel
    @Environment(\.dismiss) private var dismiss
    @State private var rule: RulePayload
    var sourceConnection: TrafficConnectionPayload?

    init(model: AppleAppModel, initialRule: RulePayload, sourceConnection: TrafficConnectionPayload? = nil) {
        self.model = model
        self.sourceConnection = sourceConnection
        self._rule = State(initialValue: initialRule)
    }

    var body: some View {
        NavigationStack {
            Form {
                TextField("Name", text: $rule.name)
                Picker("Action", selection: $rule.action) {
                    Text("Block").tag("block")
                    Text("Direct").tag("direct")
                    ForEach(model.dashboard.servers.chains, id: \.name) { chain in
                        Text("Proxy: \(chain.name)").tag("chain:\(chain.name)")
                    }
                }
                LabeledContent("Match", value: rule.domains.first ?? rule.cidrs.first ?? "--")
            }
            .navigationTitle("Create Rule")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") {
                        if let sourceConnection {
                            model.createRuleFromConnection(sourceConnection, rule: rule)
                        } else {
                            model.createRule(rule)
                        }
                        dismiss()
                    }
                    .disabled(rule.name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
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
    case pinned
    case http
    case https
    case blocked
    case direct
    case proxy

    var id: Self { self }

    static func cases(logbookOnly: Bool) -> [IOSTrafficFilter] {
        logbookOnly ? [.all, .pinned, .blocked, .direct, .proxy] : allCases
    }

    var title: String {
        switch self {
        case .all: return "All"
        case .active: return "Active"
        case .pinned: return "Pinned"
        case .http: return "HTTP"
        case .https: return "HTTPS"
        case .blocked: return "Block"
        case .direct: return "Direct"
        case .proxy: return "Proxy"
        }
    }

    var inspectionKind: InspectionFilterKind {
        switch self {
        case .all:
            return .all
        case .active:
            return .active
        case .pinned:
            return .pinned
        case .http:
            return .http
        case .https:
            return .https
        case .blocked:
            return .block
        case .direct:
            return .direct
        case .proxy:
            return .proxy
        }
    }
}
