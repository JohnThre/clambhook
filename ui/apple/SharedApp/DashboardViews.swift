import ClambhookShared
import SwiftUI

struct DashboardContentView: View {
    @ObservedObject var model: AppleAppModel
    @State private var pendingCleanup: TrafficCleanupSuggestionPayload?

    var body: some View {
        List {
            Section {
                StatusHeaderView(model: model)
            }
            Section("Profiles") {
                if model.dashboard.profiles.profiles.isEmpty {
                    Text("No profiles")
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(model.dashboard.profiles.profiles, id: \.self) { profile in
                        Button {
                            model.selectProfile(profile)
                        } label: {
                            HStack {
                                Text(profile)
                                Spacer()
                                if profile == model.dashboard.activeProfile {
                                    Image(systemName: "checkmark.circle.fill")
                                        .foregroundStyle(.green)
                                }
                            }
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
            Section("Listeners") {
                if model.dashboard.status.listeners.isEmpty {
                    Text("None active")
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(model.dashboard.status.listeners) { listener in
                        HStack {
                            Label(listener.protocol.uppercased(), systemImage: "antenna.radiowaves.left.and.right")
                            Spacer()
                            VStack(alignment: .trailing) {
                                Text(listener.addr)
                                Text("\(listener.activeConns) active")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
            Section("Policy") {
                CompactPolicySelectorView(
                    summary: model.dashboard.policySelectorSummary,
                    groups: model.dashboard.policyGroups.groups,
                    onSelect: { group, chain in
                        model.selectPolicyGroup(group: group, chain: chain)
                    }
                )
            }
            Section("Servers") {
                ServerListView(servers: model.dashboard.servers)
            }
            Section("Rules") {
                RuleListView(rules: model.dashboard.rules)
            }
            Section("Bandwidth") {
                let sample = model.dashboard.currentBandwidth
                LabeledContent("Rx", value: formatRate(sample.rxBps))
                LabeledContent("Tx", value: formatRate(sample.txBps))
            }
            Section("Traffic") {
                TrafficSummaryView(traffic: model.dashboard.traffic)
                TrafficListView(
                    connections: model.dashboard.traffic.connections,
                    fallbackChain: dashboardFallbackProxyChain(model.dashboard),
                    onTemporaryAction: temporaryRuleActionHandler,
                    onPermanentRule: { connection, rule in
                        model.createRuleFromConnection(connection, rule: rule)
                    }
                )
            }
            if !model.dashboard.traffic.ruleHits.isEmpty {
                Section("Rule Hits") {
                    ForEach(model.dashboard.traffic.ruleHits.prefix(8)) { hit in
                        LabeledContent(hit.ruleName.isEmpty ? "Default" : hit.ruleName, value: "\(hit.count)")
                    }
                }
            }
            if !model.dashboard.traffic.blockDecisions.isEmpty {
                Section("Blocked") {
                    ForEach(model.dashboard.traffic.blockDecisions.prefix(6)) { decision in
                        VStack(alignment: .leading, spacing: 3) {
                            Text(emptyDash(decision.targetHost.isEmpty ? decision.target : decision.targetHost))
                                .fontWeight(.medium)
                            Text([decision.profile, decision.ruleName, decision.action].filter { !$0.isEmpty }.joined(separator: " · "))
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }
            if !model.dashboard.traffic.cleanupSuggestions.isEmpty {
                Section("Rule Cleanup") {
                    ForEach(model.dashboard.traffic.cleanupSuggestions.prefix(5)) { suggestion in
                        HStack(alignment: .top, spacing: 12) {
                            VStack(alignment: .leading, spacing: 3) {
                                Text(dashboardCleanupTargetName(suggestion))
                                    .fontWeight(.medium)
                                Text(suggestion.message)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                            Spacer(minLength: 8)
                            Button(dashboardCleanupActionTitle(suggestion)) {
                                pendingCleanup = suggestion
                            }
                            .disabled(suggestion.operation.isEmpty)
                        }
                    }
                }
            }
            Section("Logs") {
                if model.dashboard.logs.isEmpty {
                    Text("No logs yet")
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(Array(model.dashboard.logs.suffix(8).enumerated()), id: \.offset) { _, line in
                        Text(line)
                            .font(.system(.caption, design: .monospaced))
                            .lineLimit(2)
                    }
                }
            }
        }
        .task {
            model.refresh()
        }
        .confirmationDialog(
            "Apply Rule Cleanup",
            isPresented: Binding(
                get: { pendingCleanup != nil },
                set: { if !$0 { pendingCleanup = nil } }
            ),
            presenting: pendingCleanup
        ) { suggestion in
            Button(dashboardCleanupActionTitle(suggestion), role: suggestion.operation == "delete_rule" ? .destructive : nil) {
                model.applyCleanupSuggestion(suggestion)
                pendingCleanup = nil
            }
        } message: { suggestion in
            Text(suggestion.message)
        }
    }

    private var temporaryRuleActionHandler: ((TrafficConnectionPayload, String) -> Void)? {
        #if os(iOS)
        return nil
        #else
        return { connection, action in
            model.createTemporaryRuleFromConnection(connection, action: action)
        }
        #endif
    }
}

private func dashboardCleanupTargetName(_ suggestion: TrafficCleanupSuggestionPayload) -> String {
    suggestion.targetRuleName.isEmpty ? suggestion.ruleName : suggestion.targetRuleName
}

private func dashboardCleanupActionTitle(_ suggestion: TrafficCleanupSuggestionPayload) -> String {
    suggestion.operation == "move_rule_to_end" ? "Move to End" : "Delete"
}

@MainActor
private func dashboardFallbackProxyChain(_ dashboard: DashboardStore) -> String {
    for group in dashboard.policyGroups.groups {
        if !group.selectedChain.isEmpty {
            return group.selectedChain
        }
        if !group.selected.isEmpty {
            return group.selected
        }
    }
    return dashboard.servers.chains.first?.name ?? ""
}

struct TrafficSummaryView: View {
    var traffic: TrafficSnapshotPayload

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            LabeledContent("Active", value: "\(traffic.summary.activeConnections)")
            LabeledContent("Down", value: formatRate(traffic.summary.rxBps))
            LabeledContent("Up", value: formatRate(traffic.summary.txBps))
            LabeledContent("Total", value: "\(formatBytes(traffic.summary.rxTotal)) down / \(formatBytes(traffic.summary.txTotal)) up")
            if !traffic.summary.persistError.isEmpty {
                Text(traffic.summary.persistError)
                    .font(.caption)
                    .foregroundStyle(.red)
            }
        }
    }
}

struct TrafficListView: View {
    var connections: [TrafficConnectionPayload]
    var fallbackChain: String = ""
    var onTemporaryAction: ((TrafficConnectionPayload, String) -> Void)?
    var onPermanentRule: ((TrafficConnectionPayload, RulePayload) -> Void)?

    var body: some View {
        if connections.isEmpty {
            Text("No traffic history")
                .foregroundStyle(.secondary)
        } else {
            ForEach(connections.prefix(12)) { connection in
                VStack(alignment: .leading, spacing: 4) {
                    HStack {
                        Text(emptyDash(connection.target))
                            .fontWeight(.medium)
                        Spacer()
                        Text(connection.state)
                            .foregroundStyle(.secondary)
                    }
                    Text(trafficSubtitle(connection))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Text("\(formatBytes(connection.rxTotal)) down · \(formatBytes(connection.txTotal)) up · \(formatDurationNs(connection.durationNs))")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    TrafficRowActionView(
                        connection: connection,
                        fallbackChain: fallbackChain,
                        onTemporaryAction: onTemporaryAction,
                        onPermanentRule: onPermanentRule
                    )
                }
            }
        }
    }

    private func trafficSubtitle(_ connection: TrafficConnectionPayload) -> String {
        let decision = [connection.ruleName, connection.ruleAction].filter { !$0.isEmpty }.joined(separator: " -> ")
        let parts = [connection.profile, connection.application, connection.network, connection.chainName, decision]
            .filter { !$0.isEmpty }
        if !parts.isEmpty {
            return parts.joined(separator: " · ")
        }
        return connection.listener.protocol
    }
}

private struct TrafficRowActionView: View {
    var connection: TrafficConnectionPayload
    var fallbackChain: String
    var onTemporaryAction: ((TrafficConnectionPayload, String) -> Void)?
    var onPermanentRule: ((TrafficConnectionPayload, RulePayload) -> Void)?

    private var canCreateRule: Bool {
        !connection.connID.isEmpty && !connection.monitorHost.isEmpty
    }

    private var proxyAction: String {
        connection.temporaryProxyAction(fallbackChain: fallbackChain)
    }

    var body: some View {
        ViewThatFits(in: .horizontal) {
            HStack(spacing: 8) {
                buttons
            }
            VStack(alignment: .leading, spacing: 6) {
                buttons
            }
        }
        .font(.caption)
    }

    private var buttons: some View {
        Group {
            if let onTemporaryAction {
                Button("Allow") {
                    onTemporaryAction(connection, "allow")
                }
                .disabled(!canCreateRule)
                Button("Block", role: .destructive) {
                    onTemporaryAction(connection, "block")
                }
                .disabled(!canCreateRule)
                Button("Proxy") {
                    if !proxyAction.isEmpty {
                        onTemporaryAction(connection, proxyAction)
                    }
                }
                .disabled(!canCreateRule || proxyAction.isEmpty)
            }
            Button("Permanent") {
                if let rule = connection.ruleDraft() {
                    onPermanentRule?(connection, rule)
                }
            }
            .disabled(connection.ruleDraft() == nil)
        }
        .buttonStyle(.borderless)
    }
}

struct CompactPolicySelectorView: View {
    var summary: PolicySelectorSummary
    var groups: [PolicyGroupPayload] = []
    var onSelect: ((String, String) -> Void)?
    var routeLimit = 4

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            ViewThatFits(in: .horizontal) {
                HStack(spacing: 8) {
                    actionPills
                }
                VStack(alignment: .leading, spacing: 8) {
                    actionPills
                }
            }

            if groups.isEmpty && summary.routes.isEmpty {
                Label("No route selected", systemImage: "arrow.triangle.branch")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            } else if !groups.isEmpty {
                ForEach(Array(groups.prefix(routeLimit))) { group in
                    CompactPolicyGroupRow(group: group, onSelect: onSelect)
                }
            } else {
                ForEach(Array(summary.routes.prefix(routeLimit))) { route in
                    CompactPolicyRouteRow(route: route)
                }
            }

            if !summary.topRuleHits.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    Text("Rule hits")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    ForEach(summary.topRuleHits) { hit in
                        HStack(spacing: 8) {
                            CompactPolicyActionDot(action: hit.action)
                            Text(hit.ruleName.isEmpty ? "Default route" : hit.ruleName)
                                .font(.subheadline)
                                .lineLimit(1)
                            Spacer(minLength: 8)
                            Text("\(hit.count)")
                                .font(.subheadline.weight(.semibold))
                                .monospacedDigit()
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }
        }
        .padding(.vertical, 2)
    }

    private var actionPills: some View {
        Group {
            CompactPolicyCountPill(title: "Proxy", count: summary.proxyCount, systemImage: "shield.lefthalf.filled", tint: .green)
            CompactPolicyCountPill(title: "Direct", count: summary.directCount, systemImage: "arrow.up.right", tint: .blue)
            CompactPolicyCountPill(title: "Block/Reject", count: summary.blockCount, systemImage: "hand.raised.fill", tint: .red)
        }
    }
}

private struct CompactPolicyGroupRow: View {
    var group: PolicyGroupPayload
    var onSelect: ((String, String) -> Void)?

    private var selected: String {
        if !group.selectedChain.isEmpty {
            return group.selectedChain
        }
        if !group.selected.isEmpty {
            return group.selected
        }
        return group.chains.first ?? ""
    }

    private var isManual: Bool {
        group.type.caseInsensitiveCompare("select") == .orderedSame ||
            group.selectionMode.caseInsensitiveCompare("manual") == .orderedSame
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            HStack(alignment: .top, spacing: 10) {
                Image(systemName: "point.3.connected.trianglepath.dotted")
                    .foregroundStyle(.secondary)
                    .frame(width: 22)

                VStack(alignment: .leading, spacing: 3) {
                    Text(group.name.isEmpty ? "Policy group" : group.name)
                        .font(.subheadline.weight(.medium))
                        .lineLimit(1)
                    Text([policyModeText(group), "selected \(selected.isEmpty ? "No chain selected" : selected)"].joined(separator: " / "))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }

                Spacer(minLength: 8)

                CompactPolicyGroupHealthBadge(group: group)
            }

            ForEach(group.chains, id: \.self) { chain in
                Button {
                    if isManual {
                        onSelect?(group.name, chain)
                    }
                } label: {
                    HStack(spacing: 8) {
                        Image(systemName: chain == selected ? "checkmark.circle.fill" : policyMemberIcon(group: group, chain: chain))
                            .foregroundStyle(policyMemberTint(group: group, chain: chain, selected: selected))
                            .frame(width: 18)
                        Text(chain)
                            .lineLimit(1)
                        Spacer(minLength: 8)
                        Text(policyMemberText(group: group, chain: chain))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                    .font(.caption)
                }
                .buttonStyle(.plain)
                .disabled(!isManual)
            }
        }
        .padding(.vertical, 2)
    }
}

private struct CompactPolicyGroupHealthBadge: View {
    var group: PolicyGroupPayload

    var body: some View {
        Label(policyGroupHealthText(group), systemImage: icon)
            .font(.caption.weight(.medium))
            .lineLimit(1)
            .foregroundStyle(tint)
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .background(tint.opacity(0.12), in: Capsule())
    }

    private var tint: Color {
        if group.results.isEmpty {
            return .secondary
        }
        return policyGroupFallback(group) ? .orange : .green
    }

    private var icon: String {
        if group.results.isEmpty {
            return "arrow.clockwise"
        }
        return policyGroupFallback(group) ? "exclamationmark.triangle.fill" : "checkmark.circle.fill"
    }
}

private struct CompactPolicyCountPill: View {
    var title: String
    var count: Int
    var systemImage: String
    var tint: Color

    var body: some View {
        Label {
            Text("\(title) \(count)")
                .monospacedDigit()
        } icon: {
            Image(systemName: systemImage)
        }
        .font(.caption.weight(.semibold))
        .foregroundStyle(tint)
        .lineLimit(1)
        .padding(.horizontal, 9)
        .padding(.vertical, 6)
        .background(tint.opacity(0.12), in: Capsule())
    }
}

private struct CompactPolicyRouteRow: View {
    var route: PolicySelectorRouteSummary

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "point.3.connected.trianglepath.dotted")
                .foregroundStyle(.secondary)
                .frame(width: 22)

            VStack(alignment: .leading, spacing: 3) {
                Text(route.groupName.isEmpty ? "Route" : route.groupName)
                    .font(.subheadline.weight(.medium))
                    .lineLimit(1)
                Text(route.selectedChain.isEmpty ? "No chain selected" : route.selectedChain)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer(minLength: 8)

            CompactPolicyHealthBadge(route: route)
        }
        .padding(.vertical, 2)
    }
}

private struct CompactPolicyHealthBadge: View {
    var route: PolicySelectorRouteSummary

    var body: some View {
        Label(route.healthText, systemImage: icon)
            .font(.caption.weight(.medium))
            .lineLimit(1)
            .foregroundStyle(tint)
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .background(tint.opacity(0.12), in: Capsule())
    }

    private var icon: String {
        switch route.healthState {
        case .staticRoute:
            return "arrow.triangle.branch"
        case .pending:
            return "clock"
        case .healthy:
            return "checkmark.circle.fill"
        case .fallback:
            return "exclamationmark.triangle.fill"
        }
    }

    private var tint: Color {
        switch route.healthState {
        case .staticRoute, .pending:
            return .secondary
        case .healthy:
            return .green
        case .fallback:
            return .orange
        }
    }
}

private func policyModeText(_ group: PolicyGroupPayload) -> String {
    let value = group.selectionMode.isEmpty ? group.type : group.selectionMode
    return value.isEmpty ? "policy" : value.replacingOccurrences(of: "-", with: " ")
}

private func policyMemberText(group: PolicyGroupPayload, chain: String) -> String {
    guard let result = group.results.first(where: { $0.chainName == chain }) else {
        return "pending"
    }
    if result.healthy {
        return result.latencyNs > 0 ? formatDurationNs(result.latencyNs) : "healthy"
    }
    return result.error.isEmpty ? "unhealthy" : result.error
}

private func policyMemberIcon(group: PolicyGroupPayload, chain: String) -> String {
    guard let result = group.results.first(where: { $0.chainName == chain }) else {
        return "clock"
    }
    return result.healthy ? "checkmark.circle" : "exclamationmark.triangle"
}

private func policyMemberTint(group: PolicyGroupPayload, chain: String, selected: String) -> Color {
    if chain == selected {
        return .green
    }
    guard let result = group.results.first(where: { $0.chainName == chain }) else {
        return .secondary
    }
    return result.healthy ? .green : .orange
}

private func policyGroupHealthText(_ group: PolicyGroupPayload) -> String {
    guard !group.results.isEmpty else {
        return "Pending health"
    }
    let healthy = group.results.filter(\.healthy).count
    if policyGroupFallback(group) {
        return "Fallback / \(healthy)/\(group.results.count) healthy"
    }
    return "Healthy / \(healthy)/\(group.results.count)"
}

private func policyGroupFallback(_ group: PolicyGroupPayload) -> Bool {
    guard !group.results.isEmpty else {
        return false
    }
    let selected = group.selectedChain.isEmpty ? (group.selected.isEmpty ? (group.chains.first ?? "") : group.selected) : group.selectedChain
    return group.results.first(where: { $0.chainName == selected })?.healthy != true
}

private struct CompactPolicyActionDot: View {
    var action: String

    var body: some View {
        Circle()
            .fill(tint)
            .frame(width: 8, height: 8)
    }

    private var tint: Color {
        switch action.lowercased() {
        case "direct":
            return .blue
        case "block", "reject":
            return .red
        default:
            return .green
        }
    }
}

struct RuleListView: View {
    var rules: RulesPayload

    var body: some View {
        if rules.rules.isEmpty {
            Text("No routing rules")
                .foregroundStyle(.secondary)
        } else {
            ForEach(rules.rules) { rule in
                VStack(alignment: .leading, spacing: 4) {
                    HStack {
                        Text(rule.name)
                            .fontWeight(.medium)
                        Spacer()
                        Text(rule.action)
                            .foregroundStyle(.secondary)
                    }
                    Text(ruleSummary(rule))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private func ruleSummary(_ rule: RulePayload) -> String {
        var parts: [String] = []
        if !rule.domains.isEmpty {
            parts.append(rule.domains.joined(separator: ", "))
        }
        if !rule.domainSuffixes.isEmpty {
            parts.append(rule.domainSuffixes.map { "*.\($0)" }.joined(separator: ", "))
        }
        if !rule.cidrs.isEmpty {
            parts.append(rule.cidrs.joined(separator: ", "))
        }
        if !rule.ports.isEmpty {
            parts.append(rule.ports.map(String.init).joined(separator: ", "))
        }
        if !rule.networks.isEmpty {
            parts.append(rule.networks.joined(separator: ", "))
        }
        return parts.isEmpty ? "all traffic" : parts.joined(separator: " · ")
    }
}

struct StatusHeaderView: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Label(
                    model.dashboard.status.running ? "Running" : "Stopped",
                    systemImage: model.dashboard.status.running ? "checkmark.circle.fill" : "pause.circle"
                )
                .foregroundStyle(model.dashboard.status.running ? .green : .secondary)
                Spacer()
                Label(model.dashboard.apiOnline ? "API online" : "API offline", systemImage: "network")
                    .foregroundStyle(model.dashboard.apiOnline ? .green : .red)
            }
            Text(emptyDash(model.dashboard.activeProfile))
                .font(.headline)
            if !model.dashboard.errorText.isEmpty {
                Text(model.dashboard.errorText)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .lineLimit(3)
            }
            HStack {
                Button {
                    model.connectOrDisconnect()
                } label: {
                    Label(model.dashboard.status.running ? "Disconnect" : "Connect", systemImage: model.dashboard.status.running ? "stop.fill" : "play.fill")
                }
                Button {
                    model.refresh()
                } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                }
            }
        }
    }
}

struct ServerListView: View {
    var servers: ServersPayload

    var body: some View {
        if servers.chains.isEmpty {
            Text("No servers in active profile")
                .foregroundStyle(.secondary)
        } else {
            ForEach(servers.chains) { chain in
                VStack(alignment: .leading, spacing: 2) {
                    Text(chain.name)
                        .fontWeight(.semibold)
                    Text("\(chain.hopCount) hops · \(udpSupportText(chain.capabilities))")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                ForEach(chain.servers) { server in
                    VStack(alignment: .leading, spacing: 4) {
                        HStack {
                            Text(countryFlag(server.geo.countryCode))
                            Text(server.name)
                                .fontWeight(.medium)
                            Spacer()
                            Text(server.protocol)
                                .foregroundStyle(.secondary)
                        }
                        Text(server.address)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Text("\(serverLocation(server)) · \(chain.name)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
    }
}
