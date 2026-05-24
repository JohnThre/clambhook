import ClambhookShared
import SwiftUI

struct VisionRootView: View {
    @ObservedObject var model: AppleAppModel
    @Environment(\.dismissImmersiveSpace) private var dismissImmersiveSpace
    @Environment(\.openImmersiveSpace) private var openImmersiveSpace
    @State private var selectedDestination: VisionDestination = .overview
    @State private var showingSettings = false
    @State private var immersiveOpen = false
    @State private var immersiveTransition = false

    var body: some View {
        NavigationSplitView {
            List {
                Section("Monitor") {
                    ForEach(VisionDestination.allCases) { destination in
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
            ScrollView {
                destinationView(selectedDestination)
                    .padding(28)
                    .frame(maxWidth: .infinity, alignment: .topLeading)
            }
            .navigationTitle(selectedDestination.title)
            .toolbar {
                ToolbarItemGroup(placement: .topBarTrailing) {
                    Button {
                        model.refresh()
                    } label: {
                        Image(systemName: "arrow.clockwise")
                    }
                    .accessibilityLabel("Refresh")

                    Button {
                        showingSettings = true
                    } label: {
                        Image(systemName: "gearshape")
                    }
                    .accessibilityLabel("Settings")
                }
            }
        }
        .ornament(attachmentAnchor: .scene(.bottom), contentAlignment: .center) {
            VisionControlStrip(
                model: model,
                immersiveOpen: immersiveOpen,
                immersiveTransition: immersiveTransition,
                onToggleConnection: { model.connectOrDisconnect() },
                onToggleImmersive: toggleImmersiveSpace
            )
            .padding(10)
            .background(.regularMaterial, in: Capsule())
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
            .frame(minWidth: 520, minHeight: 560)
        }
    }

    @ViewBuilder
    private func destinationView(_ destination: VisionDestination) -> some View {
        switch destination {
        case .overview:
            VisionOverviewView(model: model, onOpenSettings: { showingSettings = true })
        case .traffic:
            VisionTrafficView(model: model)
        case .servers:
            VisionServersView(model: model)
        case .logs:
            VisionLogsView(model: model)
        }
    }

    private func toggleImmersiveSpace() {
        guard !immersiveTransition else {
            return
        }
        immersiveTransition = true
        Task { @MainActor in
            if immersiveOpen {
                await dismissImmersiveSpace()
                immersiveOpen = false
            } else {
                switch await openImmersiveSpace(id: visionImmersiveSpaceID) {
                case .opened:
                    immersiveOpen = true
                case .error, .userCancelled:
                    immersiveOpen = false
                @unknown default:
                    immersiveOpen = false
                }
            }
            immersiveTransition = false
        }
    }
}

private enum VisionDestination: String, CaseIterable, Identifiable, Hashable {
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

private struct VisionControlStrip: View {
    @ObservedObject var model: AppleAppModel
    var immersiveOpen: Bool
    var immersiveTransition: Bool
    var onToggleConnection: () -> Void
    var onToggleImmersive: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            Button {
                onToggleConnection()
            } label: {
                Label(
                    model.dashboard.status.running ? "Disconnect" : "Connect",
                    systemImage: model.dashboard.status.running ? "stop.fill" : "play.fill"
                )
            }
            .buttonStyle(.borderedProminent)
            .disabled(!model.dashboard.apiOnline && !model.dashboard.status.running)

            Button {
                model.refresh()
            } label: {
                Label("Refresh", systemImage: "arrow.clockwise")
            }
            .buttonStyle(.bordered)

            Button {
                onToggleImmersive()
            } label: {
                Label(immersiveOpen ? "Close Map" : "Open Map", systemImage: "globe")
            }
            .buttonStyle(.bordered)
            .disabled(immersiveTransition)
        }
        .controlSize(.large)
    }
}

private struct VisionOverviewView: View {
    @ObservedObject var model: AppleAppModel
    var onOpenSettings: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            VisionStatusPanel(model: model, onOpenSettings: onOpenSettings)
            VisionMetricsPanel(metrics: overviewMetrics)

            ViewThatFits(in: .horizontal) {
                HStack(alignment: .top, spacing: 18) {
                    VisionProfilesPanel(model: model)
                    VisionListenersPanel(model: model)
                }
                VStack(alignment: .leading, spacing: 18) {
                    VisionProfilesPanel(model: model)
                    VisionListenersPanel(model: model)
                }
            }
        }
    }

    private var overviewMetrics: [VisionMetric] {
        let sample = model.dashboard.currentBandwidth
        let activeConnections = model.dashboard.status.listeners.reduce(0) { $0 + $1.activeConns }
        return [
            VisionMetric(title: "Down", value: formatRate(sample.rxBps), systemImage: "arrow.down"),
            VisionMetric(title: "Up", value: formatRate(sample.txBps), systemImage: "arrow.up"),
            VisionMetric(title: "Active", value: "\(activeConnections)", systemImage: "bolt.horizontal.circle"),
            VisionMetric(title: "Listeners", value: "\(model.dashboard.status.listeners.count)", systemImage: "antenna.radiowaves.left.and.right"),
        ]
    }
}

private struct VisionStatusPanel: View {
    @ObservedObject var model: AppleAppModel
    var onOpenSettings: () -> Void

    var body: some View {
        VisionPanel("Status", systemImage: model.dashboard.status.running ? "network" : "network.slash") {
            HStack(alignment: .center, spacing: 18) {
                ZStack {
                    Circle()
                        .fill(statusTint.opacity(0.16))
                    Image(systemName: model.dashboard.status.running ? "network" : "network.slash")
                        .font(.largeTitle)
                        .foregroundStyle(statusTint)
                }
                .frame(width: 72, height: 72)

                VStack(alignment: .leading, spacing: 6) {
                    Text(model.dashboard.status.running ? "Connected" : "Disconnected")
                        .font(.title2.weight(.semibold))
                    Text(emptyDash(model.dashboard.activeProfile))
                        .font(.headline)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                    Text(model.settingsStore.settings.apiEndpoint.absoluteString)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }

                Spacer(minLength: 12)

                VStack(alignment: .trailing, spacing: 8) {
                    VisionStatusChip(
                        text: model.dashboard.apiOnline ? "API online" : "API offline",
                        systemImage: "network",
                        tint: model.dashboard.apiOnline ? .green : .red
                    )
                    VisionStatusChip(
                        text: model.dashboard.status.running ? "Running" : "Stopped",
                        systemImage: model.dashboard.status.running ? "checkmark.circle.fill" : "pause.circle",
                        tint: statusTint
                    )
                }
            }

            if !model.dashboard.errorText.isEmpty {
                Label(model.dashboard.errorText, systemImage: "exclamationmark.triangle.fill")
                    .font(.callout)
                    .foregroundStyle(.red)
                    .lineLimit(3)

                Button {
                    onOpenSettings()
                } label: {
                    Label("Open Settings", systemImage: "gearshape")
                }
                .buttonStyle(.bordered)
            }
        }
    }

    private var statusTint: Color {
        model.dashboard.status.running ? .green : .secondary
    }
}

private struct VisionProfilesPanel: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        VisionPanel("Profiles", systemImage: "person.crop.rectangle.stack") {
            if model.dashboard.profiles.profiles.isEmpty {
                VisionEmptyState(text: "No profiles are available.", systemImage: "person.crop.rectangle.stack")
            } else {
                HStack(spacing: 14) {
                    VStack(alignment: .leading, spacing: 4) {
                        Text(emptyDash(model.dashboard.activeProfile))
                            .font(.headline)
                            .lineLimit(1)
                        Text("\(model.dashboard.profiles.profiles.count) profiles")
                            .font(.callout)
                            .foregroundStyle(.secondary)
                    }

                    Spacer(minLength: 12)

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
        .frame(minWidth: 320)
    }
}

private struct VisionListenersPanel: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        VisionPanel("Listeners", systemImage: "antenna.radiowaves.left.and.right") {
            if model.dashboard.status.listeners.isEmpty {
                VisionEmptyState(text: "No listeners are active.", systemImage: "antenna.radiowaves.left.and.right.slash")
            } else {
                VStack(spacing: 10) {
                    ForEach(model.dashboard.status.listeners) { listener in
                        VisionListenerRow(listener: listener)
                    }
                }
            }
        }
    }
}

private struct VisionListenerRow: View {
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

private struct VisionMetricsPanel: View {
    var metrics: [VisionMetric]

    private var columns: [GridItem] {
        [GridItem(.adaptive(minimum: 155), spacing: 12)]
    }

    var body: some View {
        VisionPanel("Live Metrics", systemImage: "waveform.path.ecg") {
            LazyVGrid(columns: columns, alignment: .leading, spacing: 12) {
                ForEach(metrics) { metric in
                    VisionMetricTile(metric: metric)
                }
            }
        }
    }
}

private struct VisionMetric: Identifiable {
    var id: String { title }
    var title: String
    var value: String
    var systemImage: String
}

private struct VisionMetricTile: View {
    var metric: VisionMetric

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Label(metric.title, systemImage: metric.systemImage)
                .font(.caption)
                .foregroundStyle(.secondary)
            Text(metric.value)
                .font(.title3.weight(.semibold))
                .monospacedDigit()
                .lineLimit(1)
                .minimumScaleFactor(0.75)
        }
        .frame(maxWidth: .infinity, minHeight: 72, alignment: .leading)
    }
}

private struct VisionServersView: View {
    @ObservedObject var model: AppleAppModel
    @State private var searchText = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            VisionPanel("Server Search", systemImage: "magnifyingglass") {
                TextField("Search name, address, protocol, or location", text: $searchText)
                    .textFieldStyle(.roundedBorder)
            }

            VisionPanel("Active Profile Servers", systemImage: "server.rack") {
                if filteredRows.isEmpty {
                    VisionEmptyState(
                        text: searchText.isEmpty ? "No servers in active profile." : "No matching servers.",
                        systemImage: "server.rack"
                    )
                } else {
                    LazyVStack(alignment: .leading, spacing: 12) {
                        ForEach(filteredRows) { row in
                            VisionServerRow(row: row)
                        }
                    }
                }
            }
        }
    }

    private var rows: [VisionServerRowData] {
        model.dashboard.servers.chains.flatMap { chain in
            chain.servers.map { VisionServerRowData(chainName: chain.name, server: $0) }
        }
    }

    private var filteredRows: [VisionServerRowData] {
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

private struct VisionServerRowData: Identifiable {
    var id: String { "\(chainName)-\(server.id)" }
    var chainName: String
    var server: ServerPayload
}

private struct VisionServerRow: View {
    var row: VisionServerRowData

    var body: some View {
        HStack(alignment: .top, spacing: 14) {
            Text(countryFlag(row.server.geo.countryCode))
                .font(.title2)
                .frame(width: 34)

            VStack(alignment: .leading, spacing: 5) {
                HStack(alignment: .firstTextBaseline, spacing: 10) {
                    Text(row.server.name)
                        .font(.headline)
                        .lineLimit(1)
                    Spacer(minLength: 8)
                    Text(row.server.protocol.uppercased())
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.secondary)
                }

                Text(row.server.address)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)

                Text("\(serverLocation(row.server)) / \(row.chainName)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
    }
}

private struct VisionTrafficView: View {
    @ObservedObject var model: AppleAppModel
    @State private var selectedConnectionID = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            VisionMetricsPanel(metrics: [
                VisionMetric(title: "Active", value: "\(model.dashboard.traffic.summary.activeConnections)", systemImage: "bolt.horizontal.circle"),
                VisionMetric(title: "Down", value: formatRate(model.dashboard.traffic.summary.rxBps), systemImage: "arrow.down"),
                VisionMetric(title: "Up", value: formatRate(model.dashboard.traffic.summary.txBps), systemImage: "arrow.up"),
                VisionMetric(title: "Total", value: "\(formatBytes(model.dashboard.traffic.summary.rxTotal)) / \(formatBytes(model.dashboard.traffic.summary.txTotal))", systemImage: "sum"),
            ])

            if !model.dashboard.traffic.summary.persistError.isEmpty {
                VisionPanel("Traffic Store", systemImage: "exclamationmark.triangle.fill") {
                    Text(model.dashboard.traffic.summary.persistError)
                        .foregroundStyle(.red)
                        .lineLimit(3)
                }
            }

            if model.dashboard.traffic.connections.isEmpty {
                VisionPanel("Connections", systemImage: "point.3.connected.trianglepath.dotted") {
                    VisionEmptyState(text: "No traffic history.", systemImage: "point.3.connected.trianglepath.dotted")
                }
            } else {
                ViewThatFits(in: .horizontal) {
                    HStack(alignment: .top, spacing: 18) {
                        connectionList
                            .frame(minWidth: 420)
                        if let selectedConnection {
                            VisionConnectionDetailPanel(connection: selectedConnection)
                                .frame(minWidth: 360)
                        }
                    }
                    VStack(alignment: .leading, spacing: 18) {
                        connectionList
                        if let selectedConnection {
                            VisionConnectionDetailPanel(connection: selectedConnection)
                        }
                    }
                }
            }
        }
    }

    private var connectionList: some View {
        VisionPanel("Connections", systemImage: "point.3.connected.trianglepath.dotted") {
            LazyVStack(alignment: .leading, spacing: 12) {
                ForEach(model.dashboard.traffic.connections) { connection in
                    Button {
                        selectedConnectionID = connection.id
                    } label: {
                        VisionTrafficConnectionRow(
                            connection: connection,
                            selected: selectedConnection?.id == connection.id
                        )
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }

    private var selectedConnection: TrafficConnectionPayload? {
        if let match = model.dashboard.traffic.connections.first(where: { $0.id == selectedConnectionID }) {
            return match
        }
        return model.dashboard.traffic.connections.first
    }
}

private struct VisionTrafficConnectionRow: View {
    var connection: TrafficConnectionPayload
    var selected: Bool

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Circle()
                .fill(connection.state.lowercased() == "active" ? Color.green : Color.secondary)
                .frame(width: 10, height: 10)
                .padding(.top, 7)

            VStack(alignment: .leading, spacing: 5) {
                HStack(alignment: .firstTextBaseline) {
                    Text(emptyDash(connection.target))
                        .font(.body.weight(.medium))
                        .lineLimit(1)
                    Spacer(minLength: 8)
                    Text(emptyDash(connection.state).capitalized)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }

                Text(visionTrafficSubtitle(connection))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)

                Text("\(formatBytes(connection.rxTotal)) down / \(formatBytes(connection.txTotal)) up / \(formatDurationNs(connection.durationNs))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(10)
        .background(selected ? Color.accentColor.opacity(0.12) : Color.clear, in: RoundedRectangle(cornerRadius: 10))
    }
}

private struct VisionConnectionDetailPanel: View {
    var connection: TrafficConnectionPayload

    var body: some View {
        VisionPanel("Connection Detail", systemImage: "info.circle") {
            VStack(alignment: .leading, spacing: 10) {
                LabeledContent("Target", value: emptyDash(connection.target))
                LabeledContent("State", value: emptyDash(connection.state).capitalized)
                LabeledContent("Network", value: emptyDash(connection.network))
                LabeledContent("Application", value: emptyDash(connection.application))
                LabeledContent("Client", value: emptyDash(connection.clientAddr))
                LabeledContent("Listener", value: visionListenerDescription(connection.listener))
                Divider()
                LabeledContent("Down", value: formatRate(connection.rxBps))
                LabeledContent("Up", value: formatRate(connection.txBps))
                LabeledContent("Total", value: "\(formatBytes(connection.rxTotal)) / \(formatBytes(connection.txTotal))")
                LabeledContent("Duration", value: formatDurationNs(connection.durationNs))

                if !connection.hops.isEmpty {
                    Divider()
                    Text("Hops")
                        .font(.headline)
                    ForEach(Array(connection.hops.enumerated()), id: \.offset) { _, hop in
                        VisionHopRow(hop: hop)
                    }
                }

                if !connection.closeReason.isEmpty {
                    Divider()
                    Text(connection.closeReason)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(3)
                }
            }
        }
    }
}

private struct VisionHopRow: View {
    var hop: TrafficHopPayload

    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack {
                Text(emptyDash(hop.name))
                    .font(.body.weight(.medium))
                    .lineLimit(1)
                Spacer()
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
            }
        }
    }
}

private struct VisionLogsView: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        VisionPanel("Recent Logs", systemImage: "doc.text.magnifyingglass") {
            if model.dashboard.logs.isEmpty {
                VisionEmptyState(text: "No logs yet.", systemImage: "doc.text.magnifyingglass")
            } else {
                LazyVStack(alignment: .leading, spacing: 8) {
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
    }
}

private struct VisionPanel<Content: View>: View {
    var title: String
    var systemImage: String
    var content: Content

    init(_ title: String, systemImage: String, @ViewBuilder content: () -> Content) {
        self.title = title
        self.systemImage = systemImage
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Label(title, systemImage: systemImage)
                .font(.headline)
                .foregroundStyle(.secondary)
            content
        }
        .padding(20)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
    }
}

private struct VisionStatusChip: View {
    var text: String
    var systemImage: String
    var tint: Color

    var body: some View {
        Label(text, systemImage: systemImage)
            .font(.caption.weight(.medium))
            .lineLimit(1)
            .foregroundStyle(tint)
            .padding(.horizontal, 10)
            .padding(.vertical, 6)
            .background(tint.opacity(0.12), in: Capsule())
    }
}

private struct VisionEmptyState: View {
    var text: String
    var systemImage: String

    var body: some View {
        Label(text, systemImage: systemImage)
            .font(.callout)
            .foregroundStyle(.secondary)
    }
}

private func visionTrafficSubtitle(_ connection: TrafficConnectionPayload) -> String {
    let parts = [connection.application, connection.network, connection.chainName]
        .filter { !$0.isEmpty }
    if !parts.isEmpty {
        return parts.joined(separator: " / ")
    }
    return emptyDash(connection.listener.protocol)
}

private func visionListenerDescription(_ listener: TrafficListenerPayload) -> String {
    let protocolText = emptyDash(listener.protocol).uppercased()
    if listener.addr.isEmpty {
        return protocolText
    }
    return "\(protocolText) / \(listener.addr)"
}
