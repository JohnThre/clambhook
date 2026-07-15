import SwiftUI

/// Surge-for-iOS-style connection status card. This is shared SwiftUI that Skip
/// transpiles to Jetpack Compose on Android, so the same layout renders on both
/// the Apple client and the Android app.
public struct TunnelStatusView: View {
    private let status: TunnelStatus
    private let onToggleConnection: () -> Void

    public init(status: TunnelStatus, onToggleConnection: @escaping () -> Void) {
        self.status = status
        self.onToggleConnection = onToggleConnection
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 8) {
                Circle()
                    .fill(status.running ? Color.green : Color.gray)
                    .frame(width: 12, height: 12)
                Text(status.connectionStateLabel)
                    .font(.headline)
                Spacer()
                Text(status.profileLabel)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }

            HStack(spacing: 24) {
                MetricView(title: "Download", value: formatByteRate(status.downloadBytesPerSecond))
                MetricView(title: "Upload", value: formatByteRate(status.uploadBytesPerSecond))
                MetricView(title: "Connections", value: "\(status.activeConnections)")
            }

            Button(action: onToggleConnection) {
                Text(status.running ? "Disconnect" : "Connect")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
        }
        .padding()
    }
}

private struct MetricView: View {
    let title: String
    let value: String

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(title)
                .font(.caption)
                .foregroundStyle(.secondary)
            Text(value)
                .font(.title3)
        }
    }
}
