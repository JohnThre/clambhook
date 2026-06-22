import AppKit
import ClambhookShared
import SwiftUI

struct MacMenuBarView: View {
    @ObservedObject var model: AppleAppModel
    @ObservedObject private var daemon: DaemonSupervisor
    @Environment(\.openSettings) private var openSettings
    @Environment(\.openWindow) private var openWindow
    @State private var trafficFilter = "all"
    @State private var trafficSearch = ""
    @State private var captureFilter: CaptureFilterKind = .all
    @State private var captureSearch = ""
    @State private var draftRule: RulePayload?
    @State private var sourceConnection: TrafficConnectionPayload?
    @State private var showLogbook = false
    @State private var showAnytime = false
    @State private var routeTestNetwork = "tcp"
    @State private var routeTestTarget = "example.com:443"
    @State private var routeTestResult: RuleTestResponse?
    @State private var routeTestError = ""

    init(model: AppleAppModel) {
        self.model = model
        self._daemon = ObservedObject(wrappedValue: model.daemonSupervisor)
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    quickConnectPanel
                    profilePolicyPanel
                    trafficRatePanel
                    recentBlockedPanel
                    DisclosureGroup("Activity", isExpanded: $showLogbook) {
                        VStack(alignment: .leading, spacing: 14) {
                            trafficPanel
                            developerCapturePanel
                            logsPanel
                        }
                        .padding(.top, 8)
                    }
                    DisclosureGroup("Profiles", isExpanded: $showAnytime) {
                        VStack(alignment: .leading, spacing: 14) {
                            profilesPanel
                            listenersPanel
                            serversPanel
                        }
                        .padding(.top, 8)
                    }
                }
                .padding(14)
            }
            Divider()
            footer
        }
        .sheet(item: $draftRule) { rule in
            MacRuleCreateSheet(model: model, initialRule: rule, sourceConnection: sourceConnection)
        }
    }

    private var header: some View {
        HStack(spacing: 10) {
            Image(systemName: model.dashboard.status.running ? "network" : "network.slash")
                .font(.title2)
                .foregroundStyle(model.dashboard.status.running ? .green : .secondary)
                .frame(width: 28)
            VStack(alignment: .leading, spacing: 2) {
                Text("clambhook")
                    .font(.headline)
                Text(model.dashboard.activeProfile.isEmpty ? "No active profile" : model.dashboard.activeProfile)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer()
            MacStatusPill(
                text: model.dashboard.apiOnline ? "API online" : "API offline",
                systemImage: "network",
                tint: model.dashboard.apiOnline ? .green : .red
            )
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
    }

    private var quickConnectPanel: some View {
        MacSection(title: "Quick Connect") {
            VStack(alignment: .leading, spacing: 10) {
                HStack(spacing: 8) {
                    MacStatusPill(
                        text: model.dashboard.status.running ? "Connected" : "Disconnected",
                        systemImage: model.dashboard.status.running ? "checkmark.circle.fill" : "pause.circle",
                        tint: model.dashboard.status.running ? .green : .secondary
                    )
                    MacStatusPill(
                        text: daemon.state.label,
                        systemImage: daemonIcon,
                        tint: daemonTint
                    )
                    if model.settingsStore.settings.normalized().usePrivilegedHelper {
                        MacStatusPill(
                            text: model.privilegedHelperManager.serviceStatus.label,
                            systemImage: privilegedHelperIcon,
                            tint: privilegedHelperTint
                        )
                    }
                    if daemon.state.isBusy || model.privilegedHelperManager.isWorking {
                        ProgressView()
                            .controlSize(.small)
                            .scaleEffect(0.75)
                    }
                    Spacer()
                }
                if !model.dashboard.errorText.isEmpty {
                    Text(model.dashboard.errorText)
                        .font(.caption)
                        .foregroundStyle(.red)
                        .lineLimit(3)
                }
                if !model.daemonMessage.isEmpty {
                    Text(model.daemonMessage)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
                HStack(spacing: 8) {
                    Button {
                        performQuickConnect()
                    } label: {
                        Label(
                            quickConnectTitle,
                            systemImage: quickConnectIcon
                        )
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(quickConnectDisabled)
                    if model.dashboard.apiOnline {
                        Button {
                            model.refresh()
                        } label: {
                            Label("Refresh", systemImage: "arrow.clockwise")
                        }
                        .buttonStyle(.bordered)
                    }
                    Spacer()
                    Text(connectionSummaryText)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
        }
    }

    private var profilePolicyPanel: some View {
        MacSection(title: "Current Profile & Active Policy") {
            VStack(alignment: .leading, spacing: 10) {
                HStack(spacing: 10) {
                    Label("Profile", systemImage: "person.crop.square")
                        .font(.caption.weight(.medium))
                        .foregroundStyle(.secondary)
                    Spacer(minLength: 8)
                    if model.dashboard.profiles.profiles.isEmpty {
                        Text("No profiles")
                            .font(.caption)
                            .foregroundStyle(.secondary)
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
                        .frame(maxWidth: 220, alignment: .trailing)
                    }
                }

                if visiblePolicyGroups.isEmpty {
                    HStack(spacing: 8) {
                        Image(systemName: "arrow.triangle.branch")
                            .foregroundStyle(.secondary)
                            .frame(width: 18)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(defaultPolicyTitle)
                                .font(.caption.weight(.semibold))
                                .lineLimit(1)
                            Text("Static route")
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                    }
                } else {
                    VStack(alignment: .leading, spacing: 8) {
                        ForEach(visiblePolicyGroups.prefix(3)) { group in
                            MacMenuPolicyRow(
                                group: group,
                                selected: selectedPolicyChain(group),
                                canSelect: canSelectPolicy(group),
                                onSelect: { chain in
                                    model.selectPolicyGroup(group: group.name, chain: chain)
                                },
                                onTest: {
                                    Task { await model.dashboard.testPolicyGroup(group: group.name) }
                                }
                            )
                        }
                    }
                }
            }
        }
    }

    private var trafficRatePanel: some View {
        let sample = currentTrafficRate
        let activeConnections = model.dashboard.status.listeners.reduce(0) { $0 + $1.activeConns }
        return MacSection(title: "Traffic Rate") {
            LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 8) {
                MacMetricTile(title: "Down", value: formatRate(sample.rxBps), systemImage: "arrow.down")
                MacMetricTile(title: "Up", value: formatRate(sample.txBps), systemImage: "arrow.up")
                MacMetricTile(title: "Active", value: "\(activeConnections)", systemImage: "point.3.connected.trianglepath.dotted")
                MacMetricTile(title: "Blocked", value: "\(model.dashboard.monitorActionCounts["block", default: 0])", systemImage: "hand.raised.fill")
            }
        }
    }

    private var recentBlockedPanel: some View {
        MacSection(title: "Recent Blocks & Rule Actions") {
            VStack(alignment: .leading, spacing: 10) {
                if recentBlockedConnections.isEmpty && unmatchedBlockDecisions.isEmpty {
                    MacEmptyRow(text: "No recent blocked requests")
                } else {
                    ForEach(recentBlockedConnections.prefix(3)) { connection in
                        MacBlockedRequestRow(
                            connection: connection,
                            fallbackChain: fallbackProxyChain,
                            onTemporaryAction: { action in
                                model.createTemporaryRuleFromConnection(connection, action: action)
                            },
                            onRule: { rule in
                                sourceConnection = connection
                                draftRule = rule
                            }
                        )
                    }
                    ForEach(Array(unmatchedBlockDecisions.prefix(max(0, 3 - recentBlockedConnections.count)).enumerated()), id: \.offset) { _, decision in
                        MacBlockedDecisionRow(decision: decision)
                    }
                }
            }
        }
    }

    private var profilesPanel: some View {
        MacSection(title: "Profiles") {
            if model.dashboard.profiles.profiles.isEmpty {
                MacEmptyRow(text: "No profiles")
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
            }
        }
    }

    private var listenersPanel: some View {
        MacSection(title: "Listeners") {
            if model.dashboard.status.listeners.isEmpty {
                MacEmptyRow(text: "None active")
            } else {
                VStack(spacing: 8) {
                    ForEach(model.dashboard.status.listeners) { listener in
                        HStack(spacing: 8) {
                            Image(systemName: "antenna.radiowaves.left.and.right")
                                .foregroundStyle(.secondary)
                                .frame(width: 18)
                            VStack(alignment: .leading, spacing: 2) {
                                Text(listener.protocol.uppercased())
                                    .font(.caption.weight(.semibold))
                                Text(listener.addr)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                    .lineLimit(1)
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

    private var serversPanel: some View {
        let rows = serverRows.prefix(5)
        return MacSection(title: "Servers") {
            if rows.isEmpty {
                MacEmptyRow(text: "No servers in active profile")
            } else {
                VStack(spacing: 8) {
                    ForEach(Array(rows), id: \.id) { row in
                        HStack(spacing: 8) {
                            Text(countryFlag(row.server.geo.countryCode))
                                .frame(width: 22)
                            VStack(alignment: .leading, spacing: 2) {
                                Text(row.server.name)
                                    .font(.caption.weight(.semibold))
                                    .lineLimit(1)
                                Text("\(row.server.protocol) / \(serverLocation(row.server))")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                    .lineLimit(1)
                            }
                            Spacer()
                            VStack(alignment: .trailing, spacing: 2) {
                                Text(row.chain)
                                    .font(.caption)
                                Text(udpSupportText(row.capabilities))
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
        }
    }

    private var trafficPanel: some View {
        let rows = filteredTraffic
        return MacSection(title: "Historical Traffic") {
            VStack(alignment: .leading, spacing: 8) {
                Text("\(model.dashboard.traffic.summary.activeConnections) active / \(formatBytes(model.dashboard.traffic.summary.rxTotal)) down / \(formatBytes(model.dashboard.traffic.summary.txTotal)) up")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                Picker("Action", selection: $trafficFilter) {
                    Text("All").tag("all")
                    Text("Proxy \(model.dashboard.monitorActionCounts["proxy", default: 0])").tag("proxy")
                    Text("Direct \(model.dashboard.monitorActionCounts["direct", default: 0])").tag("direct")
                    Text("Block \(model.dashboard.monitorActionCounts["block", default: 0])").tag("block")
                }
                .labelsHidden()
                .pickerStyle(.segmented)
                TextField("Search hosts, rules, chains", text: $trafficSearch)
                    .textFieldStyle(.roundedBorder)
                VStack(alignment: .leading, spacing: 6) {
                    HStack(spacing: 8) {
                        Picker("Network", selection: $routeTestNetwork) {
                            Text("TCP").tag("tcp")
                            Text("UDP").tag("udp")
                        }
                        .labelsHidden()
                        .pickerStyle(.segmented)
                        TextField("host:port", text: $routeTestTarget)
                            .textFieldStyle(.roundedBorder)
                        Button {
                            runRouteTest()
                        } label: {
                            Label("Test", systemImage: "checkmark.circle")
                        }
                    }
                    if !routeTestError.isEmpty {
                        Text(routeTestError)
                            .font(.caption)
                            .foregroundStyle(.red)
                            .lineLimit(2)
                    } else if let routeTestResult {
                        Text(routeTestSummary(routeTestResult))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(2)
                    }
                }
                if !model.dashboard.ruleHitSummaries.isEmpty {
                    Text("Rule hits " + model.dashboard.ruleHitSummaries.prefix(3).map { "\($0.ruleName.isEmpty ? "Default" : $0.ruleName): \($0.count)" }.joined(separator: "  "))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }
                if !model.dashboard.traffic.summary.persistError.isEmpty {
                    Text(model.dashboard.traffic.summary.persistError)
                        .font(.caption)
                        .foregroundStyle(.red)
                        .lineLimit(2)
                }
                if rows.isEmpty {
                    MacEmptyRow(text: "No traffic history")
                } else {
                    ForEach(rows.prefix(5)) { connection in
                        VStack(alignment: .leading, spacing: 2) {
                            HStack {
                                Text(emptyDash(connection.target))
                                    .font(.caption.weight(.semibold))
                                    .lineLimit(1)
                                Spacer()
                                Text(connection.actionFamily.uppercased())
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                            Text("\(trafficSubtitle(connection)) / \(formatBytes(connection.rxTotal)) down / \(formatBytes(connection.txTotal)) up")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                            Button {
                                sourceConnection = connection
                                draftRule = connection.ruleDraft()
                            } label: {
                                Label("Create Rule from Connection", systemImage: "plus.circle")
                            }
                            .buttonStyle(.plain)
                            .font(.caption)
                            .disabled(connection.ruleDraft() == nil)
                        }
                    }
                }
            }
        }
    }

    private var developerCapturePanel: some View {
        let entries = filteredCaptureEntries
        let groups = CaptureSupport.groupEntriesByHost(entries, pinnedIDs: model.pinnedConnectionIDs)
        return MacSection(title: "HTTP Capture") {
            VStack(alignment: .leading, spacing: 8) {
                HStack(spacing: 8) {
                    MacStatusPill(
                        text: "\(captureEntries.count) metadata requests",
                        systemImage: "list.bullet.rectangle",
                        tint: captureEntries.isEmpty ? .secondary : .blue
                    )
                    MacStatusPill(
                        text: "\(groups.count) hosts",
                        systemImage: "rectangle.stack",
                        tint: groups.isEmpty ? .secondary : .green
                    )
                    Spacer()
                    ShareLink(
                        item: CaptureSupport.exportString(
                            traffic: model.dashboard.traffic,
                            entries: entries
                        ),
                        subject: Text("ClambHook HTTP metadata export"),
                        message: Text("Local metadata-only JSON export.")
                    ) {
                        Image(systemName: "square.and.arrow.up")
                    }
                    .disabled(entries.isEmpty)
                }
                Picker("Capture", selection: $captureFilter) {
                    Text("All").tag(CaptureFilterKind.all)
                    Text("HTTP").tag(CaptureFilterKind.http)
                    Text("HTTPS").tag(CaptureFilterKind.https)
                    Text("Pinned").tag(CaptureFilterKind.pinned)
                }
                .labelsHidden()
                .pickerStyle(.segmented)
                TextField("Search method, host, path, rule", text: $captureSearch)
                    .textFieldStyle(.roundedBorder)
                if groups.isEmpty {
                    MacEmptyRow(text: "No HTTP metadata")
                } else {
                    VStack(alignment: .leading, spacing: 10) {
                        ForEach(groups.prefix(4)) { group in
                            MacCaptureGroupView(
                                group: group,
                                pinnedIDs: model.pinnedConnectionIDs,
                                onTogglePin: toggleCapturePin
                            )
                        }
                    }
                }
                Text("HTTPS rows remain CONNECT metadata unless opt-in HTTPS Body Capture is enabled in developer config.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
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

    private func toggleCapturePin(_ entry: CaptureMetadataEntryPayload) {
        var ids = model.pinnedConnectionIDs
        if ids.contains(entry.pinID) {
            ids.remove(entry.pinID)
        } else {
            ids.insert(entry.pinID)
        }
        model.settingsStore.settings.pinnedConnectionIDs = ids.sorted()
    }

    private var filteredTraffic: [TrafficConnectionPayload] {
        let query = trafficSearch.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return model.dashboard.traffic.connections.filter { connection in
            (trafficFilter == "all" || connection.actionFamily == trafficFilter)
            && (query.isEmpty || [
                connection.target,
                connection.monitorHost,
                connection.ruleName,
                connection.ruleAction,
                connection.chainName,
                connection.application,
                connection.network,
            ].contains { $0.lowercased().contains(query) })
        }
    }

    private var logsPanel: some View {
        MacSection(title: "Logs") {
            if model.dashboard.logs.isEmpty {
                MacEmptyRow(text: "No logs yet")
            } else {
                VStack(alignment: .leading, spacing: 5) {
                    ForEach(Array(model.dashboard.logs.suffix(6).enumerated()), id: \.offset) { _, line in
                        Text(line)
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(.secondary)
                            .lineLimit(2)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
            }
        }
    }

    private var footer: some View {
        HStack(spacing: 8) {
            Button {
                if managedDaemonRunning {
                    model.stopDaemon()
                } else {
                    model.launchDaemon()
                }
            } label: {
                Label(managedDaemonRunning ? "Stop Daemon" : "Launch Daemon", systemImage: managedDaemonRunning ? "xmark.octagon" : "terminal")
            }
            .disabled(daemon.state.isBusy || model.privilegedHelperManager.isWorking)
            Button {
                NSApp.setActivationPolicy(.regular)
                NSApp.activate(ignoringOtherApps: true)
                openWindow(id: "dashboard")
            } label: {
                Label("Open Window", systemImage: "macwindow")
            }
            Spacer()
            Button {
                openSettings()
            } label: {
                Label("Settings", systemImage: "gear")
            }
            Button {
                model.stop()
                NSApplication.shared.terminate(nil)
            } label: {
                Label("Quit", systemImage: "power")
            }
        }
        .padding(12)
    }

    private var daemonIcon: String {
        switch daemon.state {
        case .stopped:
            return "terminal"
        case .starting:
            return "hourglass"
        case .running:
            return "checkmark.circle.fill"
        case .stopping:
            return "hourglass"
        case .failed:
            return "exclamationmark.triangle.fill"
        }
    }

    private var daemonTint: Color {
        switch daemon.state {
        case .running:
            return .green
        case .failed:
            return .red
        case .starting, .stopping:
            return .orange
        case .stopped:
            return .secondary
        }
    }

    private var managedDaemonRunning: Bool {
        daemon.isRunning || model.privilegedHelperManager.daemonRunning
    }

    private var shouldLaunchDaemonForQuickConnect: Bool {
        let settings = model.settingsStore.settings.normalized()
        return settings.routingMode == .daemonProxy &&
            !model.dashboard.apiOnline &&
            !managedDaemonRunning
    }

    private var quickConnectTitle: String {
        if shouldLaunchDaemonForQuickConnect {
            return "Launch Daemon"
        }
        return model.dashboard.status.running ? "Disconnect" : "Connect"
    }

    private var quickConnectIcon: String {
        if shouldLaunchDaemonForQuickConnect {
            return "terminal"
        }
        return model.dashboard.status.running ? "stop.fill" : "play.fill"
    }

    private var quickConnectDisabled: Bool {
        if shouldLaunchDaemonForQuickConnect {
            return daemon.state.isBusy || model.privilegedHelperManager.isWorking
        }
        return !model.dashboard.apiOnline && !model.dashboard.status.running
    }

    private func performQuickConnect() {
        if shouldLaunchDaemonForQuickConnect {
            model.launchDaemon()
        } else {
            model.connectOrDisconnect()
        }
    }

    private var privilegedHelperIcon: String {
        switch model.privilegedHelperManager.serviceStatus {
        case .enabled:
            return "checkmark.circle.fill"
        case .requiresApproval:
            return "exclamationmark.triangle.fill"
        case .notRegistered:
            return "lock.shield"
        case .notFound, .unknown:
            return "questionmark.circle"
        }
    }

    private var privilegedHelperTint: Color {
        switch model.privilegedHelperManager.serviceStatus {
        case .enabled:
            return .green
        case .requiresApproval:
            return .orange
        case .notFound, .unknown:
            return .red
        case .notRegistered:
            return .secondary
        }
    }

    private var connectionSummaryText: String {
        let activeConnections = model.dashboard.status.listeners.reduce(0) { $0 + $1.activeConns }
        return "\(activeConnections) active / \(model.dashboard.status.listeners.count) listeners"
    }

    private var currentTrafficRate: BandwidthSample {
        let eventSample = model.dashboard.currentBandwidth
        if eventSample.rxBps > 0 || eventSample.txBps > 0 {
            return eventSample
        }
        return BandwidthSample(
            rxBps: model.dashboard.traffic.summary.rxBps,
            txBps: model.dashboard.traffic.summary.txBps
        )
    }

    private var visiblePolicyGroups: [PolicyGroupPayload] {
        model.dashboard.policyGroups.groups.filter { !$0.hidden }
    }

    private var defaultPolicyTitle: String {
        fallbackProxyChain.isEmpty ? "No route selected" : fallbackProxyChain
    }

    private func selectedPolicyChain(_ group: PolicyGroupPayload) -> String {
        if !group.selectedChain.isEmpty {
            return group.selectedChain
        }
        if !group.selected.isEmpty {
            return group.selected
        }
        return group.chains.first ?? ""
    }

    private func canSelectPolicy(_ group: PolicyGroupPayload) -> Bool {
        group.type.caseInsensitiveCompare("select") == .orderedSame ||
            group.selectionMode.caseInsensitiveCompare("manual") == .orderedSame
    }

    private var fallbackProxyChain: String {
        for group in visiblePolicyGroups {
            let selected = selectedPolicyChain(group)
            if !selected.isEmpty {
                return selected
            }
        }
        return model.dashboard.servers.chains.first?.name ?? ""
    }

    private var recentBlockedConnections: [TrafficConnectionPayload] {
        model.dashboard.traffic.connections
            .filter { $0.actionFamily == "block" }
            .sorted { $0.updatedTsNs > $1.updatedTsNs }
    }

    private var unmatchedBlockDecisions: [TrafficBlockDecisionPayload] {
        let connectionIDs = Set(recentBlockedConnections.map(\.connID))
        return model.dashboard.traffic.blockDecisions
            .filter { $0.connID.isEmpty || !connectionIDs.contains($0.connID) }
            .sorted { $0.tsNs > $1.tsNs }
    }

    private var serverRows: [ServerRow] {
        model.dashboard.servers.chains.flatMap { chain in
            chain.servers.map { ServerRow(chain: chain.name, capabilities: chain.capabilities, server: $0) }
        }
    }

    private func trafficSubtitle(_ connection: TrafficConnectionPayload) -> String {
        let parts = [connection.application, connection.network, connection.chainName]
            .filter { !$0.isEmpty }
        if !parts.isEmpty {
            return parts.joined(separator: " / ")
        }
        return connection.listener.protocol
    }

    private func runRouteTest() {
        routeTestError = ""
        Task {
            do {
                routeTestResult = try await model.testRule(network: routeTestNetwork, target: routeTestTarget)
            } catch {
                routeTestResult = nil
                routeTestError = error.localizedDescription
            }
        }
    }
}

private struct ServerRow: Identifiable {
    var id: String { "\(chain)-\(server.id)" }
    var chain: String
    var capabilities: ProtocolCapabilitiesPayload
    var server: ServerPayload
}

private struct MacMenuPolicyRow: View {
    var group: PolicyGroupPayload
    var selected: String
    var canSelect: Bool
    var onSelect: (String) -> Void
    var onTest: () -> Void

    private var pickerSelection: String {
        selected.isEmpty ? (group.chains.first ?? "") : selected
    }

    var body: some View {
        HStack(alignment: .center, spacing: 8) {
            Image(systemName: "point.3.connected.trianglepath.dotted")
                .foregroundStyle(.secondary)
                .frame(width: 18)
            VStack(alignment: .leading, spacing: 2) {
                Text(group.name.isEmpty ? "Policy group" : group.name)
                    .font(.caption.weight(.semibold))
                    .lineLimit(1)
                Text(policySubtitle)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 8)
            MacStatusPill(text: healthText, systemImage: healthIcon, tint: healthTint)
            Button(action: onTest) {
                Image(systemName: "bolt.horizontal")
            }
            .buttonStyle(.plain)
            .help("Test \(group.name)")
            if canSelect && !group.chains.isEmpty {
                Picker("Chain", selection: Binding(
                    get: { pickerSelection },
                    set: { onSelect($0) }
                )) {
                    ForEach(group.chains, id: \.self) { chain in
                        Text(chain).tag(chain)
                    }
                }
                .labelsHidden()
                .pickerStyle(.menu)
                .frame(width: 118, alignment: .trailing)
            } else {
                Text(emptyDash(selected))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .frame(width: 118, alignment: .trailing)
            }
        }
    }

    private var policySubtitle: String {
        let mode = group.selectionMode.isEmpty ? group.type : group.selectionMode
        let route = selected.isEmpty ? "No chain selected" : "selected \(selected)"
        if mode.isEmpty {
            return route
        }
        return "\(mode.replacingOccurrences(of: "-", with: " ")) / \(route)"
    }

    private var selectedResult: PolicyProbeResultPayload? {
        group.results.first { $0.chainName == selected }
    }

    private var healthText: String {
        guard !group.results.isEmpty else {
            return "Pending"
        }
        guard let selectedResult else {
            return "Unknown"
        }
        if selectedResult.healthy {
            return selectedResult.latencyNs > 0 ? formatDurationNs(selectedResult.latencyNs) : "Healthy"
        }
        return "Fallback"
    }

    private var healthIcon: String {
        guard !group.results.isEmpty else {
            return "clock"
        }
        return selectedResult?.healthy == true ? "checkmark.circle.fill" : "exclamationmark.triangle.fill"
    }

    private var healthTint: Color {
        guard !group.results.isEmpty else {
            return .secondary
        }
        return selectedResult?.healthy == true ? .green : .orange
    }
}

private struct MacBlockedRequestRow: View {
    var connection: TrafficConnectionPayload
    var fallbackChain: String
    var onTemporaryAction: (String) -> Void
    var onRule: (RulePayload) -> Void

    private var canCreateTemporaryRule: Bool {
        !connection.connID.isEmpty && !connection.monitorHost.isEmpty
    }

    private var hostLabel: String {
        let host = connection.targetHost.isEmpty ? connection.target : connection.targetHost
        if !connection.targetPort.isEmpty && connection.targetPort != "0" {
            return "\(host):\(connection.targetPort)"
        }
        return host
    }

    private var proxyAction: String {
        connection.temporaryProxyAction(fallbackChain: fallbackChain)
    }

    private var allowRule: RulePayload? {
        connection.ruleDraft(actionOverride: "direct") ?? connection.ruleDraft()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(emptyDash(hostLabel))
                    .font(.caption.weight(.semibold))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Spacer(minLength: 8)
                Text("BLOCKED")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(.red)
            }
            Text(subtitle)
                .font(.caption2)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            ViewThatFits(in: .horizontal) {
                HStack(spacing: 8) {
                    actionButtons
                }
                VStack(alignment: .leading, spacing: 6) {
                    actionButtons
                }
            }
            .font(.caption)
        }
        .padding(.vertical, 2)
    }

    private var subtitle: String {
        let decision = [connection.ruleName, connection.ruleAction]
            .filter { !$0.isEmpty }
            .joined(separator: " / ")
        let parts = [connection.application, connection.network.uppercased(), decision]
            .filter { !$0.isEmpty }
        return parts.isEmpty ? "Blocked request" : parts.joined(separator: " / ")
    }

    private var actionButtons: some View {
        Group {
            Button {
                onTemporaryAction("allow")
            } label: {
                Label("Allow", systemImage: "checkmark.shield")
            }
            .disabled(!canCreateTemporaryRule)

            Button {
                onTemporaryAction("direct")
            } label: {
                Label("Direct", systemImage: "arrow.up.right")
            }
            .disabled(!canCreateTemporaryRule)

            Button {
                if !proxyAction.isEmpty {
                    onTemporaryAction(proxyAction)
                }
            } label: {
                Label("Proxy", systemImage: "shield.lefthalf.filled")
            }
            .disabled(!canCreateTemporaryRule || proxyAction.isEmpty)

            Button {
                if let allowRule {
                    onRule(allowRule)
                }
            } label: {
                Label("Rule", systemImage: "plus.circle")
            }
            .disabled(allowRule == nil)
        }
        .buttonStyle(.borderless)
    }
}

private struct MacBlockedDecisionRow: View {
    var decision: TrafficBlockDecisionPayload

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "hand.raised.fill")
                .foregroundStyle(.red)
                .frame(width: 18)
            VStack(alignment: .leading, spacing: 2) {
                Text(emptyDash(target))
                    .font(.caption.weight(.semibold))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text(subtitle)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 8)
        }
    }

    private var target: String {
        if !decision.targetHost.isEmpty {
            if !decision.targetPort.isEmpty && decision.targetPort != "0" {
                return "\(decision.targetHost):\(decision.targetPort)"
            }
            return decision.targetHost
        }
        return decision.target
    }

    private var subtitle: String {
        let parts = [decision.profile, decision.network.uppercased(), decision.ruleName, decision.action]
            .filter { !$0.isEmpty }
        return parts.isEmpty ? "Blocked request" : parts.joined(separator: " / ")
    }
}

private struct MacSection<Content: View>: View {
    var title: String
    var content: Content

    init(title: String, @ViewBuilder content: () -> Content) {
        self.title = title
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title)
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .textCase(.uppercase)
            content
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

private struct MacStatusPill: View {
    var text: String
    var systemImage: String
    var tint: Color

    var body: some View {
        Label(text, systemImage: systemImage)
            .font(.caption.weight(.medium))
            .foregroundStyle(tint)
            .lineLimit(1)
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .background(tint.opacity(0.12), in: RoundedRectangle(cornerRadius: 8))
    }
}

private struct MacMetricTile: View {
    var title: String
    var value: String
    var systemImage: String

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: systemImage)
                .foregroundStyle(.secondary)
                .frame(width: 18)
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                Text(value)
                    .font(.caption.weight(.semibold))
                    .monospacedDigit()
                    .lineLimit(1)
            }
            Spacer(minLength: 0)
        }
        .padding(10)
        .background(Color.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
    }
}

private struct MacEmptyRow: View {
    var text: String

    var body: some View {
        Text(text)
            .font(.caption)
            .foregroundStyle(.secondary)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}

private struct MacCaptureGroupView: View {
    var group: CaptureGroupPayload
    var pinnedIDs: Set<String>
    var onTogglePin: (CaptureMetadataEntryPayload) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(emptyDash(group.host))
                    .font(.caption.weight(.semibold))
                    .lineLimit(1)
                Spacer()
                Text(groupSubtitle)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            ForEach(group.entries.prefix(3)) { entry in
                MacCaptureEntryRow(
                    entry: entry,
                    pinned: pinnedIDs.contains(entry.pinID),
                    onTogglePin: { onTogglePin(entry) }
                )
            }
        }
    }

    private var groupSubtitle: String {
        let schemes = group.schemes.map { $0.uppercased() }.joined(separator: ", ")
        if schemes.isEmpty {
            return "\(group.count)"
        }
        return "\(group.count) / \(schemes)"
    }
}

private struct MacCaptureEntryRow: View {
    var entry: CaptureMetadataEntryPayload
    var pinned: Bool
    var onTogglePin: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack(alignment: .firstTextBaseline, spacing: 6) {
                Text(entry.method.isEmpty ? "--" : entry.method)
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(entry.scheme.lowercased() == "https" ? .blue : .green)
                    .frame(minWidth: 46, alignment: .leading)
                Text(emptyDash(entry.displayTarget))
                    .font(.caption.weight(.medium))
                    .lineLimit(1)
                Spacer(minLength: 6)
                Button(action: onTogglePin) {
                    Image(systemName: pinned ? "pin.slash.fill" : "pin.fill")
                        .font(.caption)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(pinned ? "Unpin metadata row" : "Pin metadata row")
            }
            Text([entry.ruleName, entry.chainName, entry.ruleAction].filter { !$0.isEmpty }.joined(separator: " / "))
                .font(.caption2)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            Text("\(formatBytes(entry.rxTotal)) down / \(formatBytes(entry.txTotal)) up / \(entry.sslState.replacingOccurrences(of: "_", with: " "))")
                .font(.caption2)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            if let event = entry.timeline.last {
                Text("Last \(emptyDash(event.title)) \(event.detail)")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, 4)
    }
}

struct MacRuleCreateSheet: View {
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
        VStack(alignment: .leading, spacing: 14) {
            Text("Create Rule")
                .font(.headline)
            TextField("Name", text: $rule.name)
                .textFieldStyle(.roundedBorder)
            Picker("Action", selection: $rule.action) {
                Text("Block").tag("block")
                Text("Direct").tag("direct")
                ForEach(model.dashboard.servers.chains, id: \.name) { chain in
                    Text("Proxy: \(chain.name)").tag("chain:\(chain.name)")
                }
            }
            Text("Match: \(rule.domains.first ?? rule.cidrs.first ?? "--")")
                .font(.caption)
                .foregroundStyle(.secondary)
            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                Button("Save") {
                    if let sourceConnection {
                        model.createRuleFromConnection(sourceConnection, rule: rule)
                    } else {
                        model.createRule(rule)
                    }
                    dismiss()
                }
                .buttonStyle(.borderedProminent)
                .disabled(rule.name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 360)
    }
}
