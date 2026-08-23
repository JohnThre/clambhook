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
    case shadowtls
    case wireguard
    case openvpn
    case trojan
    case vmess
    case tor
    case clambback
    case advanced

    public var id: String { rawValue }

    public var displayName: String {
        switch self {
        case .shadowsocks:
            return "Shadowsocks"
        case .shadowtls:
            return "ShadowTLS"
        case .wireguard:
            return "WireGuard"
        case .openvpn:
            return "OpenVPN"
        case .trojan:
            return "Trojan"
        case .vmess:
            return "VMESS"
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

public struct TunnelShadowTLSTemplateSettings: Equatable, Sendable {
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
            "version": .int(3),
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

public struct TunnelVMESSTemplateSettings: Equatable, Sendable {
    public var uuid: String
    public var security: String
    public var tls: Bool
    public var sni: String
    public var skipCertVerify: Bool

    public init(uuid: String = "", security: String = "auto", tls: Bool = false, sni: String = "", skipCertVerify: Bool = false) {
        self.uuid = uuid
        self.security = security
        self.tls = tls
        self.sni = sni
        self.skipCertVerify = skipCertVerify
    }

    public var settings: [String: TunnelProfileSettingValue] {
        var settings: [String: TunnelProfileSettingValue] = [
            "uuid": .string(uuid.trimmedForProfileTemplate),
        ]
        let trimmedSecurity = security.trimmedForProfileTemplate
        if !trimmedSecurity.isEmpty {
            settings["security"] = .string(trimmedSecurity)
        }
        if tls {
            settings["tls"] = .bool(true)
            settings["skip_cert_verify"] = .bool(skipCertVerify)
            if !sni.trimmedForProfileTemplate.isEmpty {
                settings["sni"] = .string(sni.trimmedForProfileTemplate)
            }
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
    public var shadowtls: TunnelShadowTLSTemplateSettings
    public var wireguard: TunnelWireGuardTemplateSettings
    public var openvpn: TunnelOpenVPNTemplateSettings
    public var trojan: TunnelTrojanTemplateSettings
    public var vmess: TunnelVMESSTemplateSettings
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
        shadowtls: TunnelShadowTLSTemplateSettings = TunnelShadowTLSTemplateSettings(),
        wireguard: TunnelWireGuardTemplateSettings = TunnelWireGuardTemplateSettings(),
        openvpn: TunnelOpenVPNTemplateSettings = TunnelOpenVPNTemplateSettings(),
        trojan: TunnelTrojanTemplateSettings = TunnelTrojanTemplateSettings(),
        vmess: TunnelVMESSTemplateSettings = TunnelVMESSTemplateSettings(),
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
        self.shadowtls = shadowtls
        self.wireguard = wireguard
        self.openvpn = openvpn
        self.trojan = trojan
        self.vmess = vmess
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
        case .shadowtls:
            return hasCommonCreateFields && !shadowtls.password.isEmpty
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
        case .vmess:
            return hasCommonCreateFields && !vmess.uuid.trimmedForProfileTemplate.isEmpty
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
        case .shadowtls:
            return shadowtls.settings
        case .wireguard:
            return wireguard.settings(endpoint: serverAddress)
        case .openvpn:
            return openvpn.settings
        case .trojan:
            return trojan.settings
        case .vmess:
            return vmess.settings
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

public struct RuleSuggestionSummary: Identifiable, Equatable, Sendable {
    public var id: String
    public var kind: String
    public var action: String
    public var match: String
    public var count: Int
    public var confidence: String
    public var reason: String
    public var draftRule: RulePayload

    public init(id: String = "", kind: String = "", action: String = "", match: String = "", count: Int = 0, confidence: String = "", reason: String = "", draftRule: RulePayload = RulePayload()) {
        self.id = id
        self.kind = kind
        self.action = action
        self.match = match
        self.count = count
        self.confidence = confidence
        self.reason = reason
        self.draftRule = draftRule
    }
}

public enum PolicySelectorHealthState: String, Equatable, Sendable {
    case staticRoute
    case pending
    case healthy
    case fallback
}

public struct PolicySelectorRouteSummary: Identifiable, Equatable, Sendable {
    public var id: String { groupName.isEmpty ? selectedChain : groupName }
    public var groupName: String
    public var selectedChain: String
    public var healthState: PolicySelectorHealthState
    public var healthText: String

    public init(groupName: String = "", selectedChain: String = "", healthState: PolicySelectorHealthState = .pending, healthText: String = "") {
        self.groupName = groupName
        self.selectedChain = selectedChain
        self.healthState = healthState
        self.healthText = healthText
    }
}

public struct PolicySelectorSummary: Equatable, Sendable {
    public var proxyCount: Int
    public var directCount: Int
    public var blockCount: Int
    public var routes: [PolicySelectorRouteSummary]
    public var topRuleHits: [RuleHitSummary]

    public init(proxyCount: Int = 0, directCount: Int = 0, blockCount: Int = 0, routes: [PolicySelectorRouteSummary] = [], topRuleHits: [RuleHitSummary] = []) {
        self.proxyCount = proxyCount
        self.directCount = directCount
        self.blockCount = blockCount
        self.routes = routes
        self.topRuleHits = topRuleHits
    }

    public static func build(policyGroups: PolicyGroupsPayload, servers: ServersPayload, traffic: TrafficSnapshotPayload) -> PolicySelectorSummary {
        let counts = actionCounts(from: traffic)
        let routes: [PolicySelectorRouteSummary]
        if policyGroups.groups.isEmpty {
            routes = servers.chains.first.map {
                [PolicySelectorRouteSummary(groupName: "Default route", selectedChain: $0.name, healthState: .staticRoute, healthText: "Static / no health probes")]
            } ?? []
        } else {
            routes = policyGroups.groups.map { routeSummary(for: $0) }
        }
        return PolicySelectorSummary(
            proxyCount: counts["proxy", default: 0],
            directCount: counts["direct", default: 0],
            blockCount: counts["block", default: 0],
            routes: routes,
            topRuleHits: Array(buildRuleHitSummaries(from: traffic).prefix(3))
        )
    }
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
            .filter { !$0.ruleAction.isEmpty || !$0.ruleName.isEmpty || !$0.chainName.isEmpty }
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
        buildRuleHitSummaries(from: traffic)
    }

    var monitorActionCounts: [String: Int] {
        actionCounts(from: traffic)
    }

    var ruleSuggestionSummaries: [RuleSuggestionSummary] {
        traffic.ruleSuggestions.map(\.summary)
    }

    var policySelectorSummary: PolicySelectorSummary {
        PolicySelectorSummary.build(policyGroups: policyGroups, servers: servers, traffic: traffic)
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

    var temporaryAllowAction: String {
        let action = ruleAction.lowercased()
        if action == "direct" {
            return "direct"
        }
        if action == "group", !groupName.isEmpty {
            return "group:\(groupName)"
        }
        if !groupName.isEmpty {
            return "group:\(groupName)"
        }
        if !chainName.isEmpty {
            return "chain:\(chainName)"
        }
        return "direct"
    }

    func temporaryProxyAction(fallbackChain: String = "") -> String {
        if !groupName.isEmpty {
            return "group:\(groupName)"
        }
        if !chainName.isEmpty {
            return "chain:\(chainName)"
        }
        let fallback = fallbackChain.trimmingCharacters(in: .whitespacesAndNewlines)
        if !fallback.isEmpty {
            return "chain:\(fallback)"
        }
        return ""
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

public extension TrafficRuleSuggestionPayload {
    var summary: RuleSuggestionSummary {
        RuleSuggestionSummary(
            id: id.isEmpty ? "\(kind)-\(action)-\(draftRule.name)" : id,
            kind: kind,
            action: action.isEmpty ? draftRule.action : action,
            match: draftRule.displayMatch,
            count: count,
            confidence: confidence,
            reason: reason,
            draftRule: draftRule
        )
    }
}

public extension RulePayload {
    var displayMatch: String {
        if !domains.isEmpty {
            return domains.joined(separator: ",")
        }
        if !domainSuffixes.isEmpty {
            return domainSuffixes.map { "*.\($0)" }.joined(separator: ",")
        }
        if !cidrs.isEmpty {
            return cidrs.joined(separator: ",")
        }
        if !domainKeywords.isEmpty {
            return "contains \(domainKeywords.joined(separator: ","))"
        }
        return "any"
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

private func actionCounts(from traffic: TrafficSnapshotPayload) -> [String: Int] {
    var counts = ["proxy": 0, "direct": 0, "block": 0]
    if !traffic.quickFilters.isEmpty {
        for filter in traffic.quickFilters {
            if counts.keys.contains(filter.key) {
                counts[filter.key] = filter.count
            }
        }
        return counts
    }
    for connection in traffic.connections {
        counts[connection.actionFamily, default: 0] += 1
    }
    return counts
}

private func buildRuleHitSummaries(from traffic: TrafficSnapshotPayload) -> [RuleHitSummary] {
    let summaries: [RuleHitSummary]
    if !traffic.ruleHits.isEmpty {
        summaries = traffic.ruleHits.map {
            RuleHitSummary(ruleName: $0.ruleName, action: $0.action, count: $0.count)
        }
    } else {
        let grouped = Dictionary(grouping: traffic.connections.filter { !$0.ruleAction.isEmpty }) {
            "\($0.ruleName)|\($0.actionFamily)"
        }
        summaries = grouped.map { _, rows in
            let first = rows[0]
            return RuleHitSummary(ruleName: first.ruleName, action: first.actionFamily, count: rows.count)
        }
    }
    return summaries.sorted {
        if $0.count == $1.count {
            return $0.id < $1.id
        }
        return $0.count > $1.count
    }
}

private func routeSummary(for group: PolicyGroupPayload) -> PolicySelectorRouteSummary {
    let selected = group.selectedChain.isEmpty ? (group.chains.first ?? "") : group.selectedChain
    guard !group.results.isEmpty else {
        return PolicySelectorRouteSummary(groupName: group.name, selectedChain: selected, healthState: .pending, healthText: "Pending health")
    }
    let healthy = group.results.filter(\.healthy).count
    let total = group.results.count
    if group.results.first(where: { $0.chainName == selected })?.healthy == true {
        return PolicySelectorRouteSummary(groupName: group.name, selectedChain: selected, healthState: .healthy, healthText: "Healthy / \(healthy)/\(total)")
    }
    return PolicySelectorRouteSummary(groupName: group.name, selectedChain: selected, healthState: .fallback, healthText: "Fallback / \(healthy)/\(total) healthy")
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

/// Server-side filter parameters for the live connection monitor
/// (GET /api/v1/traffic). The daemon filters across the full history rather
/// than the in-memory window a client-side filter would see, so a quickFilter
/// chip or search box reaches connections beyond the returned page.
public struct TrafficMonitorFilter: Equatable, Sendable {
    public var state: String = ""
    public var action: String = ""
    public var profile: String = ""
    public var rule: String = ""
    public var country: String = ""
    public var port: String = ""
    public var process: String = ""
    public var network: String = ""
    public var app: String = ""
    public var domain: String = ""
    public var query: String = ""
    public var limit: Int = 200
    public var offset: Int = 0

    public init() {}

    public var isEmpty: Bool {
        state.isEmpty && action.isEmpty && profile.isEmpty && rule.isEmpty &&
            country.isEmpty && port.isEmpty && process.isEmpty && network.isEmpty &&
            app.isEmpty && domain.isEmpty && query.isEmpty
    }

    /// queryItems builds the URL query items for GET /api/v1/traffic.
    public var queryItems: [URLQueryItem] {
        var items: [URLQueryItem] = []
        func add(_ name: String, _ value: String) {
            let v = value.trimmingCharacters(in: .whitespacesAndNewlines)
            if !v.isEmpty { items.append(URLQueryItem(name: name, value: v)) }
        }
        add("state", state)
        add("action", action)
        add("profile", profile)
        add("rule", rule)
        add("country", country)
        add("port", port)
        add("process", process)
        add("network", network)
        add("app", app)
        add("domain", domain)
        add("query", query)
        items.append(URLQueryItem(name: "limit", value: String(limit)))
        if offset > 0 { items.append(URLQueryItem(name: "offset", value: String(offset))) }
        return items
    }

    /// applying returns a new filter with a quickFilter chip applied. A
    /// prefixed chip (country:US, port:443, process:curl, network:tcp) sets the
    /// matching param; an action chip (proxy/direct/block) sets action=; "active"
    /// sets state=active; "all" clears the dimensional filters.
    public func applying(quickFilter key: String) -> TrafficMonitorFilter {
        var f = self
        if key == "all" {
            f.action = ""; f.state = ""; f.country = ""; f.port = ""; f.process = ""; f.network = ""
            return f
        }
        if key == "active" { f.state = "active"; return f }
        switch key {
        case "proxy", "direct", "block":
            f.action = key
            return f
        default:
            break
        }
        if let colon = key.firstIndex(of: ":") {
            let name = String(key[..<colon])
            let value = String(key[key.index(after: colon)...])
            switch name {
            case "country": f.country = value
            case "port": f.port = value
            case "process": f.process = value
            case "network": f.network = value
            default: break
            }
        }
        return f
    }
}
