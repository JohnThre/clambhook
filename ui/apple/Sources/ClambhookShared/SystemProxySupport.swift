import Foundation

/// A single proxy setting (web / secure web / SOCKS) as reported by
/// `networksetup`.
public struct MacProxyState: Codable, Equatable, Sendable {
    public var enabled: Bool
    public var server: String
    public var port: Int

    public init(enabled: Bool = false, server: String = "", port: Int = 0) {
        self.enabled = enabled
        self.server = server
        self.port = port
    }

    /// Parses the multi-line output of `networksetup -get<...>proxy <service>`.
    public init(output: String) {
        var enabled = false
        var server = ""
        var port = 0
        for line in output.components(separatedBy: .newlines) {
            let parts = line.split(separator: ":", maxSplits: 1).map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            guard parts.count == 2 else {
                continue
            }
            switch parts[0].lowercased() {
            case "enabled":
                enabled = ["yes", "1", "on"].contains(parts[1].lowercased())
            case "server":
                server = parts[1]
            case "port":
                port = Int(parts[1]) ?? 0
            default:
                continue
            }
        }
        self.init(enabled: enabled, server: server, port: port)
    }
}

/// The captured pre-clambhook proxy configuration for one network service.
public struct MacProxyServiceSnapshot: Codable, Equatable, Sendable {
    public var service: String
    public var web: MacProxyState
    public var secureWeb: MacProxyState
    public var socks: MacProxyState

    public init(service: String, web: MacProxyState, secureWeb: MacProxyState, socks: MacProxyState) {
        self.service = service
        self.web = web
        self.secureWeb = secureWeb
        self.socks = socks
    }
}

/// The command required to return one proxy setting to its captured state.
public enum MacProxyRestoreCommand: Equatable, Sendable {
    /// Re-point the proxy at `host:port` and set its enabled flag.
    case set(host: String, port: Int, enabled: Bool)
    /// The captured state had no usable server; just turn the proxy off.
    case disable
}

/// Launch-time / lockout reconciliation of system-proxy state.
public enum MacSystemProxyReconcileAction: Equatable, Sendable {
    case none
    case enable
    case restore
}

public enum MacSystemProxyPlanner {
    /// Idempotency guard: decide which snapshot to persist when enabling.
    ///
    /// If a snapshot already exists we return `nil` so `enable()` never
    /// overwrites the genuine pre-clambhook state with a state clambhook itself
    /// produced (which would make a later restore a no-op).
    public static func snapshotToPersist(
        existing: Data?,
        captured: [MacProxyServiceSnapshot]
    ) -> [MacProxyServiceSnapshot]? {
        if let existing, !existing.isEmpty {
            return nil
        }
        return captured
    }

    /// Decide what to do at launch given whether a snapshot survives from a
    /// previous run and whether the user currently wants the system proxy on.
    public static func reconcileAction(hasSnapshot: Bool, desiredEnabled: Bool) -> MacSystemProxyReconcileAction {
        switch (hasSnapshot, desiredEnabled) {
        case (_, true):
            // Re-apply idempotently; an existing snapshot is preserved.
            return .enable
        case (true, false):
            // A snapshot survived (e.g. a crash) but the proxy should be off:
            // restore the captured pre-clambhook configuration.
            return .restore
        case (false, false):
            return .none
        }
    }

    /// The command needed to restore a single captured proxy setting.
    public static func restoreCommand(for state: MacProxyState) -> MacProxyRestoreCommand {
        if !state.server.isEmpty, state.port > 0 {
            return .set(host: state.server, port: state.port, enabled: state.enabled)
        }
        return .disable
    }
}
