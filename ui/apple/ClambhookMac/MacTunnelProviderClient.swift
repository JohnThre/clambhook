import ClambhookMobile
import ClambhookShared
import Foundation
import NetworkExtension

@MainActor
final class MacTunnelProviderClient: NSObject, ClambhookDashboardProviding, ClambhookRuleEditing, ClambhookRouteExplaining, ClambhookPolicyGroupEditing, ClambhookRuleSetEditing, ClambhookRuleSubscriptionEditing, ClambhookConfigSettingsProviding {
    private let groupIdentifier: String
    private let systemExtensionInstaller: MacSystemExtensionInstaller
    private let providerBundleIdentifier = clambhookMacTunnelBundleIdentifier
    private let legacyProviderBundleIdentifiers = [clambhookLegacyMacTunnelBundleIdentifier]
    private let tunnelDescription = "ClambHook"
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()

    init(
        groupIdentifier: String = defaultAppGroupIdentifier,
        systemExtensionInstaller: MacSystemExtensionInstaller
    ) {
        self.groupIdentifier = groupIdentifier
        self.systemExtensionInstaller = systemExtensionInstaller
        super.init()
    }

    func dashboard() async throws -> TunnelDashboardPayload {
        try await decodeDashboard(try await dashboardJSON())
    }

    func status() async throws -> StatusPayload {
        try await dashboard().status
    }

    func profiles() async throws -> ProfilesPayload {
        try await dashboard().profiles
    }

    func servers() async throws -> ServersPayload {
        try await dashboard().servers
    }

    func policyGroups() async throws -> PolicyGroupsPayload {
        try await dashboard().policyGroups
    }

    func rules() async throws -> RulesPayload {
        try await dashboard().rules
    }

    func dns() async throws -> DNSPayload {
        try await dashboard().dns
    }

    func traffic() async throws -> TrafficSnapshotPayload {
        try await dashboard().traffic
    }

    func connect() async throws {
        _ = try TunnelConfigStore.loadOrCreateConfig(groupIdentifier: groupIdentifier)
        let path = configPath()
        try mobileBool { MobileValidateUsableTunnelConfig(path, $0) }
        try await systemExtensionInstaller.prepareForTunnelStart()
        let manager = try await loadOrCreateManager(configPath: path)
        guard let session = manager.connection as? NETunnelProviderSession else {
            throw MacTunnelProviderError.missingTunnelSession
        }
        try session.startTunnel(options: ["config_path": path as NSString])
    }

    func disconnect() async throws {
        guard let session = try await tunnelSession(requireConnected: false) else {
            return
        }
        session.stopTunnel()
    }

    func setActiveProfile(_ name: String) async throws {
        if try await isConnected() {
            _ = try await sendCommand(.init(action: .setActiveProfile, profile: name))
            try await restartTunnel()
            return
        }
        try mobileBool { MobileSetActiveTunnelProfileConfig(configPath(), name, $0) }
    }

    func selectPolicyGroup(profile: String, group: String, chain: String) async throws -> PolicyGroupsPayload {
        if try await isConnected() {
            return try await sendJSONCommand(
                .init(action: .selectPolicyGroup, profile: profile, group: group, chain: chain),
                as: PolicyGroupsPayload.self
            )
        }
        let raw = try mobileString { MobileSelectPolicyGroupJSON(configPath(), profile, group, chain, $0) }
        return try decodeJSON(raw, as: PolicyGroupsPayload.self)
    }

    func testRule(network: String, target: String, profile: String) async throws -> RuleTestResponse {
        if try await isConnected() {
            return try await sendJSONCommand(
                .init(action: .testRule, profile: profile, network: network, target: target),
                as: RuleTestResponse.self
            )
        }
        let raw = try mobileString { MobileTestRuleJSON(configPath(), profile, network, target, "", $0) }
        return try decodeJSON(raw, as: RuleTestResponse.self)
    }

    func explainRoute(network: String, target: String, source: String, profile: String) async throws -> RuleTestResponse {
        if try await isConnected() {
            return try await sendJSONCommand(
                .init(action: .explainRoute, profile: profile, network: network, target: target, source: source),
                as: RuleTestResponse.self
            )
        }
        let raw = try mobileString { MobileTestRuleJSON(configPath(), profile, network, target, source, $0) }
        return try decodeJSON(raw, as: RuleTestResponse.self)
    }

    func createRule(_ rule: RulePayload) async throws -> RulesPayload {
        let current = try await rules()
        return try await replaceRules(current.rules + [rule], profile: current.profile)
    }

    func createRuleFromConnection(connID: String, profile: String, name: String, action: String, scope: String) async throws -> RulesPayload {
        throw MacTunnelProviderError.unsupported("Permanent rule creation from connection history is only available in daemon mode.")
    }

    func createTemporaryRuleFromConnection(connID: String, profile: String, name: String, action: String, scope: String, ttlSeconds: Int) async throws -> TemporaryRuleCreateResponsePayload {
        try await sendJSONCommand(
            .init(
                action: .createTemporaryRuleFromConnection,
                profile: profile,
                connID: connID,
                name: name,
                ruleAction: action,
                scope: scope,
                ttlSeconds: ttlSeconds
            ),
            as: TemporaryRuleCreateResponsePayload.self
        )
    }

    func cleanupRule(_ suggestion: TrafficCleanupSuggestionPayload) async throws -> RulesPayload {
        throw MacTunnelProviderError.unsupported("Rule cleanup suggestions are only available in daemon mode.")
    }

    func replaceRules(_ rules: [RulePayload], profile: String) async throws -> RulesPayload {
        if try await isConnected() {
            return try await sendJSONCommand(
                .init(action: .replaceRules, profile: profile, rules: rules),
                as: RulesPayload.self
            )
        }
        let rawRules = try encodeJSONString(rules)
        try mobileBool { MobileReplaceTunnelRulesJSON(configPath(), profile, rawRules, $0) }
        return try await self.rules()
    }

    func replacePolicyGroups(_ groups: [PolicyGroupPayload], profile: String) async throws -> PolicyGroupsPayload {
        if try await isConnected() {
            return try await sendJSONCommand(
                .init(action: .replacePolicyGroups, profile: profile, policyGroups: groups),
                as: PolicyGroupsPayload.self
            )
        }
        let rawGroups = try encodeJSONString(groups)
        try mobileBool { MobileReplaceTunnelPolicyGroupsJSON(configPath(), profile, rawGroups, $0) }
        return try await policyGroups()
    }

    func replaceRuleSets(_ ruleSets: [RuleSetPayload], profile: String) async throws -> RuleSetsPayload {
        if try await isConnected() {
            return try await sendJSONCommand(
                .init(action: .replaceRuleSets, profile: profile, ruleSets: ruleSets),
                as: RuleSetsPayload.self
            )
        }
        let rawRuleSets = try encodeJSONString(ruleSets)
        try mobileBool { MobileReplaceTunnelRuleSetsJSON(configPath(), profile, rawRuleSets, $0) }
        return try decodeJSON(try mobileString { MobileRuleSetsJSON(configPath(), profile, $0) }, as: RuleSetsPayload.self)
    }

    func refreshRuleSets(names: [String], profile: String) async throws -> RuleSetsPayload {
        if try await isConnected() {
            return try await sendJSONCommand(
                .init(action: .refreshRuleSets, profile: profile, names: names),
                as: RuleSetsPayload.self
            )
        }
        let rawNames = try encodeJSONString(names)
        return try decodeJSON(try mobileString { MobileRefreshRuleSetsJSON(configPath(), profile, rawNames, $0) }, as: RuleSetsPayload.self)
    }

    func replaceRuleSubscriptions(_ subscriptions: [RuleSubscriptionPayload], profile: String) async throws -> RuleSubscriptionsPayload {
        if try await isConnected() {
            return try await sendJSONCommand(
                .init(action: .replaceRuleSubscriptions, profile: profile, ruleSubscriptions: subscriptions),
                as: RuleSubscriptionsPayload.self
            )
        }
        let rawSubscriptions = try encodeJSONString(subscriptions)
        try mobileBool { MobileReplaceTunnelRuleSubscriptionsJSON(configPath(), profile, rawSubscriptions, $0) }
        return try decodeJSON(try mobileString { MobileRuleSubscriptionsJSON(configPath(), profile, $0) }, as: RuleSubscriptionsPayload.self)
    }

    func refreshRuleSubscriptions(names: [String], profile: String) async throws -> RuleSubscriptionsPayload {
        if try await isConnected() {
            return try await sendJSONCommand(
                .init(action: .refreshRuleSubscriptions, profile: profile, names: names),
                as: RuleSubscriptionsPayload.self
            )
        }
        let rawNames = try encodeJSONString(names)
        return try decodeJSON(try mobileString { MobileRefreshRuleSubscriptionsJSON(configPath(), profile, rawNames, $0) }, as: RuleSubscriptionsPayload.self)
    }

    func configSettings(profile: String) async throws -> ConfigSettingsPayload {
        throw MacTunnelProviderError.unsupported("Daemon listener settings are unavailable in Network Extension mode.")
    }

    func updateConfigSettings(_ request: ConfigSettingsUpdateRequest) async throws -> ConfigSettingsPayload {
        throw MacTunnelProviderError.unsupported("Daemon listener settings are unavailable in Network Extension mode.")
    }

    private func dashboardJSON() async throws -> String {
        if try await isConnected() {
            return try await sendCommand(.init(action: .dashboard)).payload ?? "{}"
        }
        return try mobileString { MobileTunnelConfigDashboardJSON(configPath(), $0) }
    }

    private func configPath() -> String {
        TunnelConfigStore.configURL(groupIdentifier: groupIdentifier).path
    }

    private func decodeDashboard(_ raw: String) async throws -> TunnelDashboardPayload {
        try decodeJSON(raw, as: TunnelDashboardPayload.self)
    }

    private func decodeJSON<T: Decodable>(_ raw: String, as type: T.Type) throws -> T {
        try decoder.decode(T.self, from: Data(raw.utf8))
    }

    private func encodeJSONString<T: Encodable>(_ value: T) throws -> String {
        String(data: try encoder.encode(value), encoding: .utf8) ?? ""
    }

    private func tunnelNetworkSettingsPayload(configPath: String) throws -> TunnelNetworkSettingsPayload {
        let rawSettings = try mobileString { MobileTunnelNetworkSettingsJSON(configPath, $0) }
        return try decoder.decode(TunnelNetworkSettingsPayload.self, from: Data(rawSettings.utf8))
    }

    private func usesFullTunnelRoutes(_ payload: TunnelNetworkSettingsPayload) throws -> Bool {
        let includedRoutes = try payload.includedRoutePrefixes()
        _ = try payload.excludedRoutePrefixes()
        return includedRoutes.contains(where: \.isIPv4DefaultRoute) && includedRoutes.contains(where: \.isIPv6DefaultRoute)
    }

    private func sendJSONCommand<T: Decodable>(_ command: TunnelCommand, as type: T.Type) async throws -> T {
        let response = try await sendCommand(command)
        return try decodeJSON(response.payload ?? "{}", as: type)
    }

    private func sendCommand(_ command: TunnelCommand) async throws -> TunnelCommandResponse {
        guard let session = try await tunnelSession(requireConnected: true) else {
            throw MacTunnelProviderError.notConnected
        }
        let data = try encoder.encode(command)
        let responseData: Data = try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Data, Error>) in
            do {
                try session.sendProviderMessage(data) { data in
                    continuation.resume(returning: data ?? Data())
                }
            } catch {
                continuation.resume(throwing: error)
            }
        }
        let response = try decoder.decode(TunnelCommandResponse.self, from: responseData)
        if response.ok {
            return response
        }
        throw MacTunnelProviderError.provider(response.error ?? "provider command failed")
    }

    private func isConnected() async throws -> Bool {
        guard let session = try await tunnelSession(requireConnected: false) else {
            return false
        }
        return session.status == .connected
    }

    private func restartTunnel() async throws {
        guard let session = try await tunnelSession(requireConnected: false) else {
            try await connect()
            return
        }
        session.stopTunnel()
        try await waitForTunnelStop(session)
        try await connect()
    }

    private func waitForTunnelStop(_ session: NETunnelProviderSession, timeout: TimeInterval = 5) async throws {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            switch session.status {
            case .connected, .connecting, .reasserting, .disconnecting:
                try await Task.sleep(nanoseconds: 100_000_000)
            default:
                return
            }
        }
        throw MacTunnelProviderError.provider("Network Extension tunnel did not stop during profile switch.")
    }

    private func tunnelSession(requireConnected: Bool) async throws -> NETunnelProviderSession? {
        let managers = try await loadManagers()
        guard let manager = managers.first(where: { providerProtocol(from: $0)?.providerBundleIdentifier == providerBundleIdentifier }) else {
            if requireConnected {
                throw MacTunnelProviderError.notConfigured
            }
            return nil
        }
        guard let session = manager.connection as? NETunnelProviderSession else {
            throw MacTunnelProviderError.missingTunnelSession
        }
        if requireConnected && session.status != .connected {
            throw MacTunnelProviderError.notConnected
        }
        return session
    }

    private func loadOrCreateManager(configPath: String) async throws -> NETunnelProviderManager {
        let managers = try await loadManagers()
        try await removeLegacyManagers(from: managers)
        let manager = managers.first(where: { providerProtocol(from: $0)?.providerBundleIdentifier == providerBundleIdentifier }) ?? NETunnelProviderManager()
        let settingsPayload = try tunnelNetworkSettingsPayload(configPath: configPath)
        let fullTunnel = try usesFullTunnelRoutes(settingsPayload)
        let proto = NETunnelProviderProtocol()
        proto.providerBundleIdentifier = providerBundleIdentifier
        proto.serverAddress = "clambhook"
        proto.providerConfiguration = [
            "config_path": configPath,
            "app_group": groupIdentifier,
        ]
        proto.includeAllNetworks = fullTunnel
        proto.enforceRoutes = fullTunnel
        proto.excludeLocalNetworks = false
        manager.localizedDescription = tunnelDescription
        manager.protocolConfiguration = proto
        manager.isEnabled = true
        try await save(manager)
        try await loadFromPreferences(manager)
        return manager
    }

    private func removeLegacyManagers(from managers: [NETunnelProviderManager]) async throws {
        for manager in managers {
            guard let bundleIdentifier = providerProtocol(from: manager)?.providerBundleIdentifier,
                  legacyProviderBundleIdentifiers.contains(bundleIdentifier)
            else {
                continue
            }
            if manager.connection.status == .connected || manager.connection.status == .connecting || manager.connection.status == .reasserting {
                continue
            }
            try await removeFromPreferences(manager)
        }
    }

    private func providerProtocol(from manager: NETunnelProviderManager) -> NETunnelProviderProtocol? {
        manager.protocolConfiguration as? NETunnelProviderProtocol
    }

    private func loadManagers() async throws -> [NETunnelProviderManager] {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<[NETunnelProviderManager], Error>) in
            NETunnelProviderManager.loadAllFromPreferences { managers, error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume(returning: managers ?? [])
                }
            }
        }
    }

    private func save(_ manager: NETunnelProviderManager) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            manager.saveToPreferences { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            }
        }
    }

    private func loadFromPreferences(_ manager: NETunnelProviderManager) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            manager.loadFromPreferences { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            }
        }
    }

    private func removeFromPreferences(_ manager: NETunnelProviderManager) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            manager.removeFromPreferences { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            }
        }
    }

}

private func mobileString(_ body: (NSErrorPointer) -> String) throws -> String {
    var error: NSError?
    let value = body(&error)
    if let error {
        throw error
    }
    return value
}

private func mobileBool(_ body: (NSErrorPointer) -> Bool) throws {
    var error: NSError?
    let ok = body(&error)
    if let error {
        throw error
    }
    if !ok {
        throw MacTunnelProviderError.mobileBridgeFailed
    }
}

private enum MacTunnelProviderError: Error, LocalizedError, Equatable {
    case missingTunnelSession
    case notConfigured
    case notConnected
    case startFailed
    case messageSendFailed
    case mobileBridgeFailed
    case provider(String)
    case unsupported(String)

    var errorDescription: String? {
        switch self {
        case .missingTunnelSession:
            return "Network Extension session is unavailable."
        case .notConfigured:
            return "Network Extension tunnel is not configured."
        case .notConnected:
            return "Network Extension tunnel is not connected."
        case .startFailed:
            return "Network Extension tunnel did not start."
        case .messageSendFailed:
            return "Network Extension provider message could not be sent."
        case .mobileBridgeFailed:
            return "Mobile tunnel bridge returned failure."
        case .provider(let message), .unsupported(let message):
            return message
        }
    }
}
