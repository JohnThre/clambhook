import Foundation
#if canImport(Darwin)
import Darwin
#endif

public enum RuleMatcherKind: String, CaseIterable, Identifiable, Sendable {
    case domain
    case domainSuffix
    case domainKeyword
    case cidr
    case port
    case network
    case allTraffic
    case combined

    public var id: String { rawValue }

    public static var editableCases: [RuleMatcherKind] {
        [.domain, .domainSuffix, .domainKeyword, .cidr, .port, .network, .allTraffic]
    }

    public var displayName: String {
        switch self {
        case .domain:
            return "DOMAIN"
        case .domainSuffix:
            return "DOMAIN-SUFFIX"
        case .domainKeyword:
            return "DOMAIN-KEYWORD"
        case .cidr:
            return "IP-CIDR"
        case .port:
            return "PORT"
        case .network:
            return "NETWORK"
        case .allTraffic:
            return "FINAL"
        case .combined:
            return "COMBINED"
        }
    }

    public var valueLabel: String {
        switch self {
        case .domain:
            return "Domain"
        case .domainSuffix:
            return "Suffix"
        case .domainKeyword:
            return "Keyword"
        case .cidr:
            return "CIDR"
        case .port:
            return "Port"
        case .network:
            return "Network"
        case .allTraffic, .combined:
            return "Value"
        }
    }

    public var placeholder: String {
        switch self {
        case .domain:
            return "example.com"
        case .domainSuffix:
            return "example.com"
        case .domainKeyword:
            return "example"
        case .cidr:
            return "192.168.0.0/16"
        case .port:
            return "443"
        case .network:
            return "tcp"
        case .allTraffic:
            return "all traffic"
        case .combined:
            return "combined matchers"
        }
    }
}

public enum RulePolicyKind: String, CaseIterable, Identifiable, Sendable {
    case proxy
    case direct
    case block
    case reject

    public var id: String { rawValue }

    public var displayName: String {
        switch self {
        case .proxy:
            return "Proxy"
        case .direct:
            return "Direct"
        case .block:
            return "Block"
        case .reject:
            return "Reject"
        }
    }

    public static func parse(action: String) -> (kind: RulePolicyKind, chainName: String) {
        let trimmed = action.ruleEditorTrimmed
        let lower = trimmed.lowercased()
        switch lower {
        case "direct":
            return (.direct, "")
        case "block":
            return (.block, "")
        case "reject":
            return (.reject, "")
        default:
            if lower.hasPrefix("chain:") {
                return (.proxy, String(trimmed.dropFirst("chain:".count)).ruleEditorTrimmed)
            }
            return (.proxy, trimmed)
        }
    }
}

public enum RuleEditorRowSource: String, Equatable, Sendable {
    case manual
    case generated
    case virtualFinal
}

public struct RuleEditorRow: Identifiable, Equatable, Sendable {
    public let id: UUID
    public var name: String
    public var matcherKind: RuleMatcherKind
    public var value: String
    public var policyKind: RulePolicyKind
    public var chainName: String
    public var compatibilityRule: RulePayload?
    public var source: RuleEditorRowSource

    public init(
        id: UUID = UUID(),
        name: String,
        matcherKind: RuleMatcherKind,
        value: String = "",
        policyKind: RulePolicyKind = .proxy,
        chainName: String = "",
        compatibilityRule: RulePayload? = nil,
        source: RuleEditorRowSource = .manual
    ) {
        self.id = id
        self.name = name
        self.matcherKind = matcherKind
        self.value = value
        self.policyKind = policyKind
        self.chainName = chainName
        self.compatibilityRule = compatibilityRule
        self.source = source
    }

    public var isGenerated: Bool { source == .generated }
    public var isVirtualFinal: Bool { source == .virtualFinal }
    public var isEditable: Bool { source != .generated }

    public var encodedAction: String {
        switch policyKind {
        case .proxy:
            return "chain:\(chainName.ruleEditorTrimmed)"
        case .direct:
            return "direct"
        case .block:
            return "block"
        case .reject:
            return "reject"
        }
    }

    public var policySummary: String {
        switch policyKind {
        case .proxy:
            let chain = chainName.ruleEditorTrimmed
            return chain.isEmpty ? "Proxy" : "Proxy: \(chain)"
        default:
            return policyKind.displayName
        }
    }

    public var matcherSummary: String {
        let trimmedValue = value.ruleEditorTrimmed
        switch matcherKind {
        case .domain:
            return trimmedValue.isEmpty ? matcherKind.displayName : trimmedValue
        case .domainSuffix:
            return trimmedValue.isEmpty ? matcherKind.displayName : "*.\(trimmedValue)"
        case .domainKeyword:
            return trimmedValue.isEmpty ? matcherKind.displayName : trimmedValue
        case .cidr, .port:
            return trimmedValue.isEmpty ? matcherKind.displayName : trimmedValue
        case .network:
            return trimmedValue.isEmpty ? matcherKind.displayName : trimmedValue.uppercased()
        case .allTraffic:
            return "Any target"
        case .combined:
            return trimmedValue.isEmpty ? "Combined matchers" : trimmedValue
        }
    }

    public func isUnchangedVirtualFinal(defaultChainName: String) -> Bool {
        source == .virtualFinal &&
            matcherKind == .allTraffic &&
            name == "FINAL" &&
            policyKind == .proxy &&
            chainName == defaultChainName
    }
}

public struct RuleEditorValidationError: Equatable, Sendable {
    public var rowIndex: Int
    public var message: String

    public init(rowIndex: Int, message: String) {
        self.rowIndex = rowIndex
        self.message = message
    }
}

public struct RuleEditorValidationFailure: Error, LocalizedError, Equatable, Sendable {
    public var errors: [RuleEditorValidationError]

    public init(errors: [RuleEditorValidationError]) {
        self.errors = errors
    }

    public var errorDescription: String? {
        guard let first = errors.first else {
            return "Rule validation failed."
        }
        if errors.count == 1 {
            return "Rule \(first.rowIndex + 1): \(first.message)"
        }
        return "Rule \(first.rowIndex + 1): \(first.message) (+\(errors.count - 1) more)"
    }
}

public enum RuleEditor {
    public static func rows(
        from rules: [RulePayload],
        source: RuleEditorRowSource = .manual,
        defaultChainName: String = "",
        includeVirtualFinal: Bool = false
    ) -> [RuleEditorRow] {
        var out = rules.flatMap { rows(from: $0, source: source) }
        if includeVirtualFinal && !rules.contains(where: isFinalRule) {
            out.append(virtualFinalRow(defaultChainName: defaultChainName))
        }
        return out
    }

    public static func rules(from rows: [RuleEditorRow], chainNames: [String], defaultChainName: String = "") throws -> [RulePayload] {
        let errors = validate(rows: rows, chainNames: chainNames, defaultChainName: defaultChainName)
        if !errors.isEmpty {
            throw RuleEditorValidationFailure(errors: errors)
        }
        return rows.compactMap { row in
            if row.isGenerated || row.isUnchangedVirtualFinal(defaultChainName: defaultChainName) {
                return nil
            }
            return rule(from: row)
        }
    }

    public static func validate(rows: [RuleEditorRow], chainNames: [String], defaultChainName: String = "") -> [RuleEditorValidationError] {
        var errors: [RuleEditorValidationError] = []
        let knownChains = Set(chainNames)
        for (index, row) in rows.enumerated() {
            if row.isGenerated {
                continue
            }
            let name = row.name.ruleEditorTrimmed
            if name.isEmpty {
                errors.append(.init(rowIndex: index, message: "name is required"))
            } else if name != row.name {
                errors.append(.init(rowIndex: index, message: "name must not have surrounding whitespace"))
            }

            if row.matcherKind == .allTraffic && index != rows.count - 1 {
                errors.append(.init(rowIndex: index, message: "all traffic must be last"))
            }
            if row.matcherKind == .combined && row.compatibilityRule == nil {
                errors.append(.init(rowIndex: index, message: "combined rule is missing its original matchers"))
            }

            validateMatcher(row: row, rowIndex: index, errors: &errors)
            validatePolicy(row: row, rowIndex: index, knownChains: knownChains, errors: &errors)
        }
        return errors
    }

    private static func rows(from rule: RulePayload, source: RuleEditorRowSource) -> [RuleEditorRow] {
        let policy = RulePolicyKind.parse(action: rule.action)
        let families = matcherFamilies(for: rule)
        if families.isEmpty {
            return [
                RuleEditorRow(
                    name: rule.name,
                    matcherKind: .allTraffic,
                    policyKind: policy.kind,
                    chainName: policy.chainName,
                    compatibilityRule: source == .generated ? rule : nil,
                    source: source
                )
            ]
        }
        if families.count == 1 {
            return families[0].values.map { value in
                RuleEditorRow(
                    name: rule.name,
                    matcherKind: families[0].kind,
                    value: value,
                    policyKind: policy.kind,
                    chainName: policy.chainName,
                    compatibilityRule: source == .generated ? rule : nil,
                    source: source
                )
            }
        }
        return [
            RuleEditorRow(
                name: rule.name,
                matcherKind: .combined,
                value: summary(for: rule),
                policyKind: policy.kind,
                chainName: policy.chainName,
                compatibilityRule: rule,
                source: source
            )
        ]
    }

    private static func virtualFinalRow(defaultChainName: String) -> RuleEditorRow {
        RuleEditorRow(
            name: "FINAL",
            matcherKind: .allTraffic,
            policyKind: .proxy,
            chainName: defaultChainName,
            source: .virtualFinal
        )
    }

    private static func isFinalRule(_ rule: RulePayload) -> Bool {
        matcherFamilies(for: rule).isEmpty
    }

    private static func matcherFamilies(for rule: RulePayload) -> [MatcherFamily] {
        var families: [MatcherFamily] = []
        if !rule.domains.isEmpty {
            families.append(.init(kind: .domain, values: rule.domains))
        }
        if !rule.domainSuffixes.isEmpty {
            families.append(.init(kind: .domainSuffix, values: rule.domainSuffixes))
        }
        if !rule.domainKeywords.isEmpty {
            families.append(.init(kind: .domainKeyword, values: rule.domainKeywords))
        }
        if !rule.cidrs.isEmpty {
            families.append(.init(kind: .cidr, values: rule.cidrs))
        }
        if !rule.ports.isEmpty {
            families.append(.init(kind: .port, values: rule.ports.map(String.init)))
        }
        if !rule.networks.isEmpty {
            families.append(.init(kind: .network, values: rule.networks))
        }
        return families
    }

    private static func validateMatcher(row: RuleEditorRow, rowIndex: Int, errors: inout [RuleEditorValidationError]) {
        switch row.matcherKind {
        case .allTraffic, .combined:
            return
        case .domain, .domainSuffix, .domainKeyword, .cidr, .port, .network:
            break
        }

        let value = row.value.ruleEditorTrimmed
        if value.isEmpty {
            errors.append(.init(rowIndex: rowIndex, message: "\(row.matcherKind.valueLabel.lowercased()) is required"))
            return
        }
        if value != row.value {
            errors.append(.init(rowIndex: rowIndex, message: "\(row.matcherKind.valueLabel.lowercased()) must not have surrounding whitespace"))
            return
        }

        switch row.matcherKind {
        case .cidr:
            if !isValidCIDR(value) {
                errors.append(.init(rowIndex: rowIndex, message: "CIDR is invalid"))
            }
        case .port:
            guard let port = Int(value), (0...65_535).contains(port) else {
                errors.append(.init(rowIndex: rowIndex, message: "port must be 0 through 65535"))
                return
            }
        case .network:
            switch value.lowercased() {
            case "tcp", "udp":
                break
            default:
                errors.append(.init(rowIndex: rowIndex, message: "network must be TCP or UDP"))
            }
        default:
            break
        }
    }

    private static func validatePolicy(
        row: RuleEditorRow,
        rowIndex: Int,
        knownChains: Set<String>,
        errors: inout [RuleEditorValidationError]
    ) {
        guard row.policyKind == .proxy else {
            return
        }
        let chain = row.chainName.ruleEditorTrimmed
        if chain.isEmpty {
            errors.append(.init(rowIndex: rowIndex, message: "proxy chain is required"))
        } else if chain != row.chainName {
            errors.append(.init(rowIndex: rowIndex, message: "proxy chain must not have surrounding whitespace"))
        } else if !knownChains.contains(chain) {
            errors.append(.init(rowIndex: rowIndex, message: "proxy chain \(chain) was not found"))
        }
    }

    private static func rule(from row: RuleEditorRow) -> RulePayload {
        if row.matcherKind == .combined {
            var rule = row.compatibilityRule ?? RulePayload()
            rule.name = row.name.ruleEditorTrimmed
            rule.action = row.encodedAction
            return rule
        }

        var rule = RulePayload(name: row.name.ruleEditorTrimmed, action: row.encodedAction)
        let value = row.value.ruleEditorTrimmed
        switch row.matcherKind {
        case .domain:
            rule.domains = [value]
        case .domainSuffix:
            rule.domainSuffixes = [value]
        case .domainKeyword:
            rule.domainKeywords = [value]
        case .cidr:
            rule.cidrs = [value]
        case .port:
            rule.ports = [Int(value) ?? 0]
        case .network:
            rule.networks = [value.lowercased()]
        case .allTraffic, .combined:
            break
        }
        return rule
    }

    private static func summary(for rule: RulePayload) -> String {
        var parts: [String] = []
        parts.append(contentsOf: rule.domains)
        parts.append(contentsOf: rule.domainSuffixes.map { "*.\($0)" })
        parts.append(contentsOf: rule.domainKeywords.map { "keyword:\($0)" })
        parts.append(contentsOf: rule.cidrs)
        parts.append(contentsOf: rule.ports.map(String.init))
        parts.append(contentsOf: rule.networks.map { $0.uppercased() })
        return parts.joined(separator: " / ")
    }

    private static func isValidCIDR(_ raw: String) -> Bool {
        let parts = raw.split(separator: "/", omittingEmptySubsequences: false)
        guard parts.count == 2,
              let prefix = Int(parts[1]) else {
            return false
        }
        let address = String(parts[0])
        if address.contains(":") {
            return (0...128).contains(prefix) && isValidIPAddress(address, family: AF_INET6)
        }
        return (0...32).contains(prefix) && isValidIPAddress(address, family: AF_INET)
    }

    private static func isValidIPAddress(_ raw: String, family: Int32) -> Bool {
        #if canImport(Darwin)
        if family == AF_INET {
            var address = in_addr()
            return raw.withCString { inet_pton(family, $0, &address) == 1 }
        }
        var address = in6_addr()
        return raw.withCString { inet_pton(family, $0, &address) == 1 }
        #else
        return !raw.isEmpty
        #endif
    }
}

private struct MatcherFamily {
    var kind: RuleMatcherKind
    var values: [String]
}

private extension String {
    var ruleEditorTrimmed: String {
        trimmingCharacters(in: .whitespacesAndNewlines)
    }
}
