import ClambhookShared
import Foundation

@MainActor
final class MacSystemProxyManager: ObservableObject {
    @Published private(set) var isApplying = false
    @Published private(set) var statusMessage = ""

    private let runner: MacCommandRunning
    private let defaults: UserDefaults
    private let snapshotKey = "clambhook.mac.system-proxy.snapshot"

    init(runner: MacCommandRunning = MacCommandRunner(), defaults: UserDefaults? = nil) {
        self.runner = runner
        self.defaults = defaults ?? (UserDefaults(suiteName: defaultAppGroupIdentifier) ?? .standard)
    }

    func apply(enabled: Bool, listen: ConfigListenSettingsPayload) {
        isApplying = true
        statusMessage = ""
        Task {
            do {
                if enabled {
                    try enable(listen: listen)
                    statusMessage = "System proxy enabled"
                } else {
                    try disableSystemProxy()
                    statusMessage = "System proxy restored"
                }
            } catch {
                statusMessage = error.localizedDescription
            }
            isApplying = false
        }
    }

    /// Reconcile persisted proxy state when the app launches. A surviving
    /// snapshot means a prior run did not restore cleanly (for example after a
    /// crash). If proxying is still desired, re-apply without overwriting that
    /// snapshot; otherwise restore it immediately.
    func reconcileOnLaunch(desiredEnabled: Bool, listen: ConfigListenSettingsPayload) {
        switch MacSystemProxyPlanner.reconcileAction(
            hasSnapshot: hasSavedSnapshot,
            desiredEnabled: desiredEnabled
        ) {
        case .none:
            return
        case .enable:
            apply(enabled: true, listen: listen)
        case .restore:
            apply(enabled: false, listen: listen)
        }
    }

    /// Synchronous best-effort restore for app termination. Termination cannot
    /// wait for an unstructured Task, so this path performs `networksetup`
    /// before returning to AppKit.
    func restoreForTermination() {
        restoreNow(context: "termination")
    }

    /// Restore immediately when licensing locks the tunnel feature.
    func restoreForLockout() {
        restoreNow(context: "license lockout")
    }

    private var hasSavedSnapshot: Bool {
        defaults.data(forKey: snapshotKey) != nil
    }

    private func restoreNow(context: String) {
        do {
            try restoreSnapshot()
            statusMessage = "System proxy restored for \(context)"
        } catch {
            statusMessage = error.localizedDescription
        }
    }

    private func enable(listen: ConfigListenSettingsPayload) throws {
        let services = try activeServices()
        guard !services.isEmpty else {
            throw MacSystemProxyError.noNetworkServices
        }
        let http = ProxyEndpoint(rawValue: listen.http, fallbackPort: 8080)
        let socks = ProxyEndpoint(rawValue: listen.socks5, fallbackPort: 1080)

        // Capture only once. Re-enabling while a snapshot exists must preserve
        // the genuine pre-clambhook state for a later restore.
        if !hasSavedSnapshot {
            let captured = try services.map { service in
                MacProxyServiceSnapshot(
                    service: service,
                    web: try readState(kind: .web, service: service),
                    secureWeb: try readState(kind: .secureWeb, service: service),
                    socks: try readState(kind: .socks, service: service)
                )
            }
            if let snapshot = MacSystemProxyPlanner.snapshotToPersist(
                existing: defaults.data(forKey: snapshotKey),
                captured: captured
            ) {
                try saveSnapshot(snapshot)
            }
        }
        for service in services {
            try set(kind: .web, service: service, endpoint: http, enabled: true)
            try set(kind: .secureWeb, service: service, endpoint: http, enabled: true)
            try set(kind: .socks, service: service, endpoint: socks, enabled: true)
        }
    }

    private func restoreSnapshot() throws {
        let snapshot = try loadSnapshot()
        guard !snapshot.isEmpty else {
            // Without a snapshot, clambhook has no evidence that it changed the
            // system proxies. Leave the user's configuration untouched.
            return
        }
        for row in snapshot {
            try restore(kind: .web, service: row.service, state: row.web)
            try restore(kind: .secureWeb, service: row.service, state: row.secureWeb)
            try restore(kind: .socks, service: row.service, state: row.socks)
        }
        defaults.removeObject(forKey: snapshotKey)
    }

    /// Explicit user-driven disable. Restores the captured pre-clambhook state
    /// if we have one; otherwise forces the proxies off to honor the user's
    /// stated intent.
    private func disableSystemProxy() throws {
        if hasSavedSnapshot {
            try restoreSnapshot()
            return
        }
        for service in try activeServices() {
            try setState(kind: .web, service: service, enabled: false)
            try setState(kind: .secureWeb, service: service, enabled: false)
            try setState(kind: .socks, service: service, enabled: false)
        }
    }

    private func activeServices() throws -> [String] {
        let output = try runner.run("/usr/sbin/networksetup", arguments: ["-listallnetworkservices"]).stdout
        return output.components(separatedBy: .newlines)
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty && !$0.hasPrefix("An asterisk") && !$0.hasPrefix("*") }
    }

    private func readState(kind: ProxyKind, service: String) throws -> MacProxyState {
        let output = try runner.run("/usr/sbin/networksetup", arguments: [kind.getCommand, service]).stdout
        return MacProxyState(output: output)
    }

    private func restore(kind: ProxyKind, service: String, state: MacProxyState) throws {
        switch MacSystemProxyPlanner.restoreCommand(for: state) {
        case let .set(host, port, enabled):
            try set(kind: kind, service: service, endpoint: ProxyEndpoint(host: host, port: port), enabled: enabled)
        case .disable:
            try setState(kind: kind, service: service, enabled: false)
        }
    }

    private func set(kind: ProxyKind, service: String, endpoint: ProxyEndpoint, enabled: Bool) throws {
        try runner.run("/usr/sbin/networksetup", arguments: [kind.setCommand, service, endpoint.host, String(endpoint.port)])
        try setState(kind: kind, service: service, enabled: enabled)
    }

    private func setState(kind: ProxyKind, service: String, enabled: Bool) throws {
        try runner.run("/usr/sbin/networksetup", arguments: [kind.stateCommand, service, enabled ? "on" : "off"])
    }

    private func saveSnapshot(_ snapshot: [MacProxyServiceSnapshot]) throws {
        defaults.set(try JSONEncoder().encode(snapshot), forKey: snapshotKey)
    }

    private func loadSnapshot() throws -> [MacProxyServiceSnapshot] {
        guard let data = defaults.data(forKey: snapshotKey) else {
            return []
        }
        return try JSONDecoder().decode([MacProxyServiceSnapshot].self, from: data)
    }
}

private enum ProxyKind {
    case web
    case secureWeb
    case socks

    var getCommand: String {
        switch self {
        case .web: return "-getwebproxy"
        case .secureWeb: return "-getsecurewebproxy"
        case .socks: return "-getsocksfirewallproxy"
        }
    }

    var setCommand: String {
        switch self {
        case .web: return "-setwebproxy"
        case .secureWeb: return "-setsecurewebproxy"
        case .socks: return "-setsocksfirewallproxy"
        }
    }

    var stateCommand: String {
        switch self {
        case .web: return "-setwebproxystate"
        case .secureWeb: return "-setsecurewebproxystate"
        case .socks: return "-setsocksfirewallproxystate"
        }
    }
}

private struct ProxyEndpoint: Equatable {
    var host: String
    var port: Int

    init(rawValue: String, fallbackPort: Int) {
        let parsed = Self.parse(rawValue)
        self.host = parsed.host.isEmpty ? "127.0.0.1" : parsed.host
        self.port = parsed.port > 0 ? parsed.port : fallbackPort
    }

    init(host: String, port: Int) {
        self.host = host
        self.port = port
    }

    private static func parse(_ value: String) -> (host: String, port: Int) {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            return ("", 0)
        }
        if let url = URL(string: "proxy://\(trimmed)"), let host = url.host {
            return (host, url.port ?? 0)
        }
        return (trimmed, 0)
    }
}


private enum MacSystemProxyError: Error, LocalizedError {
    case noNetworkServices

    var errorDescription: String? {
        switch self {
        case .noNetworkServices:
            return "No active macOS network services were found."
        }
    }
}
