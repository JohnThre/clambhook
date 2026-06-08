import ClambhookShared
import SwiftUI

struct IOSStatusView: View {
    @ObservedObject var model: AppleAppModel
    var onRecoveryAction: ((TunnelRecoveryAction) -> Void)? = nil

    var body: some View {
        ScrollView {
            VStack(spacing: 12) {
                IOSConnectionHeader(model: model, onRecoveryAction: onRecoveryAction)
                IOSBandwidthPanel(model: model)
                IOSPolicyControlPanel(model: model)
                IOSNetworkSummaryPanel(model: model)
                IOSProfileSwitchStrip(model: model)
                IOSRecentDecisionsPanel(model: model)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
        }
        .background(Color(.systemGroupedBackground))
        .refreshable {
            await model.refreshNow()
        }
    }
}

private struct IOSConnectionHeader: View {
    @ObservedObject var model: AppleAppModel
    var onRecoveryAction: ((TunnelRecoveryAction) -> Void)?
    @AppStorage("org.jpfchang.clambhook.vpnDisclosureAccepted") private var vpnDisclosureAccepted = false
    @State private var showingVPNDisclosure = false

    var body: some View {
        IOSPanel {
            VStack(alignment: .leading, spacing: 14) {
                HStack(alignment: .center, spacing: 12) {
                    statusGlyph

                    VStack(alignment: .leading, spacing: 4) {
                        Text(model.dashboard.status.running ? "Connected" : "Disconnected")
                            .font(.title3.weight(.semibold))
                            .lineLimit(1)
                        Text(profileLine)
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }

                    Spacer(minLength: 10)

                    Button {
                        model.refresh()
                    } label: {
                        Image(systemName: "arrow.clockwise")
                            .frame(width: 34, height: 34)
                    }
                    .buttonStyle(.bordered)
                    .accessibilityLabel("Refresh dashboard")
                }

                HStack(spacing: 8) {
                    IOSStatusBadge(
                        text: model.dashboard.apiOnline ? "Tunnel ready" : "Tunnel unavailable",
                        systemImage: "network",
                        tint: model.dashboard.apiOnline ? .green : .red
                    )
                    IOSStatusBadge(
                        text: model.dashboard.status.running ? "Running" : "Stopped",
                        systemImage: model.dashboard.status.running ? "checkmark.circle.fill" : "pause.circle",
                        tint: connectionTint
                    )
                    Spacer(minLength: 0)
                }

                if let issue = model.dashboard.recoveryIssue {
                    IOSRecoveryBanner(issue: issue) { action in
                        if let onRecoveryAction {
                            onRecoveryAction(action)
                        } else {
                            model.performRecoveryAction(action)
                        }
                    }
                } else if !model.dashboard.errorText.isEmpty {
                    Label(model.dashboard.errorText, systemImage: "exclamationmark.triangle.fill")
                        .font(.caption)
                        .foregroundStyle(.red)
                        .lineLimit(3)
                }

                Button {
                    handleConnectAction()
                } label: {
                    Label(
                        model.dashboard.status.running ? "Disconnect" : "Connect",
                        systemImage: model.dashboard.status.running ? "stop.fill" : "play.fill"
                    )
                    .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(!model.dashboard.apiOnline && !model.dashboard.status.running)
            }
        }
        .sheet(isPresented: $showingVPNDisclosure) {
            IOSVPNDisclosureSheet {
                vpnDisclosureAccepted = true
                model.connectOrDisconnect()
            }
        }
    }

    private var statusGlyph: some View {
        ZStack {
            Circle()
                .fill(connectionTint.opacity(0.14))
            Image(systemName: model.dashboard.status.running ? "network" : "network.slash")
                .font(.title3)
                .foregroundStyle(connectionTint)
        }
        .frame(width: 46, height: 46)
    }

    private var profileLine: String {
        model.dashboard.activeProfile.isEmpty ? "No active profile" : model.dashboard.activeProfile
    }

    private var connectionTint: Color {
        model.dashboard.status.running ? .green : .secondary
    }

    private func handleConnectAction() {
        if model.dashboard.status.running || vpnDisclosureAccepted {
            model.connectOrDisconnect()
            return
        }
        showingVPNDisclosure = true
    }
}

private struct IOSBandwidthPanel: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        IOSPanel {
            VStack(alignment: .leading, spacing: 12) {
                HStack(alignment: .firstTextBaseline) {
                    Text("Traffic")
                        .font(.headline)
                    Spacer()
                    Text("\(model.dashboard.traffic.summary.activeConnections) active")
                        .font(.caption.weight(.medium))
                        .foregroundStyle(.secondary)
                        .monospacedDigit()
                }

                IOSBandwidthGraphView(samples: model.dashboard.bandwidthSamples)

                ViewThatFits(in: .horizontal) {
                    HStack(spacing: 10) {
                        rateItem("Down", value: sample.rxBps, systemImage: "arrow.down", tint: .green)
                        rateItem("Up", value: sample.txBps, systemImage: "arrow.up", tint: .blue)
                        totalItem
                    }
                    VStack(alignment: .leading, spacing: 8) {
                        rateItem("Down", value: sample.rxBps, systemImage: "arrow.down", tint: .green)
                        rateItem("Up", value: sample.txBps, systemImage: "arrow.up", tint: .blue)
                        totalItem
                    }
                }
            }
        }
    }

    private var sample: BandwidthSample {
        model.dashboard.currentBandwidth
    }

    private func rateItem(_ title: String, value: Double, systemImage: String, tint: Color) -> some View {
        Label {
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Text(formatRate(value))
                    .font(.subheadline.weight(.semibold))
                    .monospacedDigit()
                    .lineLimit(1)
                    .minimumScaleFactor(0.75)
            }
        } icon: {
            Image(systemName: systemImage)
                .foregroundStyle(tint)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private var totalItem: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text("Total")
                .font(.caption)
                .foregroundStyle(.secondary)
            Text("\(formatBytes(model.dashboard.traffic.summary.rxTotal)) / \(formatBytes(model.dashboard.traffic.summary.txTotal))")
                .font(.subheadline.weight(.semibold))
                .monospacedDigit()
                .lineLimit(1)
                .minimumScaleFactor(0.7)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

private struct IOSPolicyControlPanel: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        IOSPanel {
            VStack(alignment: .leading, spacing: 12) {
                HStack(alignment: .firstTextBaseline) {
                    Text("Policy")
                        .font(.headline)
                    Spacer()
                    IOSStatusBadge(text: policyMode, systemImage: "slider.horizontal.3", tint: .blue)
                }

                IOSSummaryRow(
                    title: activeGroupTitle,
                    detail: activeGroupDetail,
                    systemImage: "point.3.connected.trianglepath.dotted",
                    tint: activeGroupTint
                )

                ViewThatFits(in: .horizontal) {
                    HStack(spacing: 8) {
                        actionPills
                    }
                    VStack(alignment: .leading, spacing: 8) {
                        actionPills
                    }
                }
            }
        }
    }

    private var actionPills: some View {
        Group {
            IOSActionCountPill(title: "Proxy", count: summary.proxyCount, systemImage: "shield.lefthalf.filled", tint: .green)
            IOSActionCountPill(title: "Direct", count: summary.directCount, systemImage: "arrow.up.right", tint: .blue)
            IOSActionCountPill(title: "Block", count: summary.blockCount, systemImage: "hand.raised.fill", tint: .red)
        }
    }

    private var summary: PolicySelectorSummary {
        model.dashboard.policySelectorSummary
    }

    private var firstGroup: PolicyGroupPayload? {
        model.dashboard.policyGroups.groups.first
    }

    private var firstRoute: PolicySelectorRouteSummary? {
        summary.routes.first
    }

    private var policyMode: String {
        if let group = firstGroup {
            let value = group.selectionMode.isEmpty ? group.type : group.selectionMode
            return value.isEmpty ? "Policy" : value.replacingOccurrences(of: "-", with: " ").capitalized
        }
        return "Static"
    }

    private var activeGroupTitle: String {
        guard let route = firstRoute, !route.groupName.isEmpty else {
            return "Default Route"
        }
        return route.groupName
    }

    private var activeGroupDetail: String {
        guard let route = firstRoute else {
            return "No proxy group selected"
        }
        let selected = route.selectedChain.isEmpty ? "No chain selected" : route.selectedChain
        return [selected, route.healthText].filter { !$0.isEmpty }.joined(separator: " / ")
    }

    private var activeGroupTint: Color {
        switch firstRoute?.healthState {
        case .healthy:
            return .green
        case .fallback:
            return .orange
        case .staticRoute, .pending, nil:
            return .secondary
        }
    }
}

private struct IOSNetworkSummaryPanel: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        IOSPanel {
            VStack(alignment: .leading, spacing: 12) {
                Text("DNS & Routing")
                    .font(.headline)

                IOSSummaryRow(title: dnsTitle, detail: dnsDetail, systemImage: "globe", tint: .teal)
                IOSSummaryRow(title: routingTitle, detail: routingDetail, systemImage: "arrow.triangle.branch", tint: .purple)
                IOSSummaryRow(title: proxyTitle, detail: proxyDetail, systemImage: "network", tint: .indigo)
            }
        }
    }

    private var settings: TunnelNetworkSettingsPayload {
        model.dashboard.networkSettings
    }

    private var dnsTitle: String {
        settings.dnsServers.isEmpty ? "System DNS" : "\(settings.dnsServers.count) tunnel DNS"
    }

    private var dnsDetail: String {
        if settings.dnsServers.isEmpty {
            return "No tunnel DNS override"
        }
        return settings.dnsServers.prefix(2).joined(separator: ", ")
    }

    private var routingTitle: String {
        if isFullTunnel {
            return "Full Tunnel"
        }
        let count = settings.includedRoutes.count
        return count == 1 ? "1 Included Route" : "\(count) Included Routes"
    }

    private var routingDetail: String {
        var parts: [String] = []
        if settings.includedRoutes.isEmpty {
            parts.append("Default mobile routes")
        } else {
            parts.append(settings.includedRoutes.prefix(2).joined(separator: ", "))
        }
        if !settings.excludedRoutes.isEmpty {
            parts.append("\(settings.excludedRoutes.count) excluded")
        }
        return parts.joined(separator: " / ")
    }

    private var proxyTitle: String {
        settings.httpProxy == nil ? "No HTTP Proxy" : "HTTP Proxy"
    }

    private var proxyDetail: String {
        guard let proxy = settings.httpProxy else {
            let listeners = model.dashboard.status.listeners.map { iosListenerDescription(TrafficListenerPayload(protocol: $0.protocol, addr: $0.addr)) }
            return listeners.isEmpty ? "Packet tunnel only" : listeners.prefix(2).joined(separator: ", ")
        }
        return "\(proxy.host):\(proxy.port)"
    }

    private var isFullTunnel: Bool {
        settings.includedRoutes.contains("0.0.0.0/0") || settings.includedRoutes.contains("::/0")
    }
}

private struct IOSRecentDecisionsPanel: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        IOSPanel {
            VStack(alignment: .leading, spacing: 10) {
                HStack(alignment: .firstTextBaseline) {
                    Text("Recent")
                        .font(.headline)
                    Spacer()
                    Text("\(model.dashboard.recentDecisions.count)")
                        .font(.caption.weight(.medium))
                        .foregroundStyle(.secondary)
                        .monospacedDigit()
                }

                if model.dashboard.recentDecisions.isEmpty {
                    IOSInlineEmptyState(text: "No recent activity.", systemImage: "arrow.triangle.branch")
                } else {
                    ForEach(model.dashboard.recentDecisions.prefix(5)) { decision in
                        IOSDecisionRow(decision: decision)
                    }
                }
            }
        }
    }
}

private struct IOSPanel<Content: View>: View {
    var content: Content

    init(@ViewBuilder content: () -> Content) {
        self.content = content()
    }

    var body: some View {
        content
            .padding(14)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }
}

private struct IOSSummaryRow: View {
    var title: String
    var detail: String
    var systemImage: String
    var tint: Color

    var body: some View {
        HStack(alignment: .center, spacing: 10) {
            Image(systemName: systemImage)
                .foregroundStyle(tint)
                .frame(width: 24)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                Text(detail.isEmpty ? "--" : detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
            Spacer(minLength: 0)
        }
    }
}

private struct IOSActionCountPill: View {
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

private struct IOSVPNDisclosureSheet: View {
    var onAccept: () -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    Image(systemName: "network.badge.shield.half.filled")
                        .font(.system(size: 44))
                        .foregroundStyle(.tint)
                        .frame(maxWidth: .infinity, alignment: .leading)

                    Text("VPN Data Use")
                        .font(.title2.weight(.semibold))

                    Text(vpnDataUseDisclosure)
                        .font(.body)
                        .foregroundStyle(.primary)
                        .fixedSize(horizontal: false, vertical: true)

                    Link("Privacy Policy", destination: defaultPrivacyPolicyURL)
                        .font(.body.weight(.medium))
                }
                .padding(20)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .navigationTitle("Before You Connect")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Continue") {
                        dismiss()
                        onAccept()
                    }
                    .fontWeight(.semibold)
                }
            }
        }
    }
}

private struct IOSRouteStatusPanel: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 12) {
                Image(systemName: "arrow.triangle.branch")
                    .foregroundStyle(.secondary)
                    .frame(width: 24)

                VStack(alignment: .leading, spacing: 3) {
                    Text("Rule")
                        .font(.body.weight(.medium))
                    Text(routeSubtitle)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }

                Spacer(minLength: 8)

                IOSStatusBadge(text: "\(model.dashboard.rules.rules.count) rules", systemImage: "slider.horizontal.3", tint: .blue)
            }

            CompactPolicySelectorView(summary: model.dashboard.policySelectorSummary)
        }
    }

    private var routeSubtitle: String {
        var parts = ["\(model.dashboard.servers.chains.count) route\(model.dashboard.servers.chains.count == 1 ? "" : "s")"]
        if !model.dashboard.policyGroups.groups.isEmpty {
            parts.append("\(model.dashboard.policyGroups.groups.count) polic\(model.dashboard.policyGroups.groups.count == 1 ? "y" : "ies")")
        }
        return parts.joined(separator: " / ")
    }
}

private struct IOSProfileSwitchStrip: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        if model.dashboard.profiles.profiles.isEmpty {
            IOSInlineEmptyState(text: "No profiles are available.", systemImage: "person.crop.rectangle.stack")
                .padding(.bottom, 10)
        } else {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 8) {
                    ForEach(model.dashboard.profiles.profiles, id: \.self) { profile in
                        Button {
                            model.selectProfile(profile)
                        } label: {
                            Label(profile, systemImage: profile == model.dashboard.activeProfile ? "checkmark.circle.fill" : "circle")
                                .font(.subheadline.weight(.medium))
                                .lineLimit(1)
                                .padding(.horizontal, 12)
                                .padding(.vertical, 8)
                                .background(profile == model.dashboard.activeProfile ? Color.accentColor.opacity(0.16) : Color.secondary.opacity(0.08), in: Capsule())
                        }
                        .buttonStyle(.plain)
                        .foregroundStyle(profile == model.dashboard.activeProfile ? Color.accentColor : Color.primary)
                        .disabled(profile == model.dashboard.activeProfile)
                    }
                }
            }
            .padding(.bottom, 10)
        }
    }
}

private struct IOSDecisionRow: View {
    var decision: RecentDecision

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            IOSActionChip(action: decision.action)
            VStack(alignment: .leading, spacing: 4) {
                Text(emptyDash(decision.target))
                    .font(.body.weight(.medium))
                    .lineLimit(1)
                Text([decision.ruleName, decision.connection.chainName, decision.connection.displayVisibility].filter { !$0.isEmpty }.joined(separator: " / "))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 2)
    }
}

private struct IOSRuleHitRow: View {
    var summary: RuleHitSummary

    var body: some View {
        HStack(spacing: 12) {
            IOSActionChip(action: summary.action)
            Text(summary.ruleName.isEmpty ? "Default route" : summary.ruleName)
                .font(.body.weight(.medium))
                .lineLimit(1)
            Spacer(minLength: 8)
            Text("\(summary.count)")
                .font(.subheadline.weight(.semibold))
                .monospacedDigit()
                .foregroundStyle(.secondary)
        }
    }
}
