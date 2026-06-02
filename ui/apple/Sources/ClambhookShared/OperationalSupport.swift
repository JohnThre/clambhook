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

public enum TunnelProfileTemplate: String, CaseIterable, Codable, Identifiable, Sendable {
    case shadowsocks
    case wireguard
    case openvpn
    case trojan
    case tor
    case clambback
    case advanced

    public var id: String { rawValue }

    public var displayName: String {
        switch self {
        case .shadowsocks:
            return "Shadowsocks"
        case .wireguard:
            return "WireGuard"
        case .openvpn:
            return "OpenVPN"
        case .trojan:
            return "Trojan"
        case .tor:
            return "Tor"
        case .clambback:
            return "Clambback"
        case .advanced:
            return "Advanced"
        }
    }

    public var protocolName: String? {
        self == .advanced ? nil : rawValue
    }

    public var defaultServerName: String {
        switch self {
        case .advanced:
            return "server"
        default:
            return rawValue
        }
    }
}

public enum TunnelProfileSettingValue: Codable, Equatable, Sendable {
    case string(String)
    case bool(Bool)
    case int(Int)
    case double(Double)
    case array([TunnelProfileSettingValue])
    case object([String: TunnelProfileSettingValue])

    public init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Int.self) {
            self = .int(value)
        } else if let value = try? container.decode(Double.self) {
            self = .double(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([TunnelProfileSettingValue].self) {
            self = .array(value)
        } else {
            self = .object(try container.decode([String: TunnelProfileSettingValue].self))
        }
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .string(let value):
            try container.encode(value)
        case .bool(let value):
            try container.encode(value)
        case .int(let value):
            try container.encode(value)
        case .double(let value):
            try container.encode(value)
        case .array(let value):
            try container.encode(value)
        case .object(let value):
            try container.encode(value)
        }
    }
}

public struct TunnelShadowsocksTemplateSettings: Equatable, Sendable {
    public var method: String
    public var password: String

    public init(method: String = "chacha20-ietf-poly1305", password: String = "") {
        self.method = method
        self.password = password
    }

    public var settings: [String: TunnelProfileSettingValue] {
        [
            "method": .string(method.trimmedForProfileTemplate),
            "password": .string(password),
        ]
    }
}

public struct TunnelWireGuardTemplateSettings: Equatable, Sendable {
    public var privateKey: String
    public var interfaceAddresses: String
    public var dnsServers: String
    public var peerPublicKey: String
    public var presharedKey: String
    public var allowedIPs: String
    public var persistentKeepalive: Int
    public var mtu: Int
    public var logLevel: String

    public init(
        privateKey: String = "",
        interfaceAddresses: String = "10.0.0.2/32",
        dnsServers: String = "",
        peerPublicKey: String = "",
        presharedKey: String = "",
        allowedIPs: String = "0.0.0.0/0, ::/0",
        persistentKeepalive: Int = 25,
        mtu: Int = 1420,
        logLevel: String = "error"
    ) {
        self.privateKey = privateKey
        self.interfaceAddresses = interfaceAddresses
        self.dnsServers = dnsServers
        self.peerPublicKey = peerPublicKey
        self.presharedKey = presharedKey
        self.allowedIPs = allowedIPs
        self.persistentKeepalive = persistentKeepalive
        self.mtu = mtu
        self.logLevel = logLevel
    }

    public func settings(endpoint: String) -> [String: TunnelProfileSettingValue] {
        var peer: [String: TunnelProfileSettingValue] = [
            "public_key": .string(peerPublicKey.trimmedForProfileTemplate),
            "endpoint": .string(endpoint.trimmedForProfileTemplate),
            "allowed_ips": .array(TunnelProfileCreateDraft.stringListValues(from: allowedIPs)),
        ]
        if !presharedKey.trimmedForProfileTemplate.isEmpty {
            peer["preshared_key"] = .string(presharedKey.trimmedForProfileTemplate)
        }
        if persistentKeepalive > 0 {
            peer["persistent_keepalive"] = .int(persistentKeepalive)
        }

        var settings: [String: TunnelProfileSettingValue] = [
            "private_key": .string(privateKey.trimmedForProfileTemplate),
            "addresses": .array(TunnelProfileCreateDraft.stringListValues(from: interfaceAddresses)),
            "peers": .array([.object(peer)]),
            "log_level": .string(logLevel.trimmedForProfileTemplate),
        ]
        let dns = TunnelProfileCreateDraft.stringListValues(from: dnsServers)
        if !dns.isEmpty {
            settings["dns"] = .array(dns)
        }
        if mtu > 0 {
            settings["mtu"] = .int(mtu)
        }
        return settings
    }
}

public struct TunnelOpenVPNTemplateSettings: Equatable, Sendable {
    public var caCert: String
    public var clientCert: String
    public var clientKey: String
    public var serverCN: String
    public var username: String
    public var password: String
    public var cipher: String
    public var tunMTU: Int
    public var skipCertVerify: Bool

    public init(
        caCert: String = "",
        clientCert: String = "",
        clientKey: String = "",
        serverCN: String = "",
        username: String = "",
        password: String = "",
        cipher: String = "",
        tunMTU: Int = 1500,
        skipCertVerify: Bool = false
    ) {
        self.caCert = caCert
        self.clientCert = clientCert
        self.clientKey = clientKey
        self.serverCN = serverCN
        self.username = username
        self.password = password
        self.cipher = cipher
        self.tunMTU = tunMTU
        self.skipCertVerify = skipCertVerify
    }

    public var settings: [String: TunnelProfileSettingValue] {
        var settings: [String: TunnelProfileSettingValue] = [
            "ca_cert": .string(caCert),
            "client_cert": .string(clientCert),
            "client_key": .string(clientKey),
            "skip_cert_verify": .bool(skipCertVerify),
        ]
        if !serverCN.trimmedForProfileTemplate.isEmpty {
            settings["server_cn"] = .string(serverCN.trimmedForProfileTemplate)
        }
        if !username.trimmedForProfileTemplate.isEmpty || !password.isEmpty {
            settings["username"] = .string(username.trimmedForProfileTemplate)
            settings["password"] = .string(password)
        }
        if !cipher.trimmedForProfileTemplate.isEmpty {
            settings["cipher"] = .string(cipher.trimmedForProfileTemplate)
        }
        if tunMTU > 0 {
            settings["tun_mtu"] = .int(tunMTU)
        }
        return settings
    }
}

public struct TunnelTrojanTemplateSettings: Equatable, Sendable {
    public var password: String
    public var sni: String
    public var alpn: String
    public var skipCertVerify: Bool

    public init(password: String = "", sni: String = "", alpn: String = "", skipCertVerify: Bool = false) {
        self.password = password
        self.sni = sni
        self.alpn = alpn
        self.skipCertVerify = skipCertVerify
    }

    public var settings: [String: TunnelProfileSettingValue] {
        var settings: [String: TunnelProfileSettingValue] = [
            "password": .string(password),
            "skip_cert_verify": .bool(skipCertVerify),
        ]
        if !sni.trimmedForProfileTemplate.isEmpty {
            settings["sni"] = .string(sni.trimmedForProfileTemplate)
        }
        let alpnValues = TunnelProfileCreateDraft.stringListValues(from: alpn)
        if !alpnValues.isEmpty {
            settings["alpn"] = .array(alpnValues)
        }
        return settings
    }
}

public struct TunnelTorTemplateSettings: Equatable, Sendable {
    public var isolationUser: String
    public var isolationPass: String

    public init(isolationUser: String = "", isolationPass: String = "") {
        self.isolationUser = isolationUser
        self.isolationPass = isolationPass
    }

    public var settings: [String: TunnelProfileSettingValue] {
        var settings: [String: TunnelProfileSettingValue] = [:]
        if !isolationUser.trimmedForProfileTemplate.isEmpty || !isolationPass.isEmpty {
            settings["isolation_user"] = .string(isolationUser.trimmedForProfileTemplate)
            settings["isolation_pass"] = .string(isolationPass)
        }
        return settings
    }
}

public struct TunnelProfileCreateDraft: Equatable, Sendable {
    public var template: TunnelProfileTemplate
    public var profileName: String
    public var chainName: String
    public var serverName: String
    public var serverAddress: String
    public var replace: Bool
    public var shadowsocks: TunnelShadowsocksTemplateSettings
    public var wireguard: TunnelWireGuardTemplateSettings
    public var openvpn: TunnelOpenVPNTemplateSettings
    public var trojan: TunnelTrojanTemplateSettings
    public var tor: TunnelTorTemplateSettings
    public var clambback: TunnelTrojanTemplateSettings
    public var advancedTOML: String

    public init(
        template: TunnelProfileTemplate = .shadowsocks,
        profileName: String = "default",
        chainName: String = "proxy",
        serverName: String = "server",
        serverAddress: String = "",
        replace: Bool = true,
        shadowsocks: TunnelShadowsocksTemplateSettings = TunnelShadowsocksTemplateSettings(),
        wireguard: TunnelWireGuardTemplateSettings = TunnelWireGuardTemplateSettings(),
        openvpn: TunnelOpenVPNTemplateSettings = TunnelOpenVPNTemplateSettings(),
        trojan: TunnelTrojanTemplateSettings = TunnelTrojanTemplateSettings(),
        tor: TunnelTorTemplateSettings = TunnelTorTemplateSettings(),
        clambback: TunnelTrojanTemplateSettings = TunnelTrojanTemplateSettings(),
        advancedTOML: String = ""
    ) {
        self.template = template
        self.profileName = profileName
        self.chainName = chainName
        self.serverName = serverName
        self.serverAddress = serverAddress
        self.replace = replace
        self.shadowsocks = shadowsocks
        self.wireguard = wireguard
        self.openvpn = openvpn
        self.trojan = trojan
        self.tor = tor
        self.clambback = clambback
        self.advancedTOML = advancedTOML
    }

    public var isInputComplete: Bool {
        switch template {
        case .advanced:
            return !advancedTOML.trimmedForProfileTemplate.isEmpty
        case .shadowsocks:
            return hasCommonCreateFields && !shadowsocks.password.isEmpty
        case .wireguard:
            return hasCommonCreateFields &&
                !wireguard.privateKey.trimmedForProfileTemplate.isEmpty &&
                !wireguard.peerPublicKey.trimmedForProfileTemplate.isEmpty &&
                !Self.stringListValues(from: wireguard.interfaceAddresses).isEmpty &&
                !Self.stringListValues(from: wireguard.allowedIPs).isEmpty
        case .openvpn:
            return hasCommonCreateFields &&
                !openvpn.caCert.trimmedForProfileTemplate.isEmpty &&
                !openvpn.clientCert.trimmedForProfileTemplate.isEmpty &&
                !openvpn.clientKey.trimmedForProfileTemplate.isEmpty
        case .trojan:
            return hasCommonCreateFields && !trojan.password.isEmpty
        case .tor:
            return hasCommonCreateFields &&
                (tor.isolationUser.trimmedForProfileTemplate.isEmpty == tor.isolationPass.isEmpty)
        case .clambback:
            return hasCommonCreateFields && !clambback.password.isEmpty
        }
    }

    public mutating func applyTemplateDefaults(previousTemplate: TunnelProfileTemplate) {
        if serverName.trimmedForProfileTemplate.isEmpty || serverName == previousTemplate.defaultServerName || serverName == "server" {
            serverName = template.defaultServerName
        }
        if template == .tor && serverAddress.trimmedForProfileTemplate.isEmpty {
            serverAddress = "127.0.0.1:9050"
        }
    }

    public func makeCreateRequest() -> TunnelProfileCreateRequest? {
        guard let protocolName = template.protocolName else {
            return nil
        }
        return TunnelProfileCreateRequest(
            profileName: profileName.trimmedForProfileTemplate,
            chainName: chainName.trimmedForProfileTemplate,
            serverName: serverName.trimmedForProfileTemplate,
            protocol: protocolName,
            serverAddress: serverAddress.trimmedForProfileTemplate,
            settingsTOML: "",
            settings: settings,
            replace: replace
        )
    }

    public static func stringListValues(from rawValue: String) -> [TunnelProfileSettingValue] {
        rawValue.profileTemplateListItems.map { .string($0) }
    }

    private var hasCommonCreateFields: Bool {
        !profileName.trimmedForProfileTemplate.isEmpty &&
            !serverAddress.trimmedForProfileTemplate.isEmpty
    }

    private var settings: [String: TunnelProfileSettingValue] {
        switch template {
        case .shadowsocks:
            return shadowsocks.settings
        case .wireguard:
            return wireguard.settings(endpoint: serverAddress)
        case .openvpn:
            return openvpn.settings
        case .trojan:
            return trojan.settings
        case .tor:
            return tor.settings
        case .clambback:
            return clambback.settings
        case .advanced:
            return [:]
        }
    }
}

public struct TunnelProfileCreateRequest: Codable, Equatable, Sendable {
    public var profileName: String
    public var chainName: String
    public var serverName: String
    public var serverAddress: String
    public var `protocol`: String
    public var settings: [String: TunnelProfileSettingValue]?
    public var settingsTOML: String
    public var replace: Bool

    enum CodingKeys: String, CodingKey {
        case profileName = "profile_name"
        case chainName = "chain_name"
        case serverName = "server_name"
        case serverAddress = "server_address"
        case `protocol`
        case settings
        case settingsTOML = "settings_toml"
        case replace
    }

    public init(
        profileName: String = "default",
        chainName: String = "proxy",
        serverName: String = "server",
        protocol: String = "shadowsocks",
        serverAddress: String = "",
        settingsTOML: String = "",
        settings: [String: TunnelProfileSettingValue]? = nil,
        replace: Bool = true
    ) {
        self.profileName = profileName
        self.chainName = chainName
        self.serverName = serverName
        self.protocol = `protocol`
        self.serverAddress = serverAddress
        self.settings = settings
        self.settingsTOML = settingsTOML
        self.replace = replace
    }
}

public struct TunnelImportReviewPayload: Decodable, Equatable, Sendable {
    public var activeProfile: String
    public var profiles: [TunnelImportReviewProfile]

    enum CodingKeys: String, CodingKey {
        case activeProfile = "active_profile"
        case profiles
    }

    public init(activeProfile: String = "", profiles: [TunnelImportReviewProfile] = []) {
        self.activeProfile = activeProfile
        self.profiles = profiles
    }
}

public struct TunnelImportReviewProfile: Decodable, Equatable, Identifiable, Sendable {
    public var id: String { name }
    public var name: String
    public var chainCount: Int
    public var serverCount: Int
    public var ruleCount: Int
    public var protocols: [String]

    enum CodingKeys: String, CodingKey {
        case name
        case chainCount = "chain_count"
        case serverCount = "server_count"
        case ruleCount = "rule_count"
        case protocols
    }

    public init(
        name: String,
        chainCount: Int = 0,
        serverCount: Int = 0,
        ruleCount: Int = 0,
        protocols: [String] = []
    ) {
        self.name = name
        self.chainCount = chainCount
        self.serverCount = serverCount
        self.ruleCount = ruleCount
        self.protocols = protocols
    }
}

public struct ReviewedTunnelImportRequest: Encodable, Equatable, Sendable {
    public var importText: String
    public var profiles: [ReviewedTunnelImportProfile]
    public var activateProfile: String

    enum CodingKeys: String, CodingKey {
        case importText = "import_text"
        case profiles
        case activateProfile = "activate_profile"
    }

    public init(
        importText: String,
        profiles: [ReviewedTunnelImportProfile],
        activateProfile: String = ""
    ) {
        self.importText = importText
        self.profiles = profiles
        self.activateProfile = activateProfile
    }
}

public struct ReviewedTunnelImportProfile: Encodable, Equatable, Identifiable, Sendable {
    public var id: String { sourceName }
    public var sourceName: String
    public var targetName: String

    enum CodingKeys: String, CodingKey {
        case sourceName = "source_name"
        case targetName = "target_name"
    }

    public init(sourceName: String, targetName: String) {
        self.sourceName = sourceName
        self.targetName = targetName
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
            "\($0.ruleName)|\($0.actionFamily)"
        }
        return grouped.map { _, rows in
            let first = rows[0]
            return RuleHitSummary(ruleName: first.ruleName, action: first.actionFamily, count: rows.count)
        }
        .sorted {
            if $0.count == $1.count {
                return $0.id < $1.id
            }
            return $0.count > $1.count
        }
    }

    var monitorActionCounts: [String: Int] {
        var counts = ["proxy": 0, "direct": 0, "block": 0]
        for connection in traffic.connections {
            counts[connection.actionFamily, default: 0] += 1
        }
        return counts
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
    var actionFamily: String {
        switch ruleAction.lowercased() {
        case "direct":
            return "direct"
        case "block", "reject":
            return "block"
        default:
            return "proxy"
        }
    }

    var displayDecision: String {
        if ruleAction.isEmpty && ruleName.isEmpty {
            return "proxy"
        }
        if ruleName.isEmpty {
            return ruleAction
        }
        return "\(ruleAction) / \(ruleName)"
    }

    var monitorHost: String {
        if !targetHost.isEmpty {
            return targetHost.normalizedRuleHost
        }
        if let visibility, !visibility.host.isEmpty {
            return visibility.host.normalizedRuleHost
        }
        let parts = target.split(separator: ":")
        if parts.count > 1 {
            return parts.dropLast().joined(separator: ":").normalizedRuleHost
        }
        return target.normalizedRuleHost
    }

    func ruleDraft(actionOverride: String? = nil) -> RulePayload? {
        let host = monitorHost
        guard !host.isEmpty else { return nil }
        let family = actionOverride ?? actionFamily
        let action: String
        switch family {
        case "direct":
            action = "direct"
        case "block":
            action = ruleAction.lowercased() == "reject" ? "reject" : "block"
        default:
            action = chainName.isEmpty ? "direct" : "chain:\(chainName)"
        }
        var rule = RulePayload(name: "\(family)-\(host.ruleNameToken)", action: action)
        if host.looksLikeIPv4 {
            rule.cidrs = ["\(host)/32"]
        } else if host.contains(":") {
            rule.cidrs = ["\(host)/128"]
        } else {
            rule.domains = [host]
        }
        return rule
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

private extension String {
    var normalizedRuleHost: String {
        trimmingCharacters(in: CharacterSet(charactersIn: "[] ").union(.whitespacesAndNewlines))
            .trimmingCharacters(in: CharacterSet(charactersIn: "."))
            .lowercased()
    }

    var ruleNameToken: String {
        let allowed = CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "-"))
        let value = String(lowercased().unicodeScalars.map { allowed.contains($0) ? Character($0) : "-" })
            .trimmingCharacters(in: CharacterSet(charactersIn: "-"))
        return value.isEmpty ? "connection" : value
    }

    var looksLikeIPv4: Bool {
        let parts = split(separator: ".")
        return parts.count == 4 && parts.allSatisfy { part in
            guard let value = Int(part), value >= 0, value <= 255 else { return false }
            return true
        }
    }

    var trimmedForProfileTemplate: String {
        trimmingCharacters(in: .whitespacesAndNewlines)
    }

    var profileTemplateListItems: [String] {
        components(separatedBy: CharacterSet(charactersIn: ",\n"))
            .map { $0.trimmedForProfileTemplate }
            .filter { !$0.isEmpty }
    }
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
