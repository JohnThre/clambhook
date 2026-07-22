import AppKit
import ClambhookShared
import SwiftUI

// MARK: - Dashboard

struct MacDashboardSection: View {
    @ObservedObject var model: AppleAppModel
    @ObservedObject private var daemon: DaemonSupervisor
    var onNavigate: ((SidebarItem) -> Void)?

    init(model: AppleAppModel, onNavigate: ((SidebarItem) -> Void)? = nil) {
        self.model = model
        self.onNavigate = onNavigate
        self._daemon = ObservedObject(wrappedValue: model.daemonSupervisor)
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                heroPanel

                if !model.appRecoveryStates.isEmpty {
                    recoveryStates
                }

                capabilityGrid

                HStack(alignment: .top, spacing: 16) {
                    VStack(alignment: .leading, spacing: 16) {
                        trafficCommandCenter
                        policyGroupHealth
                    }
                    .frame(maxWidth: .infinity, alignment: .topLeading)

                    VStack(alignment: .leading, spacing: 16) {
                        destinationIntelligence
                        miniActivityFeed
                    }
                    .frame(maxWidth: 360, alignment: .topLeading)
                }
            }
            .padding(24)
        }
        .background(
            LinearGradient(
                colors: [
                    Color(nsColor: .windowBackgroundColor),
                    Color.accentColor.opacity(0.045)
                ],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        )
        .task {
            model.refresh()
        }
    }

    // MARK: Hero

    private var heroPanel: some View {
        ZStack(alignment: .topTrailing) {
            RoundedRectangle(cornerRadius: 28, style: .continuous)
                .fill(
                    LinearGradient(
                        colors: heroGradient,
                        startPoint: .topLeading,
                        endPoint: .bottomTrailing
                    )
                )
            Circle()
                .fill(Color.white.opacity(0.11))
                .frame(width: 230, height: 230)
                .offset(x: 82, y: -96)
            Circle()
                .stroke(Color.white.opacity(0.12), lineWidth: 32)
                .frame(width: 300, height: 300)
                .offset(x: 118, y: 34)

            HStack(alignment: .center, spacing: 24) {
                VStack(alignment: .leading, spacing: 18) {
                    HStack(spacing: 10) {
                        DashboardPill(text: statusText, systemImage: statusSymbol, tint: .white)
                        DashboardPill(text: tunnelModeLabel, systemImage: tunnelModeSymbol, tint: .white)
                        DashboardPill(
                            text: model.dashboard.apiOnline ? "API online" : "API offline",
                            systemImage: model.dashboard.apiOnline ? "checkmark.circle.fill" : "xmark.circle.fill",
                            tint: model.dashboard.apiOnline ? .white : .orange
                        )
                        if daemon.state.isBusy {
                            ProgressView()
                                .controlSize(.small)
                                .tint(.white)
                                .scaleEffect(0.8)
                        }
                    }

                    VStack(alignment: .leading, spacing: 6) {
                        Text(model.dashboard.status.running ? "ClambHook is defending this Mac" : "ClambHook is ready to protect")
                            .font(.system(size: 28, weight: .bold, design: .rounded))
                            .foregroundStyle(.white)
                        Text(heroSubtitle)
                            .font(.subheadline)
                            .foregroundStyle(.white.opacity(0.78))
                            .lineLimit(2)
                    }

                    HStack(spacing: 12) {
                        profilePicker
                        Button {
                            model.connectOrDisconnect()
                        } label: {
                            Label(
                                model.dashboard.status.running ? "Disconnect" : "Connect",
                                systemImage: model.dashboard.status.running ? "stop.fill" : "play.fill"
                            )
                            .frame(minWidth: 118)
                        }
                        .buttonStyle(.borderedProminent)
                        .tint(model.dashboard.status.running ? .red : .green)
                        .controlSize(.large)
                        .disabled(!model.dashboard.apiOnline && !model.dashboard.status.running)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)

                VStack(alignment: .leading, spacing: 14) {
                    HStack(spacing: 16) {
                        DashboardHeroMetric(title: "Down", value: formatRate(currentBandwidth.rxBps), systemImage: "arrow.down")
                        DashboardHeroMetric(title: "Up", value: formatRate(currentBandwidth.txBps), systemImage: "arrow.up")
                    }
                    DashboardHeroMetric(title: "Active flows", value: "\(activeConnections)", systemImage: "bolt.horizontal.circle.fill")
                    DashboardHeroMetric(title: "Best route", value: bestLatency, systemImage: "speedometer")
                }
                .padding(16)
                .frame(width: 260, alignment: .leading)
                .background(Color.black.opacity(0.18), in: RoundedRectangle(cornerRadius: 18, style: .continuous))
                .overlay(
                    RoundedRectangle(cornerRadius: 18, style: .continuous)
                        .stroke(Color.white.opacity(0.16), lineWidth: 1)
                )
            }
            .padding(24)
        }
        .frame(minHeight: 220)
    }

    private var heroGradient: [Color] {
        if model.dashboard.status.running {
            return [Color.green.opacity(0.95), Color.teal.opacity(0.88), Color.blue.opacity(0.9)]
        }
        switch daemon.state {
        case .failed:
            return [Color.red.opacity(0.9), Color.orange.opacity(0.82), Color.pink.opacity(0.8)]
        case .starting, .stopping:
            return [Color.orange.opacity(0.9), Color.yellow.opacity(0.75), Color.blue.opacity(0.75)]
        default:
            return [Color.secondary.opacity(0.85), Color.blue.opacity(0.55), Color.indigo.opacity(0.72)]
        }
    }

    private var heroSubtitle: String {
        let profile = model.dashboard.activeProfile.isEmpty ? "No active profile" : model.dashboard.activeProfile
        if !model.dashboard.errorText.isEmpty {
            return model.dashboard.errorText
        }
        if model.dashboard.status.running {
            return "\(profile) · \(activeConnections) live flows · \(model.dashboard.policyGroups.groups.count) policy groups · \(model.dashboard.rules.routeTestRules.count) routing rules"
        }
        return "Import a profile, inspect HTTP traffic, route by policy, and block unwanted connections from one home screen."
    }

    private var profilePicker: some View {
        HStack(spacing: 8) {
            Image(systemName: "person.crop.circle")
                .foregroundStyle(.white.opacity(0.82))
            if model.dashboard.profiles.profiles.isEmpty {
                Text(model.dashboard.activeProfile.isEmpty ? "No profile" : model.dashboard.activeProfile)
                    .foregroundStyle(.white.opacity(0.86))
            } else {
                Picker("Profile", selection: Binding(
                    get: { model.dashboard.activeProfile },
                    set: { model.selectProfile($0) }
                )) {
                    ForEach(model.dashboard.profiles.profiles, id: \.self) { profile in
                        Text(profile).tag(profile)
                    }
                }
                .labelsHidden()
                .pickerStyle(.menu)
                .tint(.white)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .background(Color.black.opacity(0.18), in: Capsule())
        .overlay(Capsule().stroke(Color.white.opacity(0.16), lineWidth: 1))
    }

    private var statusSymbol: String {
        if model.dashboard.status.running { return "shield.checkered" }
        switch daemon.state {
        case .starting, .stopping: return "clock.arrow.circlepath"
        case .failed: return "exclamationmark.triangle.fill"
        default: return "shield"
        }
    }

    private var statusText: String {
        if model.dashboard.status.running { return "Connected" }
        switch daemon.state {
        case .running: return "Daemon running"
        case .starting: return "Starting…"
        case .stopping: return "Stopping…"
        case .failed: return "Daemon failed"
        case .stopped: return "Disconnected"
        }
    }

    private var tunnelModeLabel: String {
        switch model.dashboard.status.tunnelMode.lowercased() {
        case "tun": return "Enhanced tunnel"
        case "proxy": return "Proxy mode"
        case "": return "Mode pending"
        default: return model.dashboard.status.tunnelMode
        }
    }

    private var tunnelModeSymbol: String {
        model.dashboard.status.tunnelMode.lowercased() == "tun" ? "network.badge.shield.half.filled" : "globe"
    }

    private var currentBandwidth: BandwidthSample {
        let sample = model.dashboard.currentBandwidth
        if sample.rxBps > 0 || sample.txBps > 0 {
            return sample
        }
        return BandwidthSample(rxBps: model.dashboard.traffic.summary.rxBps, txBps: model.dashboard.traffic.summary.txBps)
    }

    private var activeConnections: Int {
        max(
            model.dashboard.traffic.summary.activeConnections,
            model.dashboard.status.listeners.reduce(0) { $0 + $1.activeConns }
        )
    }

    private var bestLatency: String {
        for group in model.dashboard.policyGroups.groups {
            let selected = group.selectedChain.isEmpty ? group.selected : group.selectedChain
            if let result = group.results.first(where: { $0.chainName == selected }), result.latencyNs > 0 {
                return formatDurationNs(result.latencyNs)
            }
        }
        return "--"
    }

    // MARK: Competitive overview

    private var capabilityGrid: some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 170), spacing: 12)], spacing: 12) {
            DashboardSummaryCard(
                title: "Surge-grade proxy",
                value: activeRouteName,
                footnote: "Policy routing · \(model.dashboard.servers.chains.count) chains",
                systemImage: "point.3.connected.trianglepath.dotted",
                tint: .green
            )
            DashboardSummaryCard(
                title: "App firewall",
                value: "\(model.dashboard.traffic.blockDecisions.count)",
                footnote: "Blocked decisions",
                systemImage: "hand.raised.fill",
                tint: .red
            )
            DashboardSummaryCard(
                title: "HTTP inspector",
                value: "\(visibleHTTPFlowCount)",
                footnote: "Visible web flows",
                systemImage: "list.bullet.rectangle.portrait.fill",
                tint: .blue
            )
            DashboardSummaryCard(
                title: "Network map",
                value: "\(geolocatedConnectionCount)",
                footnote: "Geolocated connections",
                systemImage: "globe.americas.fill",
                tint: .purple
            )
        }
    }

    private var activeRouteName: String {
        for group in model.dashboard.policyGroups.groups {
            let selected = group.selectedChain.isEmpty ? group.selected : group.selectedChain
            if !selected.isEmpty { return selected }
        }
        return dashboardFallbackProxyChain(model.dashboard)
    }

    private var visibleHTTPFlowCount: Int {
        model.dashboard.traffic.connections.filter { connection in
            guard let visibility = connection.visibility else { return false }
            let kind = visibility.kind.lowercased()
            let scheme = visibility.scheme.lowercased()
            return kind.contains("http") || scheme == "http" || scheme == "https"
        }.count
    }

    private var geolocatedConnectionCount: Int {
        model.dashboard.traffic.connections.filter { !$0.geo.country.isEmpty || !$0.geo.city.isEmpty }.count
    }

    // MARK: Traffic command center

    private var trafficCommandCenter: some View {
        DashboardCard {
            VStack(alignment: .leading, spacing: 16) {
                DashboardSectionTitle(
                    title: "Traffic Command Center",
                    subtitle: "Live bandwidth, inspection, map, rules, and system controls.",
                    systemImage: "waveform.path.ecg"
                )

                DashboardBandwidthChart(samples: bandwidthChartSamples)
                    .frame(height: 142)

                HStack(spacing: 10) {
                    DashboardQuickActionButton(title: "Activity", subtitle: "Live flows", systemImage: "arrow.up.arrow.down", tint: .green) {
                        onNavigate?(.activity)
                    }
                    DashboardQuickActionButton(title: "Map", subtitle: "World view", systemImage: "globe.americas.fill", tint: .purple) {
                        onNavigate?(.map)
                    }
                    DashboardQuickActionButton(title: "HTTP", subtitle: "Inspect", systemImage: "list.bullet.rectangle", tint: .blue) {
                        onNavigate?(.httpCapture)
                    }
                    DashboardQuickActionButton(title: "Rules", subtitle: "Firewall", systemImage: "line.3.horizontal.decrease.circle", tint: .red) {
                        onNavigate?(.rules)
                    }
                }
            }
        }
    }

    private var bandwidthChartSamples: [BandwidthSample] {
        let samples = Array(model.dashboard.bandwidthSamples.suffix(48))
        if samples.isEmpty { return [currentBandwidth] }
        return samples
    }

    // MARK: Recovery

    private var recoveryStates: some View {
        DashboardCard {
            VStack(alignment: .leading, spacing: 12) {
                DashboardSectionTitle(
                    title: "Attention",
                    subtitle: "Resolve setup, license, and tunnel issues before production routing.",
                    systemImage: "exclamationmark.triangle.fill"
                )
                ForEach(model.appRecoveryStates) { state in
                    AppRecoveryStatePanel(state: state) { action in
                        handleRecoveryAction(action)
                    }
                }
            }
        }
    }

    private func handleRecoveryAction(_ action: AppRecoveryStateAction) {
        switch action {
        case .createProfile, .importProfile, .openProfiles:
            onNavigate?(.profile(model.dashboard.activeProfile))
        case .openAppSettings, .openSettings, .openSystemSettings:
            onNavigate?(.settings)
        case .buyLicense, .activateLicense, .openLicensePortal, .renewUpdates:
            onNavigate?(.license)
        default:
            break
        }
        model.performAppRecoveryAction(action)
    }

    // MARK: Policy group health

    @ViewBuilder
    private var policyGroupHealth: some View {
        if model.dashboard.policyGroups.groups.isEmpty {
            DashboardCard {
                DashboardEmptyState(
                    title: "No policy groups yet",
                    message: "Import a profile to unlock Surge-style selectors and latency probes.",
                    systemImage: "point.3.connected.trianglepath.dotted"
                )
            }
        } else {
            DashboardCard {
                VStack(alignment: .leading, spacing: 12) {
                    HStack(alignment: .firstTextBaseline) {
                        DashboardSectionTitle(
                            title: "Policy Groups",
                            subtitle: "Manual selectors and latency health at a glance.",
                            systemImage: "point.3.connected.trianglepath.dotted"
                        )
                        Spacer()
                        Button("Manage") { onNavigate?(.policyGroups) }
                            .buttonStyle(.borderless)
                    }
                    ForEach(model.dashboard.policyGroups.groups.prefix(5)) { group in
                        MacPolicyGroupHealthRow(group: group, onSelect: { chain in
                            model.selectPolicyGroup(group: group.name, chain: chain)
                        })
                    }
                }
            }
        }
    }

    // MARK: Destination intelligence

    private var destinationIntelligence: some View {
        DashboardCard {
            VStack(alignment: .leading, spacing: 12) {
                HStack(alignment: .firstTextBaseline) {
                    DashboardSectionTitle(
                        title: "Destinations",
                        subtitle: "Top hosts, route decisions, and firewall blocks.",
                        systemImage: "scope"
                    )
                    Spacer()
                    Button("Map") { onNavigate?(.map) }
                        .buttonStyle(.borderless)
                }

                if !model.dashboard.traffic.destinationGroups.isEmpty {
                    ForEach(model.dashboard.traffic.destinationGroups.prefix(6)) { group in
                        DashboardDestinationRow(group: group)
                    }
                } else if !model.dashboard.traffic.blockDecisions.isEmpty {
                    ForEach(model.dashboard.traffic.blockDecisions.prefix(6)) { decision in
                        DashboardDecisionRow(decision: decision)
                    }
                } else {
                    DashboardEmptyState(
                        title: "No destinations yet",
                        message: "Connections appear here with route, bytes, and block context.",
                        systemImage: "network"
                    )
                }
            }
        }
    }

    // MARK: Mini activity feed

    private var miniActivityFeed: some View {
        DashboardCard {
            VStack(alignment: .leading, spacing: 12) {
                HStack(alignment: .firstTextBaseline) {
                    DashboardSectionTitle(
                        title: "Live Activity",
                        subtitle: "Little Snitch-style latest decisions.",
                        systemImage: "bolt.horizontal.circle.fill"
                    )
                    Spacer()
                    let counts = model.dashboard.monitorActionCounts
                    HStack(spacing: 6) {
                        MacActionBadge(label: "P \(counts["proxy", default: 0])", color: .green)
                        MacActionBadge(label: "D \(counts["direct", default: 0])", color: .blue)
                        MacActionBadge(label: "B \(counts["block", default: 0])", color: .red)
                    }
                }

                let connections = Array(model.dashboard.traffic.connections.prefix(10))
                if connections.isEmpty && model.dashboard.recentDecisions.isEmpty {
                    DashboardEmptyState(
                        title: "No recent traffic",
                        message: "Start routing to see app, host, rule, and route decisions.",
                        systemImage: "bolt.slash"
                    )
                } else if !connections.isEmpty {
                    ForEach(connections) { conn in
                        MiniActivityRow(connection: conn)
                    }
                } else {
                    ForEach(model.dashboard.recentDecisions.prefix(10)) { decision in
                        HStack(spacing: 8) {
                            Circle()
                                .fill(decisionColor(decision.action))
                                .frame(width: 8, height: 8)
                            Text(emptyDash(decision.target))
                                .font(.caption)
                                .lineLimit(1)
                                .truncationMode(.middle)
                            Spacer(minLength: 8)
                            Text([decision.ruleName, decision.action].filter { !$0.isEmpty }.joined(separator: " / "))
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }
                    }
                }
            }
        }
    }

    private func decisionColor(_ action: String) -> Color {
        switch action.lowercased() {
        case "direct": return .blue
        case "block", "reject": return .red
        default: return .green
        }
    }
}

private struct DashboardCard<Content: View>: View {
    var content: Content

    init(@ViewBuilder content: () -> Content) {
        self.content = content()
    }

    var body: some View {
        content
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(16)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 18, style: .continuous)
                    .stroke(Color.secondary.opacity(0.12), lineWidth: 1)
            )
    }
}

private struct DashboardSectionTitle: View {
    var title: String
    var subtitle: String
    var systemImage: String

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: systemImage)
                .font(.headline.weight(.semibold))
                .foregroundStyle(Color.accentColor)
                .frame(width: 28, height: 28)
                .background(Color.accentColor.opacity(0.12), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.headline)
                Text(subtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
        }
    }
}

private struct DashboardPill: View {
    var text: String
    var systemImage: String
    var tint: Color

    var body: some View {
        Label(text, systemImage: systemImage)
            .font(.caption.weight(.semibold))
            .foregroundStyle(tint)
            .lineLimit(1)
            .padding(.horizontal, 10)
            .padding(.vertical, 6)
            .background(Color.black.opacity(0.18), in: Capsule())
            .overlay(Capsule().stroke(tint.opacity(0.24), lineWidth: 1))
    }
}

private struct DashboardHeroMetric: View {
    var title: String
    var value: String
    var systemImage: String

    var body: some View {
        HStack(spacing: 9) {
            Image(systemName: systemImage)
                .font(.headline.weight(.semibold))
                .frame(width: 28, height: 28)
                .background(Color.white.opacity(0.13), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(.white.opacity(0.7))
                    .textCase(.uppercase)
                Text(value)
                    .font(.headline.monospacedDigit())
                    .foregroundStyle(.white)
                    .lineLimit(1)
                    .minimumScaleFactor(0.72)
            }
        }
    }
}

private struct DashboardSummaryCard: View {
    var title: String
    var value: String
    var footnote: String
    var systemImage: String
    var tint: Color

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: systemImage)
                .font(.title3.weight(.semibold))
                .foregroundStyle(tint)
                .frame(width: 38, height: 38)
                .background(tint.opacity(0.13), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
            VStack(alignment: .leading, spacing: 5) {
                Text(title)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
                    .textCase(.uppercase)
                Text(value.isEmpty ? "--" : value)
                    .font(.title3.weight(.bold))
                    .lineLimit(1)
                    .minimumScaleFactor(0.7)
                Text(footnote)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 0)
        }
        .padding(14)
        .background(tint.opacity(0.07), in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .stroke(tint.opacity(0.18), lineWidth: 1)
        )
    }
}

private struct DashboardQuickActionButton: View {
    var title: String
    var subtitle: String
    var systemImage: String
    var tint: Color
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(alignment: .leading, spacing: 8) {
                Image(systemName: systemImage)
                    .font(.headline.weight(.semibold))
                    .foregroundStyle(tint)
                VStack(alignment: .leading, spacing: 2) {
                    Text(title)
                        .font(.subheadline.weight(.semibold))
                    Text(subtitle)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(12)
            .background(tint.opacity(0.08), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 14, style: .continuous)
                    .stroke(tint.opacity(0.18), lineWidth: 1)
            )
        }
        .buttonStyle(.plain)
    }
}

private struct DashboardBandwidthChart: View {
    var samples: [BandwidthSample]

    private var peak: Double {
        max(samples.map { max($0.rxBps, $0.txBps) }.max() ?? 1, 1)
    }

    var body: some View {
        GeometryReader { proxy in
            ZStack(alignment: .bottomLeading) {
                RoundedRectangle(cornerRadius: 14, style: .continuous)
                    .fill(Color.secondary.opacity(0.055))
                VStack(spacing: 0) {
                    ForEach(0..<4, id: \.self) { _ in
                        Divider().opacity(0.25)
                        Spacer(minLength: 0)
                    }
                }
                chartPath(in: proxy.size, values: samples.map(\.rxBps))
                    .stroke(Color.green, style: StrokeStyle(lineWidth: 2.5, lineCap: .round, lineJoin: .round))
                chartPath(in: proxy.size, values: samples.map(\.txBps))
                    .stroke(Color.blue, style: StrokeStyle(lineWidth: 2.5, lineCap: .round, lineJoin: .round))
                HStack(spacing: 10) {
                    chartLegend(color: .green, text: "Down")
                    chartLegend(color: .blue, text: "Up")
                    Spacer()
                    Text("Peak \(formatRate(peak))")
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
                .padding(12)
            }
        }
    }

    private func chartPath(in size: CGSize, values: [Double]) -> Path {
        var path = Path()
        guard !values.isEmpty, size.width > 0, size.height > 0 else { return path }
        let inset: CGFloat = 14
        let plotWidth = max(size.width - inset * 2, 1)
        let plotHeight = max(size.height - inset * 2, 1)
        let denominator = max(values.count - 1, 1)
        for index in values.indices {
            let x = inset + plotWidth * CGFloat(index) / CGFloat(denominator)
            let normalized = min(max(values[index] / peak, 0), 1)
            let y = inset + plotHeight * CGFloat(1 - normalized)
            if index == values.startIndex {
                path.move(to: CGPoint(x: x, y: y))
            } else {
                path.addLine(to: CGPoint(x: x, y: y))
            }
        }
        return path
    }

    private func chartLegend(color: Color, text: String) -> some View {
        HStack(spacing: 4) {
            Circle().fill(color).frame(width: 7, height: 7)
            Text(text)
                .font(.caption.weight(.medium))
                .foregroundStyle(.secondary)
        }
    }
}

private struct DashboardDestinationRow: View {
    var group: TrafficDestinationGroupPayload

    var body: some View {
        HStack(spacing: 10) {
            Circle()
                .fill(actionColor)
                .frame(width: 9, height: 9)
            VStack(alignment: .leading, spacing: 2) {
                Text(emptyDash(group.displayHost))
                    .font(.caption.weight(.semibold))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text([group.topRuleName, group.topChainName].filter { !$0.isEmpty }.joined(separator: " · "))
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 8)
            VStack(alignment: .trailing, spacing: 2) {
                Text("\(group.count)")
                    .font(.caption.monospacedDigit().weight(.semibold))
                Text(formatBytes(group.rxTotal + group.txTotal))
                    .font(.caption2.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 3)
    }

    private var actionColor: Color {
        if group.actions.contains(where: { $0.caseInsensitiveCompare("block") == .orderedSame || $0.caseInsensitiveCompare("reject") == .orderedSame }) {
            return .red
        }
        if group.actions.contains(where: { $0.caseInsensitiveCompare("direct") == .orderedSame }) {
            return .blue
        }
        return .green
    }
}

private struct DashboardDecisionRow: View {
    var decision: TrafficBlockDecisionPayload

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: "hand.raised.fill")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.red)
                .frame(width: 20)
            VStack(alignment: .leading, spacing: 2) {
                Text(emptyDash(decision.targetHost.isEmpty ? decision.target : decision.targetHost))
                    .font(.caption.weight(.semibold))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text([decision.ruleName, decision.network, decision.closeReason].filter { !$0.isEmpty }.joined(separator: " · "))
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, 3)
    }
}

private struct DashboardEmptyState: View {
    var title: String
    var message: String
    var systemImage: String

    var body: some View {
        VStack(spacing: 6) {
            Image(systemName: systemImage)
                .font(.title2)
                .foregroundStyle(.secondary)
            Text(title)
                .font(.subheadline.weight(.semibold))
            Text(message)
                .font(.caption)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 16)
    }
}


private struct MacPolicyGroupHealthRow: View {
    var group: PolicyGroupPayload
    var onSelect: (String) -> Void

    private var selected: String {
        group.selectedChain.isEmpty ? group.selected : group.selectedChain
    }

    private var selectedResult: PolicyProbeResultPayload? {
        group.results.first(where: { $0.chainName == selected })
    }

    private var isManual: Bool {
        group.type.caseInsensitiveCompare("select") == .orderedSame ||
            group.selectionMode.caseInsensitiveCompare("manual") == .orderedSame
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Circle()
                    .fill(healthColor)
                    .frame(width: 9, height: 9)
                Text(group.name.isEmpty ? "Policy group" : group.name)
                    .font(.subheadline.weight(.medium))
                    .lineLimit(1)
                Spacer(minLength: 8)
                if let result = selectedResult, result.latencyNs > 0 {
                    Text(formatDurationNs(result.latencyNs))
                        .font(.caption.weight(.semibold))
                        .monospacedDigit()
                        .foregroundStyle(.secondary)
                }
                Text(selected.isEmpty ? "--" : selected)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            if isManual && !group.chains.isEmpty {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 6) {
                        ForEach(group.chains, id: \.self) { chain in
                            Button {
                                onSelect(chain)
                            } label: {
                                HStack(spacing: 4) {
                                    if chain == selected {
                                        Image(systemName: "checkmark")
                                            .font(.caption2.weight(.bold))
                                    }
                                    Text(chain)
                                        .font(.caption)
                                }
                                .padding(.horizontal, 8)
                                .padding(.vertical, 4)
                                .background(
                                    chain == selected ? Color.accentColor.opacity(0.15) : Color.secondary.opacity(0.08),
                                    in: Capsule()
                                )
                                .foregroundStyle(chain == selected ? Color.accentColor : Color.primary)
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }
            }
        }
        .padding(10)
        .background(Color.secondary.opacity(0.05), in: RoundedRectangle(cornerRadius: 8))
    }

    private var healthColor: Color {
        guard let result = selectedResult else { return .secondary }
        return result.healthy ? .green : .orange
    }
}

private struct MiniActivityRow: View {
    var connection: TrafficConnectionPayload

    private var isActive: Bool { connection.state.lowercased() == "active" }

    private var hostLabel: String {
        let host = connection.targetHost.isEmpty ? connection.target : connection.targetHost
        if !connection.targetPort.isEmpty && connection.targetPort != "0" {
            return "\(host):\(connection.targetPort)"
        }
        return host
    }

    private var actionColor: Color {
        switch connection.actionFamily {
        case "block": return .red
        case "direct": return .blue
        default: return .green
        }
    }

    var body: some View {
        HStack(spacing: 8) {
            Circle()
                .fill(actionColor)
                .frame(width: 8, height: 8)
            VStack(alignment: .leading, spacing: 1) {
                Text(emptyDash(hostLabel))
                    .font(.caption)
                    .lineLimit(1)
                    .truncationMode(.middle)
                if !connection.application.isEmpty {
                    Text(connection.application)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            Spacer(minLength: 8)
            VStack(alignment: .trailing, spacing: 1) {
                if isActive {
                    HStack(spacing: 3) {
                        Circle().fill(Color.green).frame(width: 5, height: 5)
                        Text("active").font(.caption2).foregroundStyle(.green)
                    }
                } else {
                    Text(timeAgoShort(connection.startTsNs))
                        .font(.caption2.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
            }
        }
        .padding(.vertical, 1)
    }
}

private struct MacActionBadge: View {
    var label: String
    var color: Color

    var body: some View {
        Text(label)
            .font(.caption2.weight(.semibold))
            .monospacedDigit()
            .foregroundStyle(color)
            .padding(.horizontal, 6)
            .padding(.vertical, 3)
            .background(color.opacity(0.12), in: Capsule())
    }
}
