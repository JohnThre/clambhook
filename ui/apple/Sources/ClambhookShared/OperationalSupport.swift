import Foundation

public enum TunnelImportError: Error, LocalizedError, Equatable {
    case empty
    case unsupported
    case invalidBase64

    public var errorDescription: String? {
        switch self {
        case .empty:
            return "The import data is empty."
        case .unsupported:
            return "Use a TOML config or a clambhook://import QR code."
        case .invalidBase64:
            return "The import QR code could not be decoded."
        }
    }
}

public enum TunnelImportDecoder {
    public static func decode(_ rawValue: String) throws -> String {
        let trimmed = rawValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw TunnelImportError.empty
        }
        if trimmed.lowercased().hasPrefix("clambhook://import") {
            return try decodeURL(trimmed)
        }
        guard looksLikeTOML(trimmed) else {
            throw TunnelImportError.unsupported
        }
        return trimmed
    }

    public static func looksLikeTOML(_ value: String) -> Bool {
        let lower = value.lowercased()
        return lower.contains("[[profile]]") || lower.contains("active =") || lower.contains("[profile.listen.tun]")
    }

    private static func decodeURL(_ rawValue: String) throws -> String {
        guard let components = URLComponents(string: rawValue),
              let rawConfig = components.queryItems?.first(where: { $0.name == "config" })?.value
        else {
            throw TunnelImportError.unsupported
        }
        guard let data = Data(base64URLEncoded: rawConfig),
              let text = String(data: data, encoding: .utf8)
        else {
            throw TunnelImportError.invalidBase64
        }
        guard looksLikeTOML(text) else {
            throw TunnelImportError.unsupported
        }
        return text
    }
}

public struct TunnelProfileCreateRequest: Codable, Equatable, Sendable {
    public var profileName: String
    public var chainName: String
    public var serverName: String
    public var serverAddress: String
    public var `protocol`: String
    public var settingsTOML: String
    public var replace: Bool

    enum CodingKeys: String, CodingKey {
        case profileName = "profile_name"
        case chainName = "chain_name"
        case serverName = "server_name"
        case serverAddress = "server_address"
        case `protocol`
        case settingsTOML = "settings_toml"
        case replace
    }

    public init(profileName: String = "default", chainName: String = "proxy", serverName: String = "server", protocol: String = "shadowsocks", serverAddress: String = "", settingsTOML: String = "", replace: Bool = true) {
        self.profileName = profileName
        self.chainName = chainName
        self.serverName = serverName
        self.protocol = `protocol`
        self.serverAddress = serverAddress
        self.settingsTOML = settingsTOML
        self.replace = replace
    }
}

public struct RecentDecision: Identifiable, Equatable, Sendable {
    public var id: String { connection.connID }
    public var connection: TrafficConnectionPayload
    public var action: String
    public var ruleName: String
    public var target: String
}

public struct RuleHitSummary: Identifiable, Equatable, Sendable {
    public var id: String { ruleName.isEmpty ? action : "\(ruleName)-\(action)" }
    public var ruleName: String
    public var action: String
    public var count: Int
}

public struct ServerHealth: Equatable, Sendable {
    public var latencyNs: Int64
    public var lastUsedTsNs: Int64
    public var lastError: String
    public var hitCount: Int

    public var state: String {
        if !lastError.isEmpty {
            return "error"
        }
        if hitCount == 0 {
            return "idle"
        }
        return "healthy"
    }
}

public extension DashboardStore {
    var recentDecisions: [RecentDecision] {
        traffic.connections
            .filter { !$0.ruleAction.isEmpty || !$0.ruleName.isEmpty }
            .sorted { $0.updatedTsNs > $1.updatedTsNs }
            .prefix(8)
            .map {
                RecentDecision(
                    connection: $0,
                    action: $0.ruleAction.isEmpty ? "chain" : $0.ruleAction,
                    ruleName: $0.ruleName,
                    target: $0.target.isEmpty ? $0.targetHost : $0.target
                )
            }
    }

    var ruleHitSummaries: [RuleHitSummary] {
        let grouped = Dictionary(grouping: traffic.connections.filter { !$0.ruleAction.isEmpty }) {
            "\($0.ruleName)|\($0.ruleAction)"
        }
        return grouped.map { _, rows in
            let first = rows[0]
            return RuleHitSummary(ruleName: first.ruleName, action: first.ruleAction, count: rows.count)
        }
        .sorted {
            if $0.count == $1.count {
                return $0.id < $1.id
            }
            return $0.count > $1.count
        }
    }

    var passiveServerHealth: [String: ServerHealth] {
        var health: [String: ServerHealth] = [:]
        for chain in servers.chains {
            for server in chain.servers {
                var row = ServerHealth(latencyNs: 0, lastUsedTsNs: 0, lastError: "", hitCount: 0)
                for connection in traffic.connections {
                    for hop in connection.hops where hopMatchesServer(hop, server: server) {
                        row.hitCount += 1
                        if hop.elapsedNs > 0 {
                            row.latencyNs = hop.elapsedNs
                        }
                        if connection.updatedTsNs > row.lastUsedTsNs {
                            row.lastUsedTsNs = connection.updatedTsNs
                        }
                        if !hop.error.isEmpty {
                            row.lastError = hop.error
                        }
                    }
                }
                health[server.id] = row
            }
        }
        return health
    }
}

public extension TrafficConnectionPayload {
    var displayDecision: String {
        if ruleAction.isEmpty && ruleName.isEmpty {
            return "proxy"
        }
        if ruleName.isEmpty {
            return ruleAction
        }
        return "\(ruleAction) / \(ruleName)"
    }

    var displayVisibility: String {
        guard let visibility else {
            return application.isEmpty ? network.uppercased() : application
        }
        switch visibility.kind {
        case "dns":
            return [visibility.host, visibility.queryType].filter { !$0.isEmpty }.joined(separator: " ")
        case "http":
            return [visibility.method, visibility.host, visibility.path].filter { !$0.isEmpty }.joined(separator: " ")
        case "http_connect":
            return [visibility.method, visibility.host].filter { !$0.isEmpty }.joined(separator: " ")
        default:
            return [visibility.kind, visibility.host].filter { !$0.isEmpty }.joined(separator: " ")
        }
    }
}

public extension TunnelConfigStore {
    static func isPlaceholderConfigText(_ text: String) -> Bool {
        let lower = text.lowercased()
        return lower.contains("replace-me") || lower.contains("replace-with-secret") || lower.contains("proxy.example.com")
    }
}

private func hopMatchesServer(_ hop: TrafficHopPayload, server: ServerPayload) -> Bool {
    hop.address == server.address || (!hop.name.isEmpty && hop.name == server.name)
}

private extension Data {
    init?(base64URLEncoded value: String) {
        var raw = value.replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/")
        let remainder = raw.count % 4
        if remainder > 0 {
            raw += String(repeating: "=", count: 4 - remainder)
        }
        self.init(base64Encoded: raw)
    }
}
