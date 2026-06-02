import ClambhookShared
import SwiftUI

struct IOSMetric: Identifiable {
    var id: String { title }
    var title: String
    var value: String
    var systemImage: String
}

struct IOSMetricsGrid: View {
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

struct IOSStatusBadge: View {
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

struct IOSInlineEmptyState: View {
    var text: String
    var systemImage: String

    var body: some View {
        Label(text, systemImage: systemImage)
            .font(.subheadline)
            .foregroundStyle(.secondary)
    }
}

struct IOSRecoveryBanner: View {
    var issue: TunnelRecoveryIssue
    var onAction: (TunnelRecoveryAction) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Label {
                VStack(alignment: .leading, spacing: 3) {
                    Text(issue.title)
                        .font(.subheadline.weight(.semibold))
                    Text(issue.message)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            } icon: {
                Image(systemName: icon)
                    .foregroundStyle(tint)
            }

            if !issue.actions.isEmpty {
                ViewThatFits(in: .horizontal) {
                    HStack(spacing: 8) {
                        actionButtons
                    }
                    VStack(spacing: 8) {
                        actionButtons
                    }
                }
            }
        }
        .padding(.vertical, 4)
    }

    private var actionButtons: some View {
        ForEach(issue.actions) { action in
            if action == issue.actions.first {
                Button {
                    onAction(action)
                } label: {
                    actionLabel(action)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.regular)
            } else {
                Button {
                    onAction(action)
                } label: {
                    actionLabel(action)
                }
                .buttonStyle(.bordered)
                .controlSize(.regular)
            }
        }
    }

    private func actionLabel(_ action: TunnelRecoveryAction) -> some View {
        Label(action.title, systemImage: actionIcon(action))
            .frame(maxWidth: .infinity)
    }

    private var icon: String {
        switch issue.kind {
        case .vpnPermissionDenied:
            return "hand.raised.fill"
        case .invalidEntitlementOrProfile:
            return "exclamationmark.shield.fill"
        case .badServerCredentials:
            return "key.fill"
        case .noUDPSupport:
            return "antenna.radiowaves.left.and.right.slash"
        case .demoProfileExpired:
            return "calendar.badge.exclamationmark"
        case .generic:
            return "exclamationmark.triangle.fill"
        }
    }

    private var tint: Color {
        switch issue.kind {
        case .badServerCredentials, .noUDPSupport, .demoProfileExpired:
            return .orange
        case .vpnPermissionDenied, .invalidEntitlementOrProfile, .generic:
            return .red
        }
    }

    private func actionIcon(_ action: TunnelRecoveryAction) -> String {
        switch action {
        case .retry:
            return "play.fill"
        case .refresh:
            return "arrow.clockwise"
        case .openAppSettings:
            return "gearshape"
        case .rebuildVPNProfile:
            return "arrow.triangle.2.circlepath"
        case .openProfiles:
            return "person.crop.rectangle.stack"
        case .importProfile:
            return "tray.and.arrow.down"
        }
    }
}

struct IOSActionChip: View {
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

func iosListenerDescription(_ listener: TrafficListenerPayload) -> String {
    let protocolText = emptyDash(listener.protocol).uppercased()
    if listener.addr.isEmpty {
        return protocolText
    }
    return "\(protocolText) / \(listener.addr)"
}
