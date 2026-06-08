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
    case selectPolicyGroup = "select_policy_group"
    case developerStatus = "developer_status"
    case developerEntries = "developer_entries"
    case developerCA = "developer_ca"
    case developerHAR = "developer_har"
    case clearDeveloperEntries = "clear_developer_entries"
}

public struct TunnelCommand: Codable, Equatable, Sendable {
    public var action: TunnelCommandAction
    public var profile: String?
    public var group: String?
    public var chain: String?

    public init(action: TunnelCommandAction, profile: String? = nil, group: String? = nil, chain: String? = nil) {
        self.action = action
        self.profile = profile
        self.group = group
        self.chain = chain
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
    public var dns: DNSPayload
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
        case dns
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
        dns: DNSPayload = DNSPayload(),
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
        self.dns = dns
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
        self.dns = try container.decodeIfPresent(DNSPayload.self, forKey: .dns) ?? DNSPayload()
        self.networkSettings = try container.decodeIfPresent(TunnelNetworkSettingsPayload.self, forKey: .networkSettings) ?? TunnelNetworkSettingsPayload()
    }
}

public enum TunnelPolicyGroupSelectionError: Error, LocalizedError, Equatable {
    case profileNotFound(String)
    case policyGroupNotFound(String)
    case notManualPolicyGroup(String)
    case chainNotMember(group: String, chain: String)

    public var errorDescription: String? {
        switch self {
        case .profileNotFound(let profile):
            return "profile \(profile) not found"
        case .policyGroupNotFound(let group):
            return "policy group \(group) not found"
        case .notManualPolicyGroup(let group):
            return "policy group \(group) is not select"
        case .chainNotMember(let group, let chain):
            return "policy group \(group) has no member chain \(chain)"
        }
    }
}

public func updateTunnelPolicyGroupSelection(configPath: String, profileName: String, groupName: String, chainName: String) throws {
    let url = URL(fileURLWithPath: configPath)
    let original = try String(contentsOf: url, encoding: .utf8)
    let trailingNewline = original.hasSuffix("\n")
    var lines = original.components(separatedBy: .newlines)
    if trailingNewline {
        lines.removeLast()
    }
    let targetProfile = profileName.isEmpty ? activeProfileName(in: lines) : profileName
    guard let profileRange = profileRange(named: targetProfile, in: lines) else {
        throw TunnelPolicyGroupSelectionError.profileNotFound(targetProfile)
    }
    guard let groupRange = policyGroupRange(named: groupName, profileRange: profileRange, in: lines) else {
        throw TunnelPolicyGroupSelectionError.policyGroupNotFound(groupName)
    }
    let groupLines = Array(lines[groupRange])
    let type = assignmentValue(named: "type", in: groupLines)
    guard type == "select" else {
        throw TunnelPolicyGroupSelectionError.notManualPolicyGroup(groupName)
    }
    let chains = arrayAssignment(named: "chains", in: groupLines)
    guard chains.contains(chainName) else {
        throw TunnelPolicyGroupSelectionError.chainNotMember(group: groupName, chain: chainName)
    }
    let nextLine = "  selected = \(tomlQuoted(chainName))"
    if let selectedIndex = groupRange.first(where: { assignmentKey(in: lines[$0]) == "selected" }) {
        let indent = leadingWhitespace(lines[selectedIndex])
        lines[selectedIndex] = "\(indent)selected = \(tomlQuoted(chainName))"
    } else if let chainsIndex = groupRange.first(where: { assignmentKey(in: lines[$0]) == "chains" }) {
        lines.insert(nextLine, at: chainsIndex + 1)
    } else {
        lines.insert(nextLine, at: groupRange.upperBound)
    }
    try (lines.joined(separator: "\n") + (trailingNewline ? "\n" : "")).write(to: url, atomically: true, encoding: .utf8)
}

private func activeProfileName(in lines: [String]) -> String {
    for line in lines {
        if assignmentKey(in: line) == "active" {
            return unquotedAssignmentValue(in: line)
        }
    }
    return ""
}

private func profileRange(named profileName: String, in lines: [String]) -> Range<Int>? {
    var starts: [Int] = []
    for (index, line) in lines.enumerated() where line.trimmingCharacters(in: .whitespaces) == "[[profile]]" {
        starts.append(index)
    }
    for (offset, start) in starts.enumerated() {
        let end = offset + 1 < starts.count ? starts[offset + 1] : lines.count
        let range = start..<end
        if range.contains(where: { assignmentKey(in: lines[$0]) == "name" && unquotedAssignmentValue(in: lines[$0]) == profileName }) {
            return range
        }
    }
    return nil
}

private func policyGroupRange(named groupName: String, profileRange: Range<Int>, in lines: [String]) -> Range<Int>? {
    let starts = profileRange.filter { lines[$0].trimmingCharacters(in: .whitespaces) == "[[profile.policy_group]]" }
    for (offset, start) in starts.enumerated() {
        let end = offset + 1 < starts.count ? starts[offset + 1] : profileRange.upperBound
        let range = start..<end
        if range.contains(where: { assignmentKey(in: lines[$0]) == "name" && unquotedAssignmentValue(in: lines[$0]) == groupName }) {
            return range
        }
    }
    return nil
}

private func assignmentKey(in line: String) -> String {
    let trimmed = line.trimmingCharacters(in: .whitespaces)
    guard !trimmed.hasPrefix("#"), let equals = trimmed.firstIndex(of: "=") else {
        return ""
    }
    return String(trimmed[..<equals]).trimmingCharacters(in: .whitespaces)
}

private func assignmentValue(named key: String, in lines: [String]) -> String {
    for line in lines where assignmentKey(in: line) == key {
        return unquotedAssignmentValue(in: line)
    }
    return ""
}

private func unquotedAssignmentValue(in line: String) -> String {
    guard let equals = line.firstIndex(of: "=") else {
        return ""
    }
    var value = String(line[line.index(after: equals)...]).trimmingCharacters(in: .whitespaces)
    if let comment = value.firstIndex(of: "#") {
        value = String(value[..<comment]).trimmingCharacters(in: .whitespaces)
    }
    if value.hasPrefix("\""), value.hasSuffix("\""), value.count >= 2 {
        value.removeFirst()
        value.removeLast()
    }
    return value
}

private func arrayAssignment(named key: String, in lines: [String]) -> [String] {
    for line in lines where assignmentKey(in: line) == key {
        guard let start = line.firstIndex(of: "["), let end = line.lastIndex(of: "]"), start < end else {
            return []
        }
        return line[line.index(after: start)..<end]
            .split(separator: ",")
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .map { raw in
                var value = String(raw)
                if value.hasPrefix("\""), value.hasSuffix("\""), value.count >= 2 {
                    value.removeFirst()
                    value.removeLast()
                }
                return value
            }
    }
    return []
}

private func leadingWhitespace(_ line: String) -> String {
    String(line.prefix { $0 == " " || $0 == "\t" })
}

private func tomlQuoted(_ value: String) -> String {
    "\"" + value.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"") + "\""
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
