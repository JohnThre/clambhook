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
                    statusPanel
                    metricsPanel
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

    private var statusPanel: some View {
        MacSection(title: "Status") {
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
                    if daemon.state.isBusy {
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
                        model.connectOrDisconnect()
                    } label: {
                        Label(
                            model.dashboard.status.running ? "Disconnect" : "Connect",
                            systemImage: model.dashboard.status.running ? "stop.fill" : "play.fill"
                        )
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(!model.dashboard.apiOnline && !model.dashboard.status.running)
                }
            }
        }
    }

    private var metricsPanel: some View {
        let sample = model.dashboard.currentBandwidth
        let activeConnections = model.dashboard.status.listeners.reduce(0) { $0 + $1.activeConns }
        return LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 8) {
            MacMetricTile(title: "Down", value: formatRate(sample.rxBps), systemImage: "arrow.down")
            MacMetricTile(title: "Up", value: formatRate(sample.txBps), systemImage: "arrow.up")
            MacMetricTile(title: "Active", value: "\(activeConnections)", systemImage: "point.3.connected.trianglepath.dotted")
            MacMetricTile(title: "Listeners", value: "\(model.dashboard.status.listeners.count)", systemImage: "antenna.radiowaves.left.and.right")
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
                if daemon.isRunning {
                    model.stopDaemon()
                } else {
                    model.launchDaemon()
                }
            } label: {
                Label(daemon.isRunning ? "Stop Daemon" : "Launch Daemon", systemImage: daemon.isRunning ? "xmark.octagon" : "terminal")
            }
            .disabled(daemon.state.isBusy)
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

private struct MacRuleCreateSheet: View {
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
