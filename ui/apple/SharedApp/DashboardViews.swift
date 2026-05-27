import ClambhookShared
import SwiftUI

struct DashboardContentView: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        List {
            Section {
                StatusHeaderView(model: model)
            }
            Section("Profiles") {
                if model.dashboard.profiles.profiles.isEmpty {
                    Text("No profiles")
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(model.dashboard.profiles.profiles, id: \.self) { profile in
                        Button {
                            model.selectProfile(profile)
                        } label: {
                            HStack {
                                Text(profile)
                                Spacer()
                                if profile == model.dashboard.activeProfile {
                                    Image(systemName: "checkmark.circle.fill")
                                        .foregroundStyle(.green)
                                }
                            }
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
            Section("Listeners") {
                if model.dashboard.status.listeners.isEmpty {
                    Text("None active")
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(model.dashboard.status.listeners) { listener in
                        HStack {
                            Label(listener.protocol.uppercased(), systemImage: "antenna.radiowaves.left.and.right")
                            Spacer()
                            VStack(alignment: .trailing) {
                                Text(listener.addr)
                                Text("\(listener.activeConns) active")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
            Section("Servers") {
                ServerListView(servers: model.dashboard.servers)
            }
            Section("Rules") {
                RuleListView(rules: model.dashboard.rules)
            }
            Section("Bandwidth") {
                let sample = model.dashboard.currentBandwidth
                LabeledContent("Rx", value: formatRate(sample.rxBps))
                LabeledContent("Tx", value: formatRate(sample.txBps))
            }
            Section("Traffic") {
                TrafficSummaryView(traffic: model.dashboard.traffic)
                TrafficListView(connections: model.dashboard.traffic.connections)
            }
            Section("Logs") {
                if model.dashboard.logs.isEmpty {
                    Text("No logs yet")
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(Array(model.dashboard.logs.suffix(8).enumerated()), id: \.offset) { _, line in
                        Text(line)
                            .font(.system(.caption, design: .monospaced))
                            .lineLimit(2)
                    }
                }
            }
        }
        .task {
            model.refresh()
        }
    }
}

struct TrafficSummaryView: View {
    var traffic: TrafficSnapshotPayload

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            LabeledContent("Active", value: "\(traffic.summary.activeConnections)")
            LabeledContent("Down", value: formatRate(traffic.summary.rxBps))
            LabeledContent("Up", value: formatRate(traffic.summary.txBps))
            LabeledContent("Total", value: "\(formatBytes(traffic.summary.rxTotal)) down / \(formatBytes(traffic.summary.txTotal)) up")
            if !traffic.summary.persistError.isEmpty {
                Text(traffic.summary.persistError)
                    .font(.caption)
                    .foregroundStyle(.red)
            }
        }
    }
}

struct TrafficListView: View {
    var connections: [TrafficConnectionPayload]

    var body: some View {
        if connections.isEmpty {
            Text("No traffic history")
                .foregroundStyle(.secondary)
        } else {
            ForEach(connections.prefix(12)) { connection in
                VStack(alignment: .leading, spacing: 4) {
                    HStack {
                        Text(emptyDash(connection.target))
                            .fontWeight(.medium)
                        Spacer()
                        Text(connection.state)
                            .foregroundStyle(.secondary)
                    }
                    Text(trafficSubtitle(connection))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Text("\(formatBytes(connection.rxTotal)) down · \(formatBytes(connection.txTotal)) up · \(formatDurationNs(connection.durationNs))")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private func trafficSubtitle(_ connection: TrafficConnectionPayload) -> String {
        let decision = [connection.ruleName, connection.ruleAction].filter { !$0.isEmpty }.joined(separator: " -> ")
        let parts = [connection.application, connection.network, connection.chainName, decision]
            .filter { !$0.isEmpty }
        if !parts.isEmpty {
            return parts.joined(separator: " · ")
        }
        return connection.listener.protocol
    }
}

struct RuleListView: View {
    var rules: RulesPayload

    var body: some View {
        if rules.rules.isEmpty {
            Text("No routing rules")
                .foregroundStyle(.secondary)
        } else {
            ForEach(rules.rules) { rule in
                VStack(alignment: .leading, spacing: 4) {
                    HStack {
                        Text(rule.name)
                            .fontWeight(.medium)
                        Spacer()
                        Text(rule.action)
                            .foregroundStyle(.secondary)
                    }
                    Text(ruleSummary(rule))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private func ruleSummary(_ rule: RulePayload) -> String {
        var parts: [String] = []
        if !rule.domains.isEmpty {
            parts.append(rule.domains.joined(separator: ", "))
        }
        if !rule.domainSuffixes.isEmpty {
            parts.append(rule.domainSuffixes.map { "*.\($0)" }.joined(separator: ", "))
        }
        if !rule.cidrs.isEmpty {
            parts.append(rule.cidrs.joined(separator: ", "))
        }
        if !rule.ports.isEmpty {
            parts.append(rule.ports.map(String.init).joined(separator: ", "))
        }
        if !rule.networks.isEmpty {
            parts.append(rule.networks.joined(separator: ", "))
        }
        return parts.isEmpty ? "all traffic" : parts.joined(separator: " · ")
    }
}

struct StatusHeaderView: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Label(
                    model.dashboard.status.running ? "Running" : "Stopped",
                    systemImage: model.dashboard.status.running ? "checkmark.circle.fill" : "pause.circle"
                )
                .foregroundStyle(model.dashboard.status.running ? .green : .secondary)
                Spacer()
                Label(model.dashboard.apiOnline ? "API online" : "API offline", systemImage: "network")
                    .foregroundStyle(model.dashboard.apiOnline ? .green : .red)
            }
            Text(emptyDash(model.dashboard.activeProfile))
                .font(.headline)
            if !model.dashboard.errorText.isEmpty {
                Text(model.dashboard.errorText)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .lineLimit(3)
            }
            HStack {
                Button {
                    model.connectOrDisconnect()
                } label: {
                    Label(model.dashboard.status.running ? "Disconnect" : "Connect", systemImage: model.dashboard.status.running ? "stop.fill" : "play.fill")
                }
                Button {
                    model.refresh()
                } label: {
                    Label("Refresh", systemImage: "arrow.clockwise")
                }
            }
        }
    }
}

struct ServerListView: View {
    var servers: ServersPayload

    var body: some View {
        if servers.chains.isEmpty {
            Text("No servers in active profile")
                .foregroundStyle(.secondary)
        } else {
            ForEach(servers.chains) { chain in
                ForEach(chain.servers) { server in
                    VStack(alignment: .leading, spacing: 4) {
                        HStack {
                            Text(countryFlag(server.geo.countryCode))
                            Text(server.name)
                                .fontWeight(.medium)
                            Spacer()
                            Text(server.protocol)
                                .foregroundStyle(.secondary)
                        }
                        Text(server.address)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Text("\(serverLocation(server)) · \(chain.name)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
    }
}
