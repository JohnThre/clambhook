import ClambhookShared
import SwiftUI

struct IOSOperationsServersView: View {
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

private struct IOSServerHealthChain: Identifiable {
    var id: String { name }
    var name: String
    var rows: [IOSServerHealthRowData]
}

struct IOSServerHealthRowData: Identifiable {
    var id: String { "\(chainName)-\(server.id)" }
    var chainName: String
    var server: ServerPayload
    var health: ServerHealth?
}

struct IOSServerHealthRow: View {
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
