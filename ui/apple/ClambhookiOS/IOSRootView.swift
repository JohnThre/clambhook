import ClambhookShared
import AVFoundation
import SwiftUI
import UniformTypeIdentifiers
import UIKit

struct IOSRootView: View {
    @ObservedObject var model: AppleAppModel
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass
    @State private var selectedDestination: IOSDashboardDestination = .overview
    @State private var showingSettings = false
    @State private var showingOnboarding = false
    @AppStorage("org.jpfchang.clambhook.onboardingComplete") private var onboardingComplete = false

    var body: some View {
        Group {
            if horizontalSizeClass == .regular {
                splitView
            } else {
                tabView
            }
        }
        .sheet(isPresented: $showingSettings) {
            NavigationStack {
                AppSettingsView(model: model)
                    .navigationTitle("Settings")
                    .toolbar {
                        ToolbarItem(placement: .topBarTrailing) {
                            Button("Done") {
                                showingSettings = false
                            }
                        }
                    }
            }
        }
        .fullScreenCover(isPresented: $showingOnboarding) {
            IOSOnboardingView(model: model) {
                onboardingComplete = true
                showingOnboarding = false
                model.refresh()
            }
        }
        .task {
            if !onboardingComplete || model.shouldShowOnboarding() {
                showingOnboarding = true
            }
        }
    }

    private var tabView: some View {
        TabView(selection: $selectedDestination) {
            ForEach(IOSDashboardDestination.allCases) { destination in
                NavigationStack {
                    destinationView(destination)
                        .navigationTitle(destination.title)
                        .toolbar {
                            settingsToolbarItem
                        }
                }
                .tabItem {
                    Label(destination.title, systemImage: destination.systemImage)
                }
                .tag(destination)
            }
        }
    }

    private var splitView: some View {
        NavigationSplitView {
            List {
                Section("Monitoring") {
                    ForEach(IOSDashboardDestination.allCases) { destination in
                        Button {
                            selectedDestination = destination
                        } label: {
                            HStack {
                                Label(destination.title, systemImage: destination.systemImage)
                                Spacer()
                                if destination == selectedDestination {
                                    Image(systemName: "checkmark")
                                        .foregroundStyle(.tint)
                                }
                            }
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
            .navigationTitle("clambhook")
        } detail: {
            NavigationStack {
                destinationView(selectedDestination)
                    .navigationTitle(selectedDestination.title)
                    .toolbar {
                        settingsToolbarItem
                    }
            }
        }
    }

    @ToolbarContentBuilder
    private var settingsToolbarItem: some ToolbarContent {
        ToolbarItem(placement: .topBarTrailing) {
            Button {
                showingSettings = true
            } label: {
                Image(systemName: "gearshape")
            }
            .accessibilityLabel("Settings")
        }
    }

    @ViewBuilder
    private func destinationView(_ destination: IOSDashboardDestination) -> some View {
        switch destination {
        case .overview:
            IOSOperationsOverviewView(model: model)
        case .traffic:
            IOSOperationsTrafficView(model: model)
        case .servers:
            IOSOperationsServersView(model: model)
        case .logs:
            IOSOperationsLogsView(model: model)
        }
    }
}

private enum IOSDashboardDestination: String, CaseIterable, Identifiable, Hashable {
    case overview
    case traffic
    case servers
    case logs

    var id: Self { self }

    var title: String {
        switch self {
        case .overview:
            return "Overview"
        case .traffic:
            return "Traffic"
        case .servers:
            return "Servers"
        case .logs:
            return "Logs"
        }
    }

    var systemImage: String {
        switch self {
        case .overview:
            return "gauge.with.dots.needle.67percent"
        case .traffic:
            return "point.3.connected.trianglepath.dotted"
        case .servers:
            return "server.rack"
        case .logs:
            return "doc.text.magnifyingglass"
        }
    }
}

private struct IOSOverviewView: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        List {
            Section {
                IOSStatusPanel(model: model)
            }
            Section("Metrics") {
                IOSMetricsGrid(metrics: overviewMetrics)
                    .listRowInsets(EdgeInsets(top: 10, leading: 16, bottom: 10, trailing: 16))
            }
            Section("Active Profile") {
                IOSProfileControl(model: model)
            }
            Section("Listeners") {
                if model.dashboard.status.listeners.isEmpty {
                    IOSInlineEmptyState(text: "No listeners are active.", systemImage: "antenna.radiowaves.left.and.right.slash")
                } else {
                    ForEach(model.dashboard.status.listeners) { listener in
                        IOSListenerRow(listener: listener)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .refreshable {
            await model.refreshNow()
        }
    }

    private var overviewMetrics: [IOSMetric] {
        let sample = model.dashboard.currentBandwidth
        let activeConnections = model.dashboard.status.listeners.reduce(0) { $0 + $1.activeConns }
        return [
            IOSMetric(title: "Down", value: formatRate(sample.rxBps), systemImage: "arrow.down"),
            IOSMetric(title: "Up", value: formatRate(sample.txBps), systemImage: "arrow.up"),
            IOSMetric(title: "Active", value: "\(activeConnections)", systemImage: "bolt.horizontal.circle"),
            IOSMetric(title: "Listeners", value: "\(model.dashboard.status.listeners.count)", systemImage: "antenna.radiowaves.left.and.right"),
        ]
    }
}

private struct IOSStatusPanel: View {
    @ObservedObject var model: AppleAppModel
    @AppStorage("org.jpfchang.clambhook.vpnDisclosureAccepted") private var vpnDisclosureAccepted = false
    @State private var showingVPNDisclosure = false

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(alignment: .center, spacing: 12) {
                ZStack {
                    Circle()
                        .fill(connectionTint.opacity(0.14))
                    Image(systemName: model.dashboard.status.running ? "network" : "network.slash")
                        .font(.title3)
                        .foregroundStyle(connectionTint)
                }
                .frame(width: 44, height: 44)

                VStack(alignment: .leading, spacing: 3) {
                    Text(model.dashboard.status.running ? "Connected" : "Disconnected")
                        .font(.headline)
                    Text(emptyDash(model.dashboard.activeProfile))
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }

                Spacer(minLength: 12)

                VStack(alignment: .trailing, spacing: 6) {
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
                }
            }

            if !model.dashboard.errorText.isEmpty {
                Label(model.dashboard.errorText, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.red)
                    .lineLimit(3)
            }

            ViewThatFits(in: .horizontal) {
                HStack(spacing: 10) {
                    actionButtons
                }
                VStack(spacing: 10) {
                    actionButtons
                }
            }
        }
        .padding(.vertical, 2)
        .sheet(isPresented: $showingVPNDisclosure) {
            IOSVPNDisclosureSheet {
                vpnDisclosureAccepted = true
                model.connectOrDisconnect()
            }
        }
    }

    private var actionButtons: some View {
        Group {
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
            .disabled(!model.dashboard.apiOnline && !model.dashboard.status.running)

            Button {
                model.refresh()
            } label: {
                Label("Refresh", systemImage: "arrow.clockwise")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.bordered)
        }
        .controlSize(.large)
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

private struct IOSProfileControl: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        if model.dashboard.profiles.profiles.isEmpty {
            IOSInlineEmptyState(text: "No profiles are available.", systemImage: "person.crop.rectangle.stack")
        } else {
            HStack(spacing: 12) {
                Label {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(emptyDash(model.dashboard.activeProfile))
                            .font(.body.weight(.medium))
                            .lineLimit(1)
                        Text("\(model.dashboard.profiles.profiles.count) profiles")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                } icon: {
                    Image(systemName: "person.crop.rectangle.stack")
                        .foregroundStyle(.secondary)
                }

                Spacer(minLength: 8)

                Menu {
                    ForEach(model.dashboard.profiles.profiles, id: \.self) { profile in
                        Button {
                            model.selectProfile(profile)
                        } label: {
                            if profile == model.dashboard.activeProfile {
                                Label(profile, systemImage: "checkmark")
                            } else {
                                Text(profile)
                            }
                        }
                    }
                } label: {
                    Label("Change", systemImage: "arrow.up.arrow.down.circle")
                }
                .buttonStyle(.bordered)
            }
        }
    }
}

private struct IOSListenerRow: View {
    var listener: ListenerStatusPayload

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: "antenna.radiowaves.left.and.right")
                .foregroundStyle(.secondary)
                .frame(width: 24)

            VStack(alignment: .leading, spacing: 3) {
                Text(listener.protocol.uppercased())
                    .font(.body.weight(.medium))
                Text(listener.addr)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer(minLength: 8)

            Text("\(listener.activeConns) active")
                .font(.caption.weight(.medium))
                .monospacedDigit()
                .foregroundStyle(.secondary)
        }
    }
}

private struct IOSServersView: View {
    @ObservedObject var model: AppleAppModel
    @State private var searchText = ""

    var body: some View {
        List {
            if filteredRows.isEmpty {
                Section {
                    ContentUnavailableView(
                        searchText.isEmpty ? "No servers" : "No matching servers",
                        systemImage: "server.rack",
                        description: Text(searchText.isEmpty ? "Servers from the active profile will appear here." : "Try a different name, address, protocol, or location.")
                    )
                }
            } else {
                Section("Active Profile Servers") {
                    ForEach(filteredRows) { row in
                        IOSServerRow(row: row)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .searchable(text: $searchText, prompt: "Search servers")
        .refreshable {
            await model.refreshNow()
        }
    }

    private var rows: [IOSServerRowData] {
        model.dashboard.servers.chains.flatMap { chain in
            chain.servers.map { IOSServerRowData(chainName: chain.name, server: $0) }
        }
    }

    private var filteredRows: [IOSServerRowData] {
        let query = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !query.isEmpty else {
            return rows
        }
        return rows.filter { row in
            [
                row.chainName,
                row.server.name,
                row.server.address,
                row.server.protocol,
                row.server.geo.city,
                row.server.geo.country,
                row.server.geo.countryCode,
            ]
            .contains { $0.lowercased().contains(query) }
        }
    }
}

private struct IOSServerRowData: Identifiable {
    var id: String { "\(chainName)-\(server.id)" }
    var chainName: String
    var server: ServerPayload
}

private struct IOSServerRow: View {
    var row: IOSServerRowData

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Text(countryFlag(row.server.geo.countryCode))
                .font(.title3)
                .frame(width: 28)

            VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text(row.server.name)
                        .font(.body.weight(.medium))
                        .lineLimit(1)
                    Spacer(minLength: 8)
                    Text(row.server.protocol.uppercased())
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.secondary)
                }

                Text(row.server.address)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)

                Text("\(serverLocation(row.server)) / \(row.chainName)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, 2)
    }
}

private struct IOSTrafficView: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        List {
            Section("Summary") {
                IOSTrafficSummaryView(traffic: model.dashboard.traffic)
                    .listRowInsets(EdgeInsets(top: 10, leading: 16, bottom: 10, trailing: 16))
            }

            Section("Connections") {
                if model.dashboard.traffic.connections.isEmpty {
                    ContentUnavailableView(
                        "No traffic history",
                        systemImage: "point.3.connected.trianglepath.dotted",
                        description: Text("Recent connections will appear here.")
                    )
                } else {
                    ForEach(model.dashboard.traffic.connections) { connection in
                        NavigationLink {
                            IOSTrafficConnectionDetailView(connection: connection)
                        } label: {
                            IOSTrafficConnectionRow(connection: connection)
                        }
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .refreshable {
            await model.refreshNow()
        }
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

private struct IOSTrafficConnectionRow: View {
    var connection: TrafficConnectionPayload

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Circle()
                .fill(connection.state.lowercased() == "active" ? Color.green : Color.secondary)
                .frame(width: 9, height: 9)
                .padding(.top, 6)

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

                Text(iosTrafficSubtitle(connection))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)

                Text("\(formatBytes(connection.rxTotal)) down / \(formatBytes(connection.txTotal)) up / \(formatDurationNs(connection.durationNs))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, 2)
    }
}

private struct IOSTrafficConnectionDetailView: View {
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

            Section("Traffic") {
                LabeledContent("Down", value: formatBytes(connection.rxTotal))
                LabeledContent("Up", value: formatBytes(connection.txTotal))
                LabeledContent("Down rate", value: formatRate(connection.rxBps))
                LabeledContent("Up rate", value: formatRate(connection.txBps))
                LabeledContent("Duration", value: formatDurationNs(connection.durationNs))
                LabeledContent("Dial time", value: formatDurationNs(connection.totalDialNs))
            }

            if !connection.hops.isEmpty {
                Section("Hops") {
                    ForEach(Array(connection.hops.enumerated()), id: \.offset) { _, hop in
                        IOSHopRow(hop: hop)
                    }
                }
            }

            if !connection.closeReason.isEmpty {
                Section("Close Reason") {
                    Text(connection.closeReason)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle(emptyDash(connection.targetHost))
        .navigationBarTitleDisplayMode(.inline)
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

private struct IOSLogsView: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        List {
            Section("Recent Logs") {
                if model.dashboard.logs.isEmpty {
                    ContentUnavailableView(
                        "No logs yet",
                        systemImage: "doc.text.magnifyingglass",
                        description: Text("Daemon log events will appear here.")
                    )
                } else {
                    ForEach(Array(model.dashboard.logs.enumerated()), id: \.offset) { _, line in
                        Text(line)
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(.secondary)
                            .textSelection(.enabled)
                            .lineLimit(4)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .refreshable {
            await model.refreshNow()
        }
    }
}

private struct IOSMetric: Identifiable {
    var id: String { title }
    var title: String
    var value: String
    var systemImage: String
}

private struct IOSMetricsGrid: View {
    var metrics: [IOSMetric]

    private var columns: [GridItem] {
        [GridItem(.adaptive(minimum: 145), spacing: 10)]
    }

    var body: some View {
        LazyVGrid(columns: columns, alignment: .leading, spacing: 10) {
            ForEach(metrics) { metric in
                IOSMetricTile(metric: metric)
            }
        }
    }
}

private struct IOSMetricTile: View {
    var metric: IOSMetric

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: metric.systemImage)
                .foregroundStyle(.secondary)
                .frame(width: 22)

            VStack(alignment: .leading, spacing: 3) {
                Text(metric.title)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Text(metric.value)
                    .font(.subheadline.weight(.semibold))
                    .monospacedDigit()
                    .lineLimit(1)
                    .minimumScaleFactor(0.75)
            }

            Spacer(minLength: 0)
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
    }
}

private struct IOSStatusBadge: View {
    var text: String
    var systemImage: String
    var tint: Color

    var body: some View {
        Label(text, systemImage: systemImage)
            .font(.caption.weight(.medium))
            .lineLimit(1)
            .foregroundStyle(tint)
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .background(tint.opacity(0.12), in: Capsule())
    }
}

private struct IOSInlineEmptyState: View {
    var text: String
    var systemImage: String

    var body: some View {
        Label(text, systemImage: systemImage)
            .font(.subheadline)
            .foregroundStyle(.secondary)
    }
}

private func iosTrafficSubtitle(_ connection: TrafficConnectionPayload) -> String {
    let parts = [connection.application, connection.network, connection.chainName]
        .filter { !$0.isEmpty }
    if !parts.isEmpty {
        return parts.joined(separator: " / ")
    }
    return emptyDash(connection.listener.protocol)
}

private func iosListenerDescription(_ listener: TrafficListenerPayload) -> String {
    let protocolText = emptyDash(listener.protocol).uppercased()
    if listener.addr.isEmpty {
        return protocolText
    }
    return "\(protocolText) / \(listener.addr)"
}

private struct IOSOperationsOverviewView: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        List {
            Section {
                IOSStatusPanel(model: model)
            }

            Section("Essentials") {
                IOSMetricsGrid(metrics: overviewMetrics)
                    .listRowInsets(EdgeInsets(top: 10, leading: 16, bottom: 10, trailing: 16))
            }

            Section("Active Profile") {
                IOSProfileControl(model: model)
            }

            Section("Recent Decisions") {
                if model.dashboard.recentDecisions.isEmpty {
                    IOSInlineEmptyState(text: "No routing decisions yet.", systemImage: "arrow.triangle.branch")
                } else {
                    ForEach(model.dashboard.recentDecisions) { decision in
                        IOSDecisionRow(decision: decision)
                    }
                }
            }

            Section("Rule Hits") {
                if model.dashboard.ruleHitSummaries.isEmpty {
                    IOSInlineEmptyState(text: "Rules have not matched traffic yet.", systemImage: "checklist")
                } else {
                    ForEach(model.dashboard.ruleHitSummaries.prefix(6)) { summary in
                        IOSRuleHitRow(summary: summary)
                    }
                }
            }

            Section("Server Health") {
                let healthRows = serverHealthRows
                if healthRows.isEmpty {
                    IOSInlineEmptyState(text: "No active profile servers.", systemImage: "server.rack")
                } else {
                    ForEach(healthRows.prefix(4)) { row in
                        IOSServerHealthRow(row: row)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .refreshable {
            await model.refreshNow()
        }
    }

    private var overviewMetrics: [IOSMetric] {
        let sample = model.dashboard.currentBandwidth
        return [
            IOSMetric(title: "Down", value: formatRate(sample.rxBps), systemImage: "arrow.down"),
            IOSMetric(title: "Up", value: formatRate(sample.txBps), systemImage: "arrow.up"),
            IOSMetric(title: "Active", value: "\(model.dashboard.traffic.summary.activeConnections)", systemImage: "bolt.horizontal.circle"),
            IOSMetric(title: "Servers", value: "\(model.dashboard.servers.chains.reduce(0) { $0 + $1.servers.count })", systemImage: "server.rack"),
        ]
    }

    private var serverHealthRows: [IOSServerHealthRowData] {
        let health = model.dashboard.passiveServerHealth
        return model.dashboard.servers.chains.flatMap { chain in
            chain.servers.map { server in
                IOSServerHealthRowData(chainName: chain.name, server: server, health: health[server.id])
            }
        }
    }
}

private struct IOSOperationsTrafficView: View {
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

private struct IOSOperationsServersView: View {
    @ObservedObject var model: AppleAppModel
    @State private var searchText = ""

    var body: some View {
        List {
            if filteredChains.isEmpty {
                Section {
                    ContentUnavailableView(
                        searchText.isEmpty ? "No servers" : "No matching servers",
                        systemImage: "server.rack",
                        description: Text("Servers from the active profile appear here with passive health from recent traffic.")
                    )
                }
            } else {
                ForEach(filteredChains) { chain in
                    Section(chain.name) {
                        ForEach(chain.rows) { row in
                            IOSServerHealthRow(row: row)
                        }
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .searchable(text: $searchText, prompt: "Search servers")
        .refreshable {
            await model.refreshNow()
        }
    }

    private var filteredChains: [IOSServerHealthChain] {
        let health = model.dashboard.passiveServerHealth
        let query = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return model.dashboard.servers.chains.compactMap { chain in
            let rows = chain.servers
                .map { IOSServerHealthRowData(chainName: chain.name, server: $0, health: health[$0.id]) }
                .filter { row in
                    guard !query.isEmpty else { return true }
                    return [
                        row.chainName,
                        row.server.name,
                        row.server.address,
                        row.server.protocol,
                        row.server.geo.city,
                        row.server.geo.country,
                        row.health?.state ?? "",
                    ]
                    .contains { $0.lowercased().contains(query) }
                }
            return rows.isEmpty ? nil : IOSServerHealthChain(name: chain.name, rows: rows)
        }
    }
}

private struct IOSOperationsLogsView: View {
    @ObservedObject var model: AppleAppModel
    @State private var searchText = ""
    @State private var filter: IOSLogFilter = .all

    var body: some View {
        List {
            Section("Filter") {
                Picker("Log Filter", selection: $filter) {
                    ForEach(IOSLogFilter.allCases) { filter in
                        Text(filter.title).tag(filter)
                    }
                }
                .pickerStyle(.segmented)
            }

            Section("Recent Logs") {
                if filteredLogs.isEmpty {
                    ContentUnavailableView(
                        "No matching logs",
                        systemImage: "doc.text.magnifyingglass",
                        description: Text("Daemon and tunnel log events appear here.")
                    )
                } else {
                    ForEach(Array(filteredLogs.enumerated()), id: \.offset) { _, line in
                        IOSLogLineRow(line: line)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .searchable(text: $searchText, prompt: "Search logs")
        .refreshable {
            await model.refreshNow()
        }
    }

    private var filteredLogs: [String] {
        let query = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return model.dashboard.logs.filter { line in
            filter.matches(line) && (query.isEmpty || line.lowercased().contains(query))
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

private struct IOSServerHealthChain: Identifiable {
    var id: String { name }
    var name: String
    var rows: [IOSServerHealthRowData]
}

private struct IOSServerHealthRowData: Identifiable {
    var id: String { "\(chainName)-\(server.id)" }
    var chainName: String
    var server: ServerPayload
    var health: ServerHealth?
}

private struct IOSServerHealthRow: View {
    var row: IOSServerHealthRowData

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Text(countryFlag(row.server.geo.countryCode))
                .font(.title3)
                .frame(width: 28)

            VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text(row.server.name)
                        .font(.body.weight(.medium))
                        .lineLimit(1)
                    Spacer(minLength: 8)
                    IOSHealthBadge(health: row.health)
                }
                Text(row.server.address)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                Text([row.server.protocol.uppercased(), serverLocation(row.server), latencyText].filter { !$0.isEmpty }.joined(separator: " / "))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 2)
    }

    private var latencyText: String {
        guard let health = row.health, health.latencyNs > 0 else {
            return ""
        }
        return formatDurationNs(health.latencyNs)
    }
}

private struct IOSHealthBadge: View {
    var health: ServerHealth?

    var body: some View {
        Label(title, systemImage: icon)
            .font(.caption.weight(.medium))
            .foregroundStyle(tint)
            .labelStyle(.titleAndIcon)
    }

    private var title: String {
        switch health?.state {
        case "healthy":
            return "Healthy"
        case "error":
            return "Error"
        default:
            return "Idle"
        }
    }

    private var icon: String {
        switch health?.state {
        case "healthy":
            return "checkmark.circle.fill"
        case "error":
            return "exclamationmark.triangle.fill"
        default:
            return "circle"
        }
    }

    private var tint: Color {
        switch health?.state {
        case "healthy":
            return .green
        case "error":
            return .red
        default:
            return .secondary
        }
    }
}

private struct IOSActionChip: View {
    var action: String

    var body: some View {
        Label(title, systemImage: icon)
            .font(.caption.weight(.semibold))
            .foregroundStyle(tint)
            .padding(.horizontal, 8)
            .padding(.vertical, 4)
            .background(tint.opacity(0.12), in: Capsule())
            .lineLimit(1)
    }

    private var normalized: String {
        action.lowercased()
    }

    private var title: String {
        switch normalized {
        case "block", "reject":
            return "Block"
        case "direct":
            return "Direct"
        default:
            return "Proxy"
        }
    }

    private var icon: String {
        switch normalized {
        case "block", "reject":
            return "hand.raised.fill"
        case "direct":
            return "arrow.up.right"
        default:
            return "shield.lefthalf.filled"
        }
    }

    private var tint: Color {
        switch normalized {
        case "block", "reject":
            return .red
        case "direct":
            return .blue
        default:
            return .green
        }
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

private enum IOSLogFilter: String, CaseIterable, Identifiable {
    case all
    case errors
    case warnings

    var id: Self { self }

    var title: String {
        switch self {
        case .all: return "All"
        case .errors: return "Errors"
        case .warnings: return "Warn"
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

private struct IOSLogLineRow: View {
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

private struct IOSRuleEditorView: View {
    @ObservedObject var model: AppleAppModel
    @Environment(\.dismiss) private var dismiss
    @State private var rules: [RulePayload] = []
    @State private var message = ""
    @State private var loaded = false

    var body: some View {
        List {
            Section("Rules") {
                if rules.isEmpty {
                    IOSInlineEmptyState(text: "No routing rules.", systemImage: "checklist")
                } else {
                    ForEach(rules.indices, id: \.self) { index in
                        NavigationLink {
                            IOSRuleFormView(rule: $rules[index], chainNames: chainNames)
                        } label: {
                            IOSRuleDraftRow(rule: rules[index])
                        }
                    }
                    .onDelete { rules.remove(atOffsets: $0) }
                    .onMove { rules.move(fromOffsets: $0, toOffset: $1) }
                }
            }

            Section {
                Button {
                    rules.append(RulePayload(name: "new-rule", action: "block"))
                } label: {
                    Label("Add Rule", systemImage: "plus.circle")
                }

                Button {
                    saveRules()
                } label: {
                    Label("Save Rules", systemImage: "checkmark.circle")
                }
                .fontWeight(.semibold)
            }

            if !message.isEmpty {
                Section("Status") {
                    Text(message)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Rule Editor")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                EditButton()
            }
        }
        .onAppear {
            if !loaded {
                rules = model.dashboard.rules.rules
                loaded = true
            }
        }
    }

    private var chainNames: [String] {
        model.dashboard.servers.chains.map(\.name)
    }

    private func saveRules() {
        do {
            try model.replaceActiveProfileRules(rules)
            message = "Saved rules."
            dismiss()
        } catch {
            message = error.localizedDescription
        }
    }
}

private struct IOSRuleDraftRow: View {
    var rule: RulePayload

    var body: some View {
        HStack(spacing: 12) {
            IOSActionChip(action: rule.action)
            VStack(alignment: .leading, spacing: 3) {
                Text(emptyDash(rule.name))
                    .font(.body.weight(.medium))
                    .lineLimit(1)
                Text(ruleSummary)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
        }
    }

    private var ruleSummary: String {
        var parts: [String] = []
        parts.append(rule.action)
        parts.append(contentsOf: rule.domains.prefix(2))
        parts.append(contentsOf: rule.domainSuffixes.prefix(2).map { "*.\($0)" })
        parts.append(contentsOf: rule.cidrs.prefix(2))
        if !rule.ports.isEmpty {
            parts.append(rule.ports.map(String.init).joined(separator: ","))
        }
        return parts.filter { !$0.isEmpty }.joined(separator: " / ")
    }
}

private struct IOSRuleFormView: View {
    @Binding var rule: RulePayload
    var chainNames: [String]

    var body: some View {
        Form {
            Section("Rule") {
                TextField("Name", text: $rule.name)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                Picker("Action", selection: $rule.action) {
                    Text("Block").tag("block")
                    Text("Reject").tag("reject")
                    Text("Direct").tag("direct")
                    ForEach(chainNames, id: \.self) { chain in
                        Text("Proxy: \(chain)").tag("chain:\(chain)")
                    }
                }
            }

            Section("Matchers") {
                IOSCSVField(title: "Domains", values: $rule.domains)
                IOSCSVField(title: "Suffixes", values: $rule.domainSuffixes)
                IOSCSVField(title: "Keywords", values: $rule.domainKeywords)
                IOSCSVField(title: "CIDRs", values: $rule.cidrs)
                IOSPortsField(ports: $rule.ports)
                IOSCSVField(title: "Networks", values: $rule.networks)
            }
        }
        .navigationTitle(rule.name.isEmpty ? "Rule" : rule.name)
        .navigationBarTitleDisplayMode(.inline)
    }
}

private struct IOSCSVField: View {
    var title: String
    @Binding var values: [String]

    var body: some View {
        TextField(title, text: Binding(
            get: { values.joined(separator: ", ") },
            set: { raw in
                values = raw.split(separator: ",")
                    .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
                    .filter { !$0.isEmpty }
            }
        ))
        .textInputAutocapitalization(.never)
        .autocorrectionDisabled()
    }
}

private struct IOSPortsField: View {
    @Binding var ports: [Int]

    var body: some View {
        TextField("Ports", text: Binding(
            get: { ports.map(String.init).joined(separator: ", ") },
            set: { raw in
                ports = raw.split(separator: ",")
                    .compactMap { Int($0.trimmingCharacters(in: .whitespacesAndNewlines)) }
            }
        ))
        .keyboardType(.numbersAndPunctuation)
    }
}

private struct IOSOnboardingView: View {
    @ObservedObject var model: AppleAppModel
    var onComplete: () -> Void
    @State private var showingFileImporter = false
    @State private var showingScanner = false
    @State private var message = ""
    @State private var canContinue = false
    @State private var profileRequest = TunnelProfileCreateRequest()

    var body: some View {
        NavigationStack {
            List {
                Section {
                    VStack(alignment: .leading, spacing: 12) {
                        Image(systemName: "network.badge.shield.half.filled")
                            .font(.system(size: 42))
                            .foregroundStyle(.tint)
                        Text("Set Up clambhook")
                            .font(.title2.weight(.semibold))
                        Text(vpnDataUseDisclosure)
                            .font(.body)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    .padding(.vertical, 6)
                }

                Section("Import Config") {
                    Button {
                        showingFileImporter = true
                    } label: {
                        Label("Import From Files", systemImage: "folder")
                    }

                    Button {
                        importFromClipboard()
                    } label: {
                        Label("Import From Clipboard", systemImage: "doc.on.clipboard")
                    }

                    Button {
                        showingScanner = true
                    } label: {
                        Label("Scan QR", systemImage: "qrcode.viewfinder")
                    }
                }

                Section("Create First Profile") {
                    TextField("Profile name", text: $profileRequest.profileName)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("Server name", text: $profileRequest.serverName)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("Server address", text: $profileRequest.serverAddress)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("Protocol", text: $profileRequest.protocol)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextEditor(text: $profileRequest.settingsTOML)
                        .font(.system(.footnote, design: .monospaced))
                        .frame(minHeight: 110)
                    Button {
                        createProfile()
                    } label: {
                        Label("Create Profile", systemImage: "plus.circle")
                    }
                }

                if !message.isEmpty {
                    Section("Status") {
                        Text(message)
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            .navigationTitle("Welcome")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Continue") {
                        continueIfReady()
                    }
                    .fontWeight(.semibold)
                    .disabled(!canContinue)
                }
            }
            .fileImporter(
                isPresented: $showingFileImporter,
                allowedContentTypes: [.text, .plainText, .data],
                allowsMultipleSelection: false
            ) { result in
                importFromFile(result)
            }
            .sheet(isPresented: $showingScanner) {
                IOSQRCodeScannerView { value in
                    showingScanner = false
                    importText(value)
                }
            }
            .task {
                refreshReadiness()
            }
        }
    }

    private func importFromClipboard() {
        guard let text = UIPasteboard.general.string else {
            message = "Clipboard does not contain text."
            return
        }
        importText(text)
    }

    private func importFromFile(_ result: Result<[URL], Error>) {
        do {
            guard let url = try result.get().first else {
                return
            }
            let scoped = url.startAccessingSecurityScopedResource()
            defer {
                if scoped {
                    url.stopAccessingSecurityScopedResource()
                }
            }
            importText(try String(contentsOf: url, encoding: .utf8))
        } catch {
            message = error.localizedDescription
        }
    }

    private func importText(_ raw: String) {
        do {
            try model.importTunnelConfigText(raw)
            refreshReadiness(successMessage: "Imported tunnel configuration.")
        } catch {
            message = error.localizedDescription
            canContinue = false
        }
    }

    private func createProfile() {
        do {
            try model.createTunnelProfile(profileRequest)
            refreshReadiness(successMessage: "Created profile.")
        } catch {
            message = error.localizedDescription
            canContinue = false
        }
    }

    private func continueIfReady() {
        refreshReadiness()
        guard canContinue else {
            return
        }
        onComplete()
    }

    private func refreshReadiness(successMessage: String? = nil) {
        let wasReady = canContinue
        if let readinessMessage = model.tunnelOnboardingReadinessMessage() {
            canContinue = false
            message = readinessMessage
        } else {
            canContinue = true
            if let successMessage {
                message = successMessage
            } else if !wasReady {
                message = ""
            }
        }
    }
}

private struct IOSQRCodeScannerView: UIViewControllerRepresentable {
    var onCode: (String) -> Void

    func makeUIViewController(context: Context) -> IOSQRCodeScannerController {
        let controller = IOSQRCodeScannerController()
        controller.onCode = onCode
        return controller
    }

    func updateUIViewController(_ uiViewController: IOSQRCodeScannerController, context: Context) {}
}

private final class IOSQRCodeScannerController: UIViewController, AVCaptureMetadataOutputObjectsDelegate {
    var onCode: ((String) -> Void)?
    private let session = AVCaptureSession()

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .systemBackground
        configure()
    }

    private func configure() {
        guard let device = AVCaptureDevice.default(for: .video),
              let input = try? AVCaptureDeviceInput(device: device),
              session.canAddInput(input)
        else {
            showUnavailable()
            return
        }
        session.addInput(input)

        let output = AVCaptureMetadataOutput()
        guard session.canAddOutput(output) else {
            showUnavailable()
            return
        }
        session.addOutput(output)
        output.setMetadataObjectsDelegate(self, queue: .main)
        output.metadataObjectTypes = [.qr]

        let preview = AVCaptureVideoPreviewLayer(session: session)
        preview.videoGravity = .resizeAspectFill
        preview.frame = view.bounds
        view.layer.addSublayer(preview)

        Task.detached { [session] in
            session.startRunning()
        }
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        view.layer.sublayers?.compactMap { $0 as? AVCaptureVideoPreviewLayer }.forEach {
            $0.frame = view.bounds
        }
    }

    func metadataOutput(_ output: AVCaptureMetadataOutput, didOutput metadataObjects: [AVMetadataObject], from connection: AVCaptureConnection) {
        guard let value = metadataObjects.compactMap({ ($0 as? AVMetadataMachineReadableCodeObject)?.stringValue }).first else {
            return
        }
        session.stopRunning()
        onCode?(value)
    }

    private func showUnavailable() {
        let label = UILabel()
        label.text = "Camera is unavailable."
        label.textAlignment = .center
        label.textColor = .secondaryLabel
        label.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(label)
        NSLayoutConstraint.activate([
            label.centerXAnchor.constraint(equalTo: view.centerXAnchor),
            label.centerYAnchor.constraint(equalTo: view.centerYAnchor),
        ])
    }
}
