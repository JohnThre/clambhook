import AppKit
import ClambhookShared
import SwiftUI

// MARK: - Dashboard

struct MacDashboardSection: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                StatusHeaderView(model: model)
                Divider()
                metricsGrid
                Divider()
                listenersList
            }
            .padding(20)
        }
    }

    private var metricsGrid: some View {
        let sample = model.dashboard.currentBandwidth
        let activeConnections = model.dashboard.status.listeners.reduce(0) { $0 + $1.activeConns }
        return LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 10) {
            MacMetricCard(title: "Download", value: formatRate(sample.rxBps), systemImage: "arrow.down", tint: .blue)
            MacMetricCard(title: "Upload", value: formatRate(sample.txBps), systemImage: "arrow.up", tint: .green)
            MacMetricCard(title: "Active", value: "\(activeConnections)", systemImage: "point.3.connected.trianglepath.dotted", tint: .orange)
            MacMetricCard(title: "Listeners", value: "\(model.dashboard.status.listeners.count)", systemImage: "antenna.radiowaves.left.and.right", tint: .purple)
        }
    }

    private var listenersList: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Listeners")
                .font(.headline)
            if model.dashboard.status.listeners.isEmpty {
                Text("None active")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(model.dashboard.status.listeners) { listener in
                    HStack {
                        Image(systemName: "antenna.radiowaves.left.and.right")
                            .foregroundStyle(.secondary)
                            .frame(width: 20)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(listener.protocol.uppercased())
                                .font(.caption.weight(.semibold))
                            Text(listener.addr)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Text("\(listener.activeConns) active")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
    }
}

private struct MacMetricCard: View {
    var title: String
    var value: String
    var systemImage: String
    var tint: Color

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: systemImage)
                .font(.title3)
                .foregroundStyle(tint)
                .frame(width: 28)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Text(value)
                    .font(.title3.weight(.semibold))
                    .monospacedDigit()
                    .lineLimit(1)
            }
            Spacer(minLength: 0)
        }
        .padding(14)
        .background(Color.secondary.opacity(0.06), in: RoundedRectangle(cornerRadius: 10))
    }
}

// MARK: - Profiles

struct MacProfilesSection: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                profilePicker
                Divider()
                ServerListView(servers: model.dashboard.servers)
            }
            .padding(20)
        }
    }

    private var profilePicker: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Active Profile")
                .font(.headline)
            if model.dashboard.profiles.profiles.isEmpty {
                Text("No profiles")
                    .foregroundStyle(.secondary)
            } else {
                Picker("Profile", selection: Binding(
                    get: { model.dashboard.activeProfile },
                    set: { model.selectProfile($0) }
                )) {
                    ForEach(model.dashboard.profiles.profiles, id: \.self) { profile in
                        HStack {
                            Text(profile)
                            if profile == model.dashboard.activeProfile {
                                Image(systemName: "checkmark.circle.fill")
                                    .foregroundStyle(.green)
                            }
                        }
                        .tag(profile)
                    }
                }
                .pickerStyle(.menu)
            }
        }
    }
}

// MARK: - Policy Groups

struct MacPolicyGroupsSection: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                CompactPolicySelectorView(
                    summary: model.dashboard.policySelectorSummary,
                    groups: model.dashboard.policyGroups.groups,
                    onSelect: { group, chain in
                        model.selectPolicyGroup(group: group, chain: chain)
                    }
                )
            }
            .padding(20)
        }
    }
}

// MARK: - Rules

struct MacRulesSection: View {
    @ObservedObject var model: AppleAppModel
    @State private var routeTestNetwork = "tcp"
    @State private var routeTestTarget = "example.com:443"
    @State private var routeTestResult: RuleTestResponse?
    @State private var routeTestError = ""

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                RuleListView(rules: model.dashboard.rules)
                if !model.dashboard.rules.effectiveRules.isEmpty && model.dashboard.rules.effectiveRules.count != model.dashboard.rules.rules.count {
                    Divider()
                    VStack(alignment: .leading, spacing: 10) {
                        Text("Effective Rules")
                            .font(.headline)
                        ForEach(model.dashboard.rules.effectiveRules) { rule in
                            VStack(alignment: .leading, spacing: 4) {
                                HStack {
                                    Text(rule.name).fontWeight(.medium)
                                    Spacer()
                                    Text(rule.action).foregroundStyle(.secondary)
                                }
                                Text(ruleSummary(rule))
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                        }
                    }
                }
                if !model.dashboard.rules.ruleSets.isEmpty {
                    Divider()
                    VStack(alignment: .leading, spacing: 10) {
                        Text("Rule Sets")
                            .font(.headline)
                        ForEach(model.dashboard.rules.ruleSets) { rs in
                            HStack {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(rs.name).fontWeight(.medium)
                                    Text(rs.url).font(.caption).foregroundStyle(.secondary).lineLimit(1)
                                }
                                Spacer()
                                VStack(alignment: .trailing, spacing: 2) {
                                    Text(rs.cached ? "Cached" : "Not cached")
                                        .font(.caption)
                                        .foregroundStyle(rs.cached ? .green : .secondary)
                                    if rs.domainCount + rs.cidrCount > 0 {
                                        Text("\(rs.domainCount) domains / \(rs.cidrCount) CIDRs")
                                            .font(.caption2).foregroundStyle(.secondary)
                                    }
                                }
                            }
                        }
                        Button {
                            model.refreshActiveProfileRuleSets()
                        } label: {
                            Label("Refresh Rule Sets", systemImage: "arrow.clockwise")
                        }
                    }
                }
                Divider()
                routeTester
            }
            .padding(20)
        }
    }

    private var routeTester: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Route Tester")
                .font(.headline)
            HStack(spacing: 8) {
                Picker("Network", selection: $routeTestNetwork) {
                    Text("TCP").tag("tcp")
                    Text("UDP").tag("udp")
                }
                .labelsHidden()
                .pickerStyle(.segmented)
                .frame(width: 120)
                TextField("host:port", text: $routeTestTarget)
                    .textFieldStyle(.roundedBorder)
                Button {
                    routeTestError = ""
                    Task {
                        do {
                            routeTestResult = try await model.testRule(network: routeTestNetwork, target: routeTestTarget)
                        } catch {
                            routeTestResult = nil
                            routeTestError = error.localizedDescription
                        }
                    }
                } label: {
                    Label("Test", systemImage: "checkmark.circle")
                }
            }
            if !routeTestError.isEmpty {
                Text(routeTestError)
                    .font(.caption)
                    .foregroundStyle(.red)
            } else if let result = routeTestResult {
                VStack(alignment: .leading, spacing: 4) {
                    Text("Action: \(result.decision.action)")
                        .font(.caption.weight(.semibold))
                    Text("Rule: \(result.decision.ruleName.isEmpty ? "Default" : result.decision.ruleName)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    if !result.decision.chainName.isEmpty {
                        Text("Chain: \(result.decision.chainName)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
    }

    private func ruleSummary(_ rule: RulePayload) -> String {
        var parts: [String] = []
        if !rule.domains.isEmpty { parts.append(rule.domains.joined(separator: ", ")) }
        if !rule.domainSuffixes.isEmpty { parts.append(rule.domainSuffixes.map { "*.\($0)" }.joined(separator: ", ")) }
        if !rule.cidrs.isEmpty { parts.append(rule.cidrs.joined(separator: ", ")) }
        if !rule.ports.isEmpty { parts.append(rule.ports.map(String.init).joined(separator: ", ")) }
        if !rule.networks.isEmpty { parts.append(rule.networks.joined(separator: ", ")) }
        return parts.isEmpty ? "all traffic" : parts.joined(separator: " · ")
    }
}

// MARK: - DNS

struct MacDNSSection: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                dnsOverview
                if !model.dashboard.dns.upstreams.isEmpty {
                    Divider()
                    upstreamsTable
                }
                if !model.dashboard.dns.upstreamRoutes.isEmpty {
                    Divider()
                    routesTable
                }
            }
            .padding(20)
        }
    }

    private var dnsOverview: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("DNS Configuration")
                .font(.headline)
            HStack(spacing: 16) {
                Label(model.dashboard.dns.enabled ? "Enabled" : "Disabled", systemImage: model.dashboard.dns.enabled ? "checkmark.circle.fill" : "xmark.circle")
                    .foregroundStyle(model.dashboard.dns.enabled ? .green : .secondary)
                Label("Strategy: \(model.dashboard.dns.strategy)", systemImage: "arrow.triangle.branch")
                    .foregroundStyle(.secondary)
                if !model.dashboard.dns.timeout.isEmpty {
                    Label("Timeout: \(model.dashboard.dns.timeout)", systemImage: "clock")
                        .foregroundStyle(.secondary)
                }
                if model.dashboard.dns.interceptsPort53 {
                    Label("Intercepts port 53", systemImage: "shield.lefthalf.filled")
                        .foregroundStyle(.blue)
                }
            }
            .font(.subheadline)
        }
    }

    private var upstreamsTable: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Upstreams")
                .font(.headline)
            Table(model.dashboard.dns.upstreams) {
                TableColumn("Name") { upstream in
                    Text(upstream.name.isEmpty ? upstream.id : upstream.name)
                }
                TableColumn("Protocol") { upstream in
                    Text(upstream.protocol.uppercased())
                }
                TableColumn("Address / URL") { upstream in
                    Text(upstream.targetDescription)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                TableColumn("Bootstrap IPs") { upstream in
                    Text(upstream.bootstrapIPs.isEmpty ? "--" : upstream.bootstrapIPs.joined(separator: ", "))
                        .font(.caption)
                        .lineLimit(1)
                }
            }
        }
    }

    private var routesTable: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Upstream Routes")
                .font(.headline)
            Table(model.dashboard.dns.upstreamRoutes) {
                TableColumn("Name") { route in
                    Text(route.name.isEmpty ? route.id : route.name)
                }
                TableColumn("Network") { route in
                    Text(route.network.isEmpty ? "all" : route.network)
                }
                TableColumn("Action") { route in
                    Text(route.action)
                }
                TableColumn("Target") { route in
                    Text(route.target)
                        .lineLimit(1)
                }
                TableColumn("Chain") { route in
                    Text(route.chainName.isEmpty ? "--" : route.chainName)
                }
            }
        }
    }
}

// MARK: - Activity

struct MacActivitySection: View {
    @ObservedObject var model: AppleAppModel
    @State private var trafficFilter = "all"
    @State private var trafficSearch = ""
    @State private var draftRule: RulePayload?
    @State private var sourceConnection: TrafficConnectionPayload?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                TrafficSummaryView(traffic: model.dashboard.traffic)
                Divider()
                filterBar
                trafficList
                if !model.dashboard.traffic.blockDecisions.isEmpty {
                    Divider()
                    blockDecisionsList
                }
                if !model.dashboard.traffic.cleanupSuggestions.isEmpty {
                    Divider()
                    cleanupList
                }
            }
            .padding(20)
        }
        .sheet(item: $draftRule) { rule in
            MacRuleCreateSheet(model: model, initialRule: rule, sourceConnection: sourceConnection)
        }
    }

    private var filterBar: some View {
        HStack(spacing: 10) {
            Picker("Filter", selection: $trafficFilter) {
                Text("All").tag("all")
                Text("Proxy").tag("proxy")
                Text("Direct").tag("direct")
                Text("Block").tag("block")
            }
            .labelsHidden()
            .pickerStyle(.segmented)
            TextField("Search hosts, rules, chains", text: $trafficSearch)
                .textFieldStyle(.roundedBorder)
        }
    }

    private var filteredTraffic: [TrafficConnectionPayload] {
        let query = trafficSearch.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return model.dashboard.traffic.connections.filter { connection in
            (trafficFilter == "all" || connection.actionFamily == trafficFilter)
            && (query.isEmpty || [
                connection.target, connection.monitorHost, connection.ruleName,
                connection.ruleAction, connection.chainName, connection.application, connection.network,
            ].contains { $0.lowercased().contains(query) })
        }
    }

    private var trafficList: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Connections")
                .font(.headline)
            TrafficListView(
                connections: filteredTraffic,
                fallbackChain: dashboardFallbackProxyChain(model.dashboard),
                onTemporaryAction: { connection, action in
                    model.createTemporaryRuleFromConnection(connection, action: action)
                },
                onPermanentRule: { connection, rule in
                    model.createRuleFromConnection(connection, rule: rule)
                }
            )
        }
    }

    private var blockDecisionsList: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Blocked")
                .font(.headline)
            ForEach(model.dashboard.traffic.blockDecisions) { decision in
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

    private var cleanupList: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Rule Cleanup")
                .font(.headline)
            ForEach(model.dashboard.traffic.cleanupSuggestions) { suggestion in
                HStack(alignment: .top, spacing: 12) {
                    VStack(alignment: .leading, spacing: 3) {
                        Text(suggestion.targetRuleName.isEmpty ? suggestion.ruleName : suggestion.targetRuleName)
                            .fontWeight(.medium)
                        Text(suggestion.message)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    Spacer(minLength: 8)
                    Button(suggestion.operation == "move_rule_to_end" ? "Move to End" : "Delete") {
                        model.applyCleanupSuggestion(suggestion)
                    }
                    .disabled(suggestion.operation.isEmpty)
                }
            }
        }
    }
}

// MARK: - HTTP Capture

struct MacHTTPCaptureSection: View {
    @ObservedObject var model: AppleAppModel
    @State private var captureFilter: CaptureFilterKind = .all
    @State private var captureSearch = ""

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                captureStatus
                Divider()
                HStack(spacing: 10) {
                    Picker("Filter", selection: $captureFilter) {
                        Text("All").tag(CaptureFilterKind.all)
                        Text("HTTP").tag(CaptureFilterKind.http)
                        Text("HTTPS").tag(CaptureFilterKind.https)
                        Text("Pinned").tag(CaptureFilterKind.pinned)
                    }
                    .labelsHidden()
                    .pickerStyle(.segmented)
                    TextField("Search method, host, path, rule", text: $captureSearch)
                        .textFieldStyle(.roundedBorder)
                }
                captureGroups
                Text("HTTPS rows remain CONNECT metadata unless opt-in HTTPS Body Capture is enabled in developer config.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            .padding(20)
        }
    }

    private var captureEntries: [CaptureEntryPayload] {
        CaptureSupport.captureEntries(from: model.dashboard.traffic)
    }

    private var filteredCaptureEntries: [CaptureEntryPayload] {
        CaptureSupport.filteredEntries(
            captureEntries,
            filter: captureFilter,
            query: captureSearch,
            pinnedIDs: model.pinnedConnectionIDs
        )
    }

    private var captureStatus: some View {
        HStack(spacing: 12) {
            Label("\(captureEntries.count) metadata requests", systemImage: "list.bullet.rectangle")
                .foregroundStyle(captureEntries.isEmpty ? Color.secondary : Color.blue)
            Label("\(CaptureSupport.groupEntriesByHost(filteredCaptureEntries, pinnedIDs: model.pinnedConnectionIDs).count) hosts", systemImage: "rectangle.stack")
                .foregroundStyle(.secondary)
            Spacer()
            ShareLink(
                item: CaptureSupport.exportString(traffic: model.dashboard.traffic, entries: filteredCaptureEntries),
                subject: Text("ClambHook HTTP metadata export"),
                message: Text("Local metadata-only JSON export.")
            ) {
                Image(systemName: "square.and.arrow.up")
            }
            .disabled(filteredCaptureEntries.isEmpty)
        }
        .font(.subheadline)
    }

    private var captureGroups: some View {
        let groups = CaptureSupport.groupEntriesByHost(filteredCaptureEntries, pinnedIDs: model.pinnedConnectionIDs)
        return VStack(alignment: .leading, spacing: 10) {
            if groups.isEmpty {
                Text("No HTTP metadata")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(groups) { group in
                    MacCaptureGroupCard(group: group, pinnedIDs: model.pinnedConnectionIDs, onTogglePin: toggleCapturePin)
                }
            }
        }
    }

    private func toggleCapturePin(_ entry: CaptureMetadataEntryPayload) {
        var ids = model.pinnedConnectionIDs
        if ids.contains(entry.pinID) {
            ids.remove(entry.pinID)
        } else {
            ids.insert(entry.pinID)
        }
        model.settingsStore.settings.pinnedConnectionIDs = ids.sorted()
    }
}

private struct MacCaptureGroupCard: View {
    var group: CaptureGroupPayload
    var pinnedIDs: Set<String>
    var onTogglePin: (CaptureMetadataEntryPayload) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text(emptyDash(group.host))
                    .font(.headline)
                Spacer()
                let schemes = group.schemes.map { $0.uppercased() }.joined(separator: ", ")
                Text(schemes.isEmpty ? "\(group.count)" : "\(group.count) / \(schemes)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            ForEach(group.entries) { entry in
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text(entry.method.isEmpty ? "--" : entry.method)
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(entry.scheme.lowercased() == "https" ? .blue : .green)
                        .frame(minWidth: 46, alignment: .leading)
                    Text(emptyDash(entry.displayTarget))
                        .font(.caption)
                        .lineLimit(1)
                    Spacer()
                    Button(action: { onTogglePin(entry) }) {
                        Image(systemName: pinnedIDs.contains(entry.pinID) ? "pin.slash.fill" : "pin.fill")
                            .font(.caption)
                    }
                    .buttonStyle(.plain)
                }
                Text([entry.ruleName, entry.chainName, entry.ruleAction].filter { !$0.isEmpty }.joined(separator: " / "))
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(12)
        .background(Color.secondary.opacity(0.05), in: RoundedRectangle(cornerRadius: 8))
    }
}

// MARK: - Logs

struct MacLogsSection: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 2) {
                    if model.dashboard.logs.isEmpty {
                        Text("No logs yet")
                            .foregroundStyle(.secondary)
                            .padding(20)
                    } else {
                        ForEach(Array(model.dashboard.logs.enumerated()), id: \.offset) { index, line in
                            Text(line)
                                .font(.system(.caption, design: .monospaced))
                                .foregroundStyle(.secondary)
                                .textSelection(.enabled)
                                .id(index)
                        }
                    }
                }
                .padding(12)
            }
            .onChange(of: model.dashboard.logs.count) {
                if !model.dashboard.logs.isEmpty {
                    proxy.scrollTo(model.dashboard.logs.count - 1, anchor: .bottom)
                }
            }
        }
    }
}

// MARK: - Settings

struct MacSettingsSection: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        AppSettingsView(model: model)
    }
}

// MARK: - License

struct MacLicenseSectionInline: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                ProductStatePanel(decision: model.licenseManager.decision)
                Divider()
                MacLicenseControls(manager: model.licenseManager)
            }
            .padding(20)
        }
    }
}

private struct MacLicenseControls: View {
    @ObservedObject var manager: MacLicenseManager
    @State private var licenseKey = ""
    @State private var email = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Label(deviceSummary, systemImage: "desktopcomputer")
                Spacer()
                Text("\(manager.deviceState.activeDeviceCount)/\(manager.deviceState.maxActiveDevices) active")
                    .foregroundStyle(.secondary)
            }

            SecureField("License key", text: $licenseKey)
                .textFieldStyle(.roundedBorder)
            TextField("Email", text: $email)
                .textFieldStyle(.roundedBorder)

            HStack(spacing: 10) {
                Button {
                    Task { await manager.activate(licenseKey: licenseKey, email: email) }
                } label: {
                    Label("Activate", systemImage: "checkmark.seal")
                }
                .disabled(manager.isLoading || licenseKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)

                Button(role: .destructive) {
                    Task { await manager.deactivateCurrentDevice() }
                } label: {
                    Label("Deactivate", systemImage: "minus.circle")
                }
                .disabled(manager.isLoading || !manager.deviceState.isCurrentDeviceActive)
            }

            HStack(spacing: 10) {
                Button {
                    Task { await manager.reactivateCurrentDevice() }
                } label: {
                    Label("Reactivate", systemImage: "arrow.clockwise.circle")
                }
                .disabled(manager.isLoading || !manager.deviceState.canReactivateCurrentDevice)

                Button {
                    Task { await manager.transferCurrentDevice() }
                } label: {
                    Label("Transfer", systemImage: "arrow.right.arrow.left")
                }
                .disabled(manager.isLoading || !manager.deviceState.canTransferCurrentDevice)
            }

            Link(destination: URL(string: "https://jpfchang.org/clambhook/buy")!) {
                Label("Buy ClambHook USD \(MobileLicenseCommercialTerms.lifetimePriceUSD)", systemImage: "cart")
            }

            if manager.isLoading {
                ProgressView()
            }

            if !manager.statusMessage.isEmpty {
                Text(manager.statusMessage)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
        .onAppear {
            licenseKey = manager.savedLicenseKey()
            email = manager.savedEmail()
        }
    }

    private var deviceSummary: String {
        if let device = manager.deviceState.currentDevice {
            return device.status == .active ? "\(device.displayName) is active" : "\(device.displayName) is deactivated"
        }
        return "This Mac is not activated"
    }
}

// MARK: - Helpers

@MainActor
private func dashboardFallbackProxyChain(_ dashboard: DashboardStore) -> String {
    for group in dashboard.policyGroups.groups {
        if !group.selectedChain.isEmpty { return group.selectedChain }
        if !group.selected.isEmpty { return group.selected }
    }
    return dashboard.servers.chains.first?.name ?? ""
}
