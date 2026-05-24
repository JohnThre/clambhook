import ClambhookShared
import SwiftUI

struct IOSRootView: View {
    @ObservedObject var model: AppleAppModel
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass
    @State private var selectedDestination: IOSDashboardDestination = .overview
    @State private var showingSettings = false

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
            IOSOverviewView(model: model)
        case .traffic:
            IOSTrafficView(model: model)
        case .servers:
            IOSServersView(model: model)
        case .logs:
            IOSLogsView(model: model)
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
                        text: model.dashboard.apiOnline ? "API online" : "API offline",
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
    }

    private var actionButtons: some View {
        Group {
            Button {
                model.connectOrDisconnect()
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
