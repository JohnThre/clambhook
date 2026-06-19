import ClambhookShared
import Foundation

@MainActor
final class MacSystemProxyManager: ObservableObject {
    @Published private(set) var isApplying = false
    @Published private(set) var statusMessage = ""

    private let runner: MacCommandRunning
    private let defaults: UserDefaults
    private let snapshotKey = "clambhook.mac.system-proxy.snapshot"

    init(runner: MacCommandRunning = MacCommandRunner(), defaults: UserDefaults = .standard) {
        self.runner = runner
        self.defaults = defaults
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
                    try disable()
                    statusMessage = "System proxy restored"
                }
            } catch {
                statusMessage = error.localizedDescription
            }
            isApplying = false
        }
    }

    private func enable(listen: ConfigListenSettingsPayload) throws {
        let services = try activeServices()
        guard !services.isEmpty else {
            throw MacSystemProxyError.noNetworkServices
        }
        let http = ProxyEndpoint(rawValue: listen.http, fallbackPort: 8080)
        let socks = ProxyEndpoint(rawValue: listen.socks5, fallbackPort: 1080)
        let snapshot = try services.map { service in
            MacProxyServiceSnapshot(
                service: service,
                web: try readState(kind: .web, service: service),
                secureWeb: try readState(kind: .secureWeb, service: service),
                socks: try readState(kind: .socks, service: service)
            )
        }
        try saveSnapshot(snapshot)
        for service in services {
            try set(kind: .web, service: service, endpoint: http, enabled: true)
            try set(kind: .secureWeb, service: service, endpoint: http, enabled: true)
            try set(kind: .socks, service: service, endpoint: socks, enabled: true)
        }
    }

    private func disable() throws {
        let snapshot = try loadSnapshot()
        if snapshot.isEmpty {
            for service in try activeServices() {
                try setState(kind: .web, service: service, enabled: false)
                try setState(kind: .secureWeb, service: service, enabled: false)
                try setState(kind: .socks, service: service, enabled: false)
            }
            return
        }
        for row in snapshot {
            try restore(kind: .web, service: row.service, state: row.web)
            try restore(kind: .secureWeb, service: row.service, state: row.secureWeb)
            try restore(kind: .socks, service: row.service, state: row.socks)
        }
        defaults.removeObject(forKey: snapshotKey)
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
        if !state.server.isEmpty, state.port > 0 {
            try set(kind: kind, service: service, endpoint: ProxyEndpoint(host: state.server, port: state.port), enabled: state.enabled)
        } else {
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

private struct MacProxyServiceSnapshot: Codable, Equatable {
    var service: String
    var web: MacProxyState
    var secureWeb: MacProxyState
    var socks: MacProxyState
}

private struct MacProxyState: Codable, Equatable {
    var enabled: Bool
    var server: String
    var port: Int

    init(enabled: Bool = false, server: String = "", port: Int = 0) {
        self.enabled = enabled
        self.server = server
        self.port = port
    }

    init(output: String) {
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

private enum MacSystemProxyError: Error, LocalizedError {
    case noNetworkServices

    var errorDescription: String? {
        switch self {
        case .noNetworkServices:
            return "No active macOS network services were found."
        }
    }
}
