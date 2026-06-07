import Darwin
import Foundation

public enum RuleTestFailure: Error, LocalizedError {
    case invalidNetwork
    case invalidTarget
    case noChains

    public var errorDescription: String? {
        switch self {
        case .invalidNetwork:
            return "network must be TCP or UDP"
        case .invalidTarget:
            return "target must be host:port"
        case .noChains:
            return "profile has no chains"
        }
    }
}

public enum RuleTester {
    public static func test(
        network rawNetwork: String,
        target rawTarget: String,
        profile: String,
        rules: [RulePayload],
        effectiveRules: [RulePayload] = [],
        chains: [ChainPayload]
    ) throws -> RuleTestResponse {
        let network = rawNetwork.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard network == "tcp" || network == "udp" else {
            throw RuleTestFailure.invalidNetwork
        }
        let target = rawTarget.trimmingCharacters(in: .whitespacesAndNewlines)
        let split = splitTarget(target)
        guard !split.host.isEmpty, let port = Int(split.port), (1...65535).contains(port) else {
            throw RuleTestFailure.invalidTarget
        }
        guard let defaultChain = chains.first else {
            throw RuleTestFailure.noChains
        }

        let routeRules = effectiveRules.isEmpty ? rules : effectiveRules
        for (index, rule) in routeRules.enumerated() where matches(rule: rule, network: network, host: split.host, port: port) {
            let parsed = parseAction(rule.action)
            return response(
                profile: profile,
                decision: RuleTestDecisionPayload(
                    ruleName: rule.name.isEmpty ? "unnamed" : rule.name,
                    ruleNumber: index + 1,
                    action: parsed.action,
                    chainName: parsed.chainName,
                    target: target,
                    targetHost: split.host,
                    targetPort: String(port),
                    network: network
                ),
                chains: chains
            )
        }

        return response(
            profile: profile,
            decision: RuleTestDecisionPayload(
                ruleNumber: routeRules.count + 1,
                action: "chain",
                chainName: defaultChain.name,
                target: target,
                targetHost: split.host,
                targetPort: String(port),
                network: network,
                isDefault: true
            ),
            chains: chains
        )
    }

    private static func response(profile: String, decision: RuleTestDecisionPayload, chains: [ChainPayload]) -> RuleTestResponse {
        guard decision.action == "chain",
              let chain = chains.first(where: { $0.name == decision.chainName }) else {
            return RuleTestResponse(profile: profile, decision: decision)
        }
        return RuleTestResponse(
            profile: profile,
            decision: decision,
            chain: RuleTestChainPayload(name: chain.name, hopCount: chain.hopCount, capabilities: chain.capabilities),
            hops: chain.servers
        )
    }

    private static func matches(rule: RulePayload, network: String, host: String, port: Int) -> Bool {
        if !rule.networks.isEmpty && !rule.networks.map({ $0.lowercased() }).contains(network) {
            return false
        }
        if !rule.ports.isEmpty && !rule.ports.contains(port) {
            return false
        }
        if hasDomainMatchers(rule), !matchesDomain(rule: rule, host: host) {
            return false
        }
        if !rule.cidrs.isEmpty && !matchesCIDR(cidrs: rule.cidrs, host: host) {
            return false
        }
        return true
    }

    private static func hasDomainMatchers(_ rule: RulePayload) -> Bool {
        !rule.domains.isEmpty || !rule.domainSuffixes.isEmpty || !rule.domainKeywords.isEmpty
    }

    private static func matchesDomain(rule: RulePayload, host rawHost: String) -> Bool {
        let host = normalizeHost(rawHost)
        guard !host.isEmpty else { return false }
        if rule.domains.map(normalizeHost).contains(host) {
            return true
        }
        if rule.domainSuffixes.map(normalizeHost).contains(where: { host == $0 || host.hasSuffix("." + $0) }) {
            return true
        }
        if rule.domainKeywords.map(normalizeHost).contains(where: { host.contains($0) }) {
            return true
        }
        return false
    }

    private static func matchesCIDR(cidrs: [String], host: String) -> Bool {
        guard let ip = ipBytes(normalizeHost(host)) else { return false }
        return cidrs.contains { cidr in
            let parts = cidr.split(separator: "/", maxSplits: 1).map(String.init)
            guard parts.count == 2,
                  let prefixIP = ipBytes(parts[0]),
                  prefixIP.count == ip.count,
                  let prefixLen = Int(parts[1]),
                  prefixLen >= 0,
                  prefixLen <= ip.count * 8 else {
                return false
            }
            return bytes(ip, match: prefixIP, prefixLen: prefixLen)
        }
    }

    private static func bytes(_ value: [UInt8], match prefix: [UInt8], prefixLen: Int) -> Bool {
        let fullBytes = prefixLen / 8
        if fullBytes > 0 && Array(value.prefix(fullBytes)) != Array(prefix.prefix(fullBytes)) {
            return false
        }
        let remainingBits = prefixLen % 8
        if remainingBits == 0 {
            return true
        }
        let mask = UInt8(0xff << UInt8(8 - remainingBits))
        return (value[fullBytes] & mask) == (prefix[fullBytes] & mask)
    }

    private static func ipBytes(_ host: String) -> [UInt8]? {
        var ipv4 = in_addr()
        if inet_pton(AF_INET, host, &ipv4) == 1 {
            return withUnsafeBytes(of: ipv4) { Array($0) }
        }
        var ipv6 = in6_addr()
        if inet_pton(AF_INET6, host, &ipv6) == 1 {
            return withUnsafeBytes(of: ipv6) { Array($0) }
        }
        return nil
    }

    private static func parseAction(_ rawAction: String) -> (action: String, chainName: String) {
        let action = rawAction.trimmingCharacters(in: .whitespacesAndNewlines)
        let lower = action.lowercased()
        if lower == "direct" || lower == "block" || lower == "reject" {
            return (lower, "")
        }
        if lower.hasPrefix("chain:") {
            return ("chain", String(action.dropFirst("chain:".count)).trimmingCharacters(in: .whitespacesAndNewlines))
        }
        return ("chain", action)
    }

    private static func splitTarget(_ target: String) -> (host: String, port: String) {
        if target.hasPrefix("["),
           let close = target.firstIndex(of: "]"),
           target.index(after: close) < target.endIndex,
           target[target.index(after: close)] == ":" {
            let host = String(target[target.index(after: target.startIndex)..<close])
            let port = String(target[target.index(close, offsetBy: 2)...])
            return (normalizeHost(host), port)
        }
        guard let separator = target.lastIndex(of: ":"), separator > target.startIndex else {
            return (normalizeHost(target), "")
        }
        let host = String(target[..<separator])
        let port = String(target[target.index(after: separator)...])
        return (normalizeHost(host), port)
    }

    private static func normalizeHost(_ rawHost: String) -> String {
        rawHost
            .trimmingCharacters(in: .whitespacesAndNewlines)
            .trimmingCharacters(in: CharacterSet(charactersIn: "[]"))
            .trimmingCharacters(in: CharacterSet(charactersIn: "."))
            .lowercased()
    }
}
