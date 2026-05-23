import AppKit
import ClambhookShared
import SwiftUI

struct MacMenuBarView: View {
    @ObservedObject var model: AppleAppModel
    @ObservedObject private var daemon: DaemonSupervisor
    @Environment(\.openSettings) private var openSettings

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
                    profilesPanel
                    listenersPanel
                    serversPanel
                    trafficPanel
                    logsPanel
                }
                .padding(14)
            }
            Divider()
            footer
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

                    Button {
                        model.refresh()
                    } label: {
                        Label("Refresh", systemImage: "arrow.clockwise")
                    }
                    .buttonStyle(.bordered)
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
                            Text(row.chain)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }
                    }
                }
            }
        }
    }

    private var trafficPanel: some View {
        MacSection(title: "Traffic") {
            VStack(alignment: .leading, spacing: 8) {
                Text("\(model.dashboard.traffic.summary.activeConnections) active / \(formatBytes(model.dashboard.traffic.summary.rxTotal)) down / \(formatBytes(model.dashboard.traffic.summary.txTotal)) up")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                if !model.dashboard.traffic.summary.persistError.isEmpty {
                    Text(model.dashboard.traffic.summary.persistError)
                        .font(.caption)
                        .foregroundStyle(.red)
                        .lineLimit(2)
                }
                if model.dashboard.traffic.connections.isEmpty {
                    MacEmptyRow(text: "No traffic history")
                } else {
                    ForEach(model.dashboard.traffic.connections.prefix(5)) { connection in
                        VStack(alignment: .leading, spacing: 2) {
                            HStack {
                                Text(emptyDash(connection.target))
                                    .font(.caption.weight(.semibold))
                                    .lineLimit(1)
                                Spacer()
                                Text(connection.state)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                            Text("\(trafficSubtitle(connection)) / \(formatBytes(connection.rxTotal)) down / \(formatBytes(connection.txTotal)) up")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }
                    }
                }
            }
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
            chain.servers.map { ServerRow(chain: chain.name, server: $0) }
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
}

private struct ServerRow: Identifiable {
    var id: String { "\(chain)-\(server.id)" }
    var chain: String
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
