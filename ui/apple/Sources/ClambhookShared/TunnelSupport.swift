import Foundation

public let defaultTunnelConfigFileName = "clambhook-ios.toml"

public let defaultIOSTunnelConfig = """
# Replace this placeholder with a real clambhook profile before connecting.
active = "default"

[[profile]]
name = "default"

  [profile.listen]
  http = "127.0.0.1:8080"
  http_chain = "proxy"

  [profile.listen.tun]
  enabled = true
  mtu = 1500
  routes = ["0.0.0.0/0", "::/0"]
  exclude_cidrs = ["127.0.0.0/8", "::1/128"]

  [[profile.chain]]
  name = "proxy"

    [[profile.chain.server]]
    name = "replace-me"
    address = "proxy.example.com:443"
    protocol = "shadowsocks"

      [profile.chain.server.settings]
      method = "chacha20-ietf-poly1305"
      password = "replace-with-secret"
"""

public enum TunnelCommandAction: String, Codable, Sendable {
    case dashboard
    case status
    case profiles
    case servers
    case rules
    case traffic
    case reload
    case setActiveProfile = "set_active_profile"
    case developerStatus = "developer_status"
    case developerEntries = "developer_entries"
    case developerCA = "developer_ca"
    case developerHAR = "developer_har"
    case clearDeveloperEntries = "clear_developer_entries"
}

public struct TunnelCommand: Codable, Equatable, Sendable {
    public var action: TunnelCommandAction
    public var profile: String?

    public init(action: TunnelCommandAction, profile: String? = nil) {
        self.action = action
        self.profile = profile
    }
}

public struct TunnelDashboardPayload: Codable, Equatable, Sendable {
    public var status: StatusPayload
    public var profiles: ProfilesPayload
    public var servers: ServersPayload
    public var rules: RulesPayload
    public var policyGroups: PolicyGroupsPayload
    public var ruleSets: RuleSetsPayload
    public var ruleSubscriptions: RuleSubscriptionsPayload
    public var traffic: TrafficSnapshotPayload
    public var networkSettings: TunnelNetworkSettingsPayload

    enum CodingKeys: String, CodingKey {
        case status
        case profiles
        case servers
        case rules
        case policyGroups = "policy_groups"
        case ruleSets = "rule_sets"
        case ruleSubscriptions = "rule_subscriptions"
        case traffic
        case networkSettings = "network_settings"
    }

    public init(
        status: StatusPayload = StatusPayload(),
        profiles: ProfilesPayload = ProfilesPayload(),
        servers: ServersPayload = ServersPayload(),
        rules: RulesPayload = RulesPayload(),
        policyGroups: PolicyGroupsPayload = PolicyGroupsPayload(),
        ruleSets: RuleSetsPayload = RuleSetsPayload(),
        ruleSubscriptions: RuleSubscriptionsPayload = RuleSubscriptionsPayload(),
        traffic: TrafficSnapshotPayload = TrafficSnapshotPayload(),
        networkSettings: TunnelNetworkSettingsPayload = TunnelNetworkSettingsPayload()
    ) {
        self.status = status
        self.profiles = profiles
        self.servers = servers
        self.rules = rules
        self.policyGroups = policyGroups
        self.ruleSets = ruleSets
        self.ruleSubscriptions = ruleSubscriptions
        self.traffic = traffic
        self.networkSettings = networkSettings
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.status = try container.decodeIfPresent(StatusPayload.self, forKey: .status) ?? StatusPayload()
        self.profiles = try container.decodeIfPresent(ProfilesPayload.self, forKey: .profiles) ?? ProfilesPayload()
        self.servers = try container.decodeIfPresent(ServersPayload.self, forKey: .servers) ?? ServersPayload()
        self.rules = try container.decodeIfPresent(RulesPayload.self, forKey: .rules) ?? RulesPayload()
        self.policyGroups = try container.decodeIfPresent(PolicyGroupsPayload.self, forKey: .policyGroups) ?? PolicyGroupsPayload()
        self.ruleSets = try container.decodeIfPresent(RuleSetsPayload.self, forKey: .ruleSets) ?? RuleSetsPayload()
        self.ruleSubscriptions = try container.decodeIfPresent(RuleSubscriptionsPayload.self, forKey: .ruleSubscriptions) ?? RuleSubscriptionsPayload()
        self.traffic = try container.decodeIfPresent(TrafficSnapshotPayload.self, forKey: .traffic) ?? TrafficSnapshotPayload()
        self.networkSettings = try container.decodeIfPresent(TunnelNetworkSettingsPayload.self, forKey: .networkSettings) ?? TunnelNetworkSettingsPayload()
    }
}

public struct TunnelNetworkSettingsPayload: Codable, Equatable, Sendable {
    public var mtu: Int
    public var remoteAddress: String
    public var ipv4: [TunnelIPAddressPayload]
    public var ipv6: [TunnelIPAddressPayload]
    public var dnsServers: [String]
    public var includedRoutes: [String]
    public var excludedRoutes: [String]
    public var httpProxy: TunnelProxyPayload?
    public var httpsProxy: TunnelProxyPayload?

    enum CodingKeys: String, CodingKey {
        case mtu
        case remoteAddress = "remote_address"
        case ipv4
        case ipv6
        case dnsServers = "dns_servers"
        case includedRoutes = "included_routes"
        case excludedRoutes = "excluded_routes"
        case httpProxy = "http_proxy"
        case httpsProxy = "https_proxy"
    }

    public init(
        mtu: Int = 1500,
        remoteAddress: String = "127.0.0.1",
        ipv4: [TunnelIPAddressPayload] = [],
        ipv6: [TunnelIPAddressPayload] = [],
        dnsServers: [String] = [],
        includedRoutes: [String] = [],
        excludedRoutes: [String] = [],
        httpProxy: TunnelProxyPayload? = nil,
        httpsProxy: TunnelProxyPayload? = nil
    ) {
        self.mtu = mtu
        self.remoteAddress = remoteAddress
        self.ipv4 = ipv4
        self.ipv6 = ipv6
        self.dnsServers = dnsServers
        self.includedRoutes = includedRoutes
        self.excludedRoutes = excludedRoutes
        self.httpProxy = httpProxy
        self.httpsProxy = httpsProxy
    }
}

public struct TunnelProxyPayload: Codable, Equatable, Sendable {
    public var host: String
    public var port: Int

    public init(host: String = "", port: Int = 0) {
        self.host = host
        self.port = port
    }
}

public struct TunnelIPAddressPayload: Codable, Equatable, Sendable {
    public var address: String
    public var prefixLen: Int

    enum CodingKeys: String, CodingKey {
        case address
        case prefixLen = "prefix_len"
    }

    public init(address: String, prefixLen: Int) {
        self.address = address
        self.prefixLen = prefixLen
    }
}

public enum TunnelConfigStore {
    public static func configURL(groupIdentifier: String = defaultAppGroupIdentifier) -> URL {
        if let container = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: groupIdentifier) {
            return container.appendingPathComponent(defaultTunnelConfigFileName)
        }
        return FileManager.default.temporaryDirectory.appendingPathComponent(defaultTunnelConfigFileName)
    }

    public static func loadOrCreateConfig(groupIdentifier: String = defaultAppGroupIdentifier) throws -> String {
        let url = configURL(groupIdentifier: groupIdentifier)
        if FileManager.default.fileExists(atPath: url.path) {
            return try String(contentsOf: url, encoding: .utf8)
        }
        try save(defaultIOSTunnelConfig, groupIdentifier: groupIdentifier)
        return defaultIOSTunnelConfig
    }

    public static func save(_ text: String, groupIdentifier: String = defaultAppGroupIdentifier) throws {
        let url = configURL(groupIdentifier: groupIdentifier)
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try text.write(to: url, atomically: true, encoding: .utf8)
    }
}
