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

struct IOSBandwidthGraphView: View {
    var samples: [BandwidthSample]
    var downTint: Color = .green
    var upTint: Color = .blue

    var body: some View {
        Canvas { context, size in
            drawGrid(in: &context, size: size)
            let maxRate = max(
                samples.map { max($0.rxBps, $0.txBps) }.max() ?? 0,
                1
            )
            let downPath = linePath(size: size, maxRate: maxRate) { $0.rxBps }
            let upPath = linePath(size: size, maxRate: maxRate) { $0.txBps }
            context.stroke(downPath, with: .color(downTint), style: StrokeStyle(lineWidth: 2.5, lineCap: .round, lineJoin: .round))
            context.stroke(upPath, with: .color(upTint), style: StrokeStyle(lineWidth: 2, lineCap: .round, lineJoin: .round))
        }
        .frame(height: 104)
        .accessibilityLabel("Live bandwidth graph")
    }

    private func drawGrid(in context: inout GraphicsContext, size: CGSize) {
        var grid = Path()
        for step in 0...3 {
            let y = size.height * CGFloat(step) / 3
            grid.move(to: CGPoint(x: 0, y: y))
            grid.addLine(to: CGPoint(x: size.width, y: y))
        }
        context.stroke(grid, with: .color(Color.secondary.opacity(0.16)), lineWidth: 1)
    }

    private func linePath(size: CGSize, maxRate: Double, value: (BandwidthSample) -> Double) -> Path {
        let points = graphSamples
        var path = Path()
        guard !points.isEmpty else { return path }
        for index in points.indices {
            let x = points.count == 1 ? 0 : size.width * CGFloat(index) / CGFloat(points.count - 1)
            let normalized = min(max(value(points[index]) / maxRate, 0), 1)
            let y = size.height - (size.height * CGFloat(normalized))
            let point = CGPoint(x: x, y: y)
            if index == points.startIndex {
                path.move(to: point)
            } else {
                path.addLine(to: point)
            }
        }
        return path
    }

    private var graphSamples: [BandwidthSample] {
        if samples.isEmpty {
            return [BandwidthSample()]
        }
        return samples
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

struct IOSConsolePanel<Content: View>: View {
    var content: Content

    init(@ViewBuilder content: () -> Content) {
        self.content = content()
    }

    var body: some View {
        content
            .padding(12)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }
}

struct IOSConsoleSection<Content: View>: View {
    var title: String
    var detail: String
    var content: Content

    init(_ title: String, detail: String = "", @ViewBuilder content: () -> Content) {
        self.title = title
        self.detail = detail
        self.content = content()
    }

    var body: some View {
        IOSConsolePanel {
            VStack(alignment: .leading, spacing: 10) {
                HStack(alignment: .firstTextBaseline) {
                    Text(title.uppercased())
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.secondary)
                    Spacer(minLength: 8)
                    if !detail.isEmpty {
                        Text(detail)
                            .font(.caption.monospacedDigit())
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
                content
            }
        }
    }
}

struct IOSConsoleMetric: Identifiable {
    var id: String { title }
    var title: String
    var value: String
    var tint: Color = .secondary
}

struct IOSConsoleMetricStrip: View {
    var metrics: [IOSConsoleMetric]

    var body: some View {
        LazyVGrid(columns: [GridItem(.adaptive(minimum: 92), spacing: 8)], alignment: .leading, spacing: 8) {
            ForEach(metrics) { metric in
                VStack(alignment: .leading, spacing: 2) {
                    Text(metric.title.uppercased())
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(.secondary)
                    Text(metric.value)
                        .font(.caption.weight(.semibold))
                        .monospacedDigit()
                        .foregroundStyle(metric.tint)
                        .lineLimit(1)
                        .minimumScaleFactor(0.75)
                }
                .padding(.horizontal, 9)
                .padding(.vertical, 7)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color(.tertiarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            }
        }
    }
}

struct IOSConsoleKeyValueRow: View {
    var label: String
    var value: String
    var valueColor: Color = .primary

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(label.uppercased())
                .font(.caption2.weight(.semibold))
                .foregroundStyle(.secondary)
                .frame(width: 72, alignment: .leading)
            Text(value.isEmpty ? "--" : value)
                .font(.caption.monospaced())
                .foregroundStyle(valueColor)
                .lineLimit(2)
                .textSelection(.enabled)
            Spacer(minLength: 0)
        }
    }
}

struct IOSConsoleIconButton: View {
    var systemImage: String
    var title: String
    var role: ButtonRole?
    var action: () -> Void

    init(_ systemImage: String, title: String, role: ButtonRole? = nil, action: @escaping () -> Void) {
        self.systemImage = systemImage
        self.title = title
        self.role = role
        self.action = action
    }

    var body: some View {
        Button(role: role, action: action) {
            Image(systemName: systemImage)
                .font(.caption.weight(.semibold))
                .frame(width: 28, height: 28)
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .accessibilityLabel(title)
    }
}
