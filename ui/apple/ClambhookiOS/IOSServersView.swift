import ClambhookShared
import SwiftUI

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
