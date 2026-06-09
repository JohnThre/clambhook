import ClambhookShared
import SwiftUI

struct IOSPolicyGroupsView: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 12) {
                IOSSurfaceSection("Route Mix", detail: activeProfileText) {
                    IOSMetricsGrid(metrics: [
                        IOSMetric(title: "Proxy", value: "\(summary.proxyCount)", systemImage: "shield.lefthalf.filled"),
                        IOSMetric(title: "Direct", value: "\(summary.directCount)", systemImage: "arrow.up.right"),
                        IOSMetric(title: "Block", value: "\(summary.blockCount)", systemImage: "hand.raised.fill"),
                        IOSMetric(title: "Groups", value: "\(model.dashboard.policyGroups.groups.count)", systemImage: "point.3.connected.trianglepath.dotted"),
                    ])
                }

                IOSSurfaceSection("Policy Groups", detail: policyGroupDetail) {
                    if model.dashboard.policyGroups.groups.isEmpty {
                        ContentUnavailableView(
                            "No policy groups",
                            systemImage: "point.3.connected.trianglepath.dotted",
                            description: Text("Static profile routes are shown below.")
                        )
                    } else {
                        VStack(spacing: 10) {
                            ForEach(model.dashboard.policyGroups.groups) { group in
                                IOSPolicyGroupCard(group: group) { chain in
                                    model.selectPolicyGroup(group: group.name, chain: chain)
                                }
                            }
                        }
                    }
                }

                IOSSurfaceSection("Current Routes", detail: "\(summary.routes.count)") {
                    if summary.routes.isEmpty {
                        IOSInlineEmptyState(text: "No route summary is available.", systemImage: "arrow.triangle.branch")
                    } else {
                        VStack(spacing: 8) {
                            ForEach(Array(summary.routes.prefix(8))) { route in
                                IOSPolicyRouteRow(route: route)
                            }
                        }
                    }
                }

                if !summary.topRuleHits.isEmpty {
                    IOSSurfaceSection("Rule Hits", detail: "\(summary.topRuleHits.count)") {
                        VStack(spacing: 8) {
                            ForEach(summary.topRuleHits) { hit in
                                HStack(spacing: 10) {
                                    IOSActionChip(action: hit.action)
                                    Text(hit.ruleName.isEmpty ? "Default route" : hit.ruleName)
                                        .font(.subheadline.weight(.medium))
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
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
        }
        .background(Color(.systemGroupedBackground))
        .refreshable {
            await model.refreshNow()
        }
    }

    private var summary: PolicySelectorSummary {
        model.dashboard.policySelectorSummary
    }

    private var activeProfileText: String {
        model.dashboard.activeProfile.isEmpty ? "No active profile" : model.dashboard.activeProfile
    }

    private var policyGroupDetail: String {
        let count = model.dashboard.policyGroups.groups.count
        return count == 1 ? "1 group" : "\(count) groups"
    }
}

private struct IOSPolicyGroupCard: View {
    var group: PolicyGroupPayload
    var onSelect: (String) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .top, spacing: 10) {
                Image(systemName: "point.3.connected.trianglepath.dotted")
                    .foregroundStyle(.secondary)
                    .frame(width: 24)

                VStack(alignment: .leading, spacing: 4) {
                    Text(group.name.isEmpty ? "Policy group" : group.name)
                        .font(.subheadline.weight(.semibold))
                        .lineLimit(1)
                    Text([modeText, selectedText].filter { !$0.isEmpty }.joined(separator: " / "))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }

                Spacer(minLength: 8)

                IOSStatusBadge(text: healthText, systemImage: healthIcon, tint: healthTint)
            }

            if group.chains.isEmpty {
                IOSInlineEmptyState(text: "No member chains.", systemImage: "tray")
            } else {
                VStack(spacing: 6) {
                    ForEach(group.chains, id: \.self) { chain in
                        Button {
                            if isManual {
                                onSelect(chain)
                            }
                        } label: {
                            IOSPolicyMemberRow(
                                chain: chain,
                                result: result(for: chain),
                                isSelected: chain == selectedChain,
                                isManual: isManual
                            )
                        }
                        .buttonStyle(.plain)
                        .disabled(!isManual || chain == selectedChain)
                    }
                }
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(.tertiarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }

    private var selectedChain: String {
        if !group.selectedChain.isEmpty {
            return group.selectedChain
        }
        if !group.selected.isEmpty {
            return group.selected
        }
        return group.chains.first ?? ""
    }

    private var selectedText: String {
        selectedChain.isEmpty ? "No chain selected" : "Selected \(selectedChain)"
    }

    private var modeText: String {
        let value = group.selectionMode.isEmpty ? group.type : group.selectionMode
        return value.isEmpty ? "static" : value.replacingOccurrences(of: "-", with: " ")
    }

    private var isManual: Bool {
        group.type.caseInsensitiveCompare("select") == .orderedSame ||
            group.selectionMode.caseInsensitiveCompare("manual") == .orderedSame
    }

    private var healthText: String {
        guard !group.results.isEmpty else {
            return isManual ? "Manual" : "Pending"
        }
        let healthy = group.results.filter(\.healthy).count
        return fallbackSelected ? "Fallback \(healthy)/\(group.results.count)" : "Healthy \(healthy)/\(group.results.count)"
    }

    private var healthIcon: String {
        if group.results.isEmpty {
            return isManual ? "hand.tap" : "clock"
        }
        return fallbackSelected ? "exclamationmark.triangle.fill" : "checkmark.circle.fill"
    }

    private var healthTint: Color {
        if group.results.isEmpty {
            return .secondary
        }
        return fallbackSelected ? .orange : .green
    }

    private var fallbackSelected: Bool {
        guard !group.results.isEmpty else {
            return false
        }
        return group.results.first(where: { $0.chainName == selectedChain })?.healthy != true
    }

    private func result(for chain: String) -> PolicyProbeResultPayload? {
        group.results.first { $0.chainName == chain }
    }
}

private struct IOSPolicyMemberRow: View {
    var chain: String
    var result: PolicyProbeResultPayload?
    var isSelected: Bool
    var isManual: Bool

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: icon)
                .foregroundStyle(tint)
                .frame(width: 22)
            VStack(alignment: .leading, spacing: 2) {
                Text(emptyDash(chain))
                    .font(.subheadline.weight(.medium))
                    .lineLimit(1)
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
            Spacer(minLength: 8)
            if isManual {
                Image(systemName: "chevron.right")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .background(isSelected ? Color.accentColor.opacity(0.12) : Color.secondary.opacity(0.06), in: RoundedRectangle(cornerRadius: 7, style: .continuous))
    }

    private var icon: String {
        if isSelected {
            return "checkmark.circle.fill"
        }
        guard let result else {
            return "clock"
        }
        return result.healthy ? "checkmark.circle" : "exclamationmark.triangle"
    }

    private var tint: Color {
        if isSelected {
            return .accentColor
        }
        guard let result else {
            return .secondary
        }
        return result.healthy ? .green : .orange
    }

    private var detail: String {
        guard let result else {
            return isManual ? "Tap a member to select it when health arrives." : "Waiting for health probe."
        }
        if result.healthy {
            return result.latencyNs > 0 ? formatDurationNs(result.latencyNs) : "Healthy"
        }
        return result.error.isEmpty ? "Unhealthy" : result.error
    }
}

private struct IOSPolicyRouteRow: View {
    var route: PolicySelectorRouteSummary

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "arrow.triangle.branch")
                .foregroundStyle(tint)
                .frame(width: 24)
            VStack(alignment: .leading, spacing: 3) {
                Text(route.groupName.isEmpty ? "Route" : route.groupName)
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                Text([route.selectedChain, route.healthText].filter { !$0.isEmpty }.joined(separator: " / "))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
            Spacer(minLength: 8)
            Image(systemName: icon)
                .foregroundStyle(tint)
        }
        .padding(.vertical, 2)
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
