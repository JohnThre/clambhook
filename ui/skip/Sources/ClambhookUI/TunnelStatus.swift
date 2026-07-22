import Foundation

/// Shared, platform-neutral view state for the ClambHook tunnel status.
///
/// Authored once in Swift and transpiled to Kotlin by Skip so the Android app
/// and the Apple client render the same model over the daemon/`TunnelRuntime`
/// JSON surfaces.
public struct TunnelStatus: Hashable, Sendable {
    public var running: Bool
    public var profileName: String
    public var activeConnections: Int
    public var downloadBytesPerSecond: Double
    public var uploadBytesPerSecond: Double

    public init(
        running: Bool = false,
        profileName: String = "",
        activeConnections: Int = 0,
        downloadBytesPerSecond: Double = 0,
        uploadBytesPerSecond: Double = 0
    ) {
        self.running = running
        self.profileName = profileName
        self.activeConnections = activeConnections
        self.downloadBytesPerSecond = downloadBytesPerSecond
        self.uploadBytesPerSecond = uploadBytesPerSecond
    }

    public var connectionStateLabel: String {
        running ? "Connected" : "Not Connected"
    }

    public var profileLabel: String {
        profileName.isEmpty ? "No profile" : profileName
    }
}

/// Formats a byte-rate as a compact human-readable string, e.g. "1.2 MB/s".
public func formatByteRate(_ bytesPerSecond: Double) -> String {
    let units = ["B/s", "KB/s", "MB/s", "GB/s", "TB/s"]
    // Guard against NaN and negative values: max(0, .nan) returns .nan,
    // and Int(.nan) traps at runtime. Treat both as zero.
    var value = (bytesPerSecond.isFinite && bytesPerSecond > 0) ? bytesPerSecond : 0
    var unit = 0
    while value >= 1024 && unit < units.count - 1 {
        value /= 1024
        unit += 1
    }
    if unit == 0 {
        return "\(Int(value)) \(units[unit])"
    }
    return String(format: "%.1f %@", value, units[unit])
}
