import ClambhookMobile
import ClambhookShared
import Foundation
import NetworkExtension

final class PacketTunnelProvider: NEPacketTunnelProvider, MobilePacketWriterProtocol {
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()
    private var runtime: MobileTunnelRuntime?
    private var configPath = ""
    private var appGroupIdentifier = defaultAppGroupIdentifier
    private var stopping = false

    override func startTunnel(options: [String: NSObject]? = nil, completionHandler: @escaping (Error?) -> Void) {
        do {
            let proto = protocolConfiguration as? NETunnelProviderProtocol
            let providerConfig = proto?.providerConfiguration ?? [:]
            if let group = providerConfig["app_group"] as? String, !group.isEmpty {
                appGroupIdentifier = group
            }
            if let path = options?["config_path"] as? String, !path.isEmpty {
                configPath = path
            } else if let path = providerConfig["config_path"] as? String, !path.isEmpty {
                configPath = path
            } else {
                configPath = TunnelConfigStore.configURL(groupIdentifier: appGroupIdentifier).path
            }
            _ = try TunnelConfigStore.loadOrCreateConfig(groupIdentifier: appGroupIdentifier)
            applyCurrentNetworkSettings { [weak self] error in
                guard let self else { return }
                if let error {
                    completionHandler(error)
                    return
                }
                do {
                    let runtime = MobileNewTunnelRuntime(self)
                    self.runtime = runtime
                    try runtime?.start(self.configPath)
                    self.stopping = false
                    self.readPackets()
                    completionHandler(nil)
                } catch {
                    completionHandler(error)
                }
            }
        } catch {
            completionHandler(error)
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        stopping = true
        let current = runtime
        runtime = nil
        do {
            try current?.stop()
        } catch {
            NSLog("clambhook tunnel stop error: \(error.localizedDescription)")
        }
        completionHandler()
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)? = nil) {
        do {
            let command = try decoder.decode(TunnelCommand.self, from: messageData)
            let payload = try perform(command)
            let response = TunnelCommandResponse.success(payload)
            completionHandler?(try encoder.encode(response))
        } catch {
            let response = TunnelCommandResponse.failure(error.localizedDescription)
            completionHandler?(try? encoder.encode(response))
        }
    }

    func writePacket(_ packet: Data?) throws {
        guard let packet, let first = packet.first else { return }
        let version = first >> 4
        let family: NSNumber
        switch version {
        case 4:
            family = NSNumber(value: AF_INET)
        case 6:
            family = NSNumber(value: AF_INET6)
        default:
            throw PacketTunnelProviderError.unsupportedIPVersion(Int(version))
        }
        let ok = packetFlow.writePackets([packet], withProtocols: [family])
        if !ok {
            throw PacketTunnelProviderError.packetWriteFailed
        }
    }

    private func readPackets() {
        packetFlow.readPackets { [weak self] packets, _ in
            guard let self, !self.stopping else { return }
            for packet in packets {
                do {
                    try self.runtime?.injectPacket(packet)
                } catch {
                    NSLog("clambhook packet inject error: \(error.localizedDescription)")
                }
            }
            self.readPackets()
        }
    }

    private func perform(_ command: TunnelCommand) throws -> String? {
        guard let runtime else {
            throw PacketTunnelProviderError.runtimeUnavailable
        }
        switch command.action {
        case .dashboard:
            return try runtimeString { runtime.dashboardJSON($0) }
        case .status:
            return try runtimeString { runtime.statusJSON($0) }
        case .profiles:
            return try runtimeString { runtime.profilesJSON($0) }
        case .servers:
            return try runtimeString { runtime.serversJSON($0) }
        case .policyGroups:
            return try dashboardField(\.policyGroups)
        case .rules:
            return try runtimeString { runtime.rulesJSON($0) }
        case .ruleSets:
            return try dashboardField(\.ruleSets)
        case .ruleSubscriptions:
            return try dashboardField(\.ruleSubscriptions)
        case .dns:
            return try dashboardField(\.dns)
        case .traffic:
            return try runtimeString { runtime.trafficJSON($0) }
        case .reload:
            try runtime.reload(configPath)
            try reapplyCurrentNetworkSettings()
            return try runtimeString { runtime.dashboardJSON($0) }
        case .setActiveProfile:
            try mobileBool { MobileSetActiveTunnelProfileConfig(configPath, command.profile ?? "", $0) }
            try runtime.reload(configPath)
            try reapplyCurrentNetworkSettings()
            return try runtimeString { runtime.profilesJSON($0) }
        case .selectPolicyGroup:
            let raw = try mobileString {
                MobileSelectPolicyGroupJSON(configPath, command.profile ?? "", command.group ?? "", command.chain ?? "", $0)
            }
            try runtime.reload(configPath)
            return raw
        case .testRule, .explainRoute:
            return try mobileString {
                MobileTestRuleJSON(
                    configPath,
                    command.profile ?? "",
                    command.network ?? "",
                    command.target ?? "",
                    command.source ?? "",
                    $0
                )
            }
        case .createRule:
            let dashboard = try currentDashboard()
            guard let rule = command.rule else {
                throw PacketTunnelProviderError.missingPayload("rule")
            }
            let profile = command.profile ?? dashboard.rules.profile
            let rules = dashboard.rules.rules + [rule]
            return try replaceRules(rules, profile: profile, runtime: runtime)
        case .createRuleFromConnection:
            throw PacketTunnelProviderError.unsupported("Permanent rule creation from connection history is only available in daemon mode.")
        case .createTemporaryRuleFromConnection:
            return try runtimeString {
                runtime.createTemporaryRule(
                    fromConnectionJSON: command.connID ?? "",
                    profileName: command.profile ?? "",
                    name: command.name ?? "",
                    action: command.ruleAction ?? "",
                    scope: command.scope ?? "auto",
                    ttlSeconds: Int64(command.ttlSeconds ?? 900),
                    error: $0
                )
            }
        case .replaceRules:
            return try replaceRules(command.rules ?? [], profile: command.profile ?? "", runtime: runtime)
        case .replacePolicyGroups:
            let rawGroups = try encodeJSONString(command.policyGroups ?? [])
            try mobileBool { MobileReplaceTunnelPolicyGroupsJSON(configPath, command.profile ?? "", rawGroups, $0) }
            try runtime.reload(configPath)
            return try dashboardField(\.policyGroups)
        case .replaceRuleSets:
            let rawRuleSets = try encodeJSONString(command.ruleSets ?? [])
            try mobileBool { MobileReplaceTunnelRuleSetsJSON(configPath, command.profile ?? "", rawRuleSets, $0) }
            try runtime.reload(configPath)
            return try mobileString { MobileRuleSetsJSON(configPath, command.profile ?? "", $0) }
        case .refreshRuleSets:
            let rawNames = try encodeJSONString(command.names ?? [])
            let raw = try mobileString { MobileRefreshRuleSetsJSON(configPath, command.profile ?? "", rawNames, $0) }
            try runtime.reload(configPath)
            return raw
        case .replaceRuleSubscriptions:
            let rawSubscriptions = try encodeJSONString(command.ruleSubscriptions ?? [])
            try mobileBool { MobileReplaceTunnelRuleSubscriptionsJSON(configPath, command.profile ?? "", rawSubscriptions, $0) }
            try runtime.reload(configPath)
            return try mobileString { MobileRuleSubscriptionsJSON(configPath, command.profile ?? "", $0) }
        case .refreshRuleSubscriptions:
            let rawNames = try encodeJSONString(command.names ?? [])
            let raw = try mobileString { MobileRefreshRuleSubscriptionsJSON(configPath, command.profile ?? "", rawNames, $0) }
            try runtime.reload(configPath)
            return raw
        case .developerStatus:
            return try runtimeString { runtime.developerStatusJSON($0) }
        case .developerEntries:
            return try runtimeString { runtime.developerEntriesJSON($0) }
        case .developerCA:
            return try runtimeString { runtime.developerCAPEM($0) }
        case .developerHAR:
            return try runtimeString { runtime.developerHARJSON($0) }
        case .clearDeveloperEntries:
            runtime.clearDeveloperEntries()
            return nil
        }
    }

    private func replaceRules(_ rules: [RulePayload], profile: String, runtime: MobileTunnelRuntime) throws -> String {
        let rawRules = try encodeJSONString(rules)
        try mobileBool { MobileReplaceTunnelRulesJSON(configPath, profile, rawRules, $0) }
        try runtime.reload(configPath)
        return try runtimeString { runtime.rulesJSON($0) }
    }

    private func dashboardField<T: Encodable>(_ keyPath: KeyPath<TunnelDashboardPayload, T>) throws -> String {
        try encodeJSONString(currentDashboard()[keyPath: keyPath])
    }

    private func currentDashboard() throws -> TunnelDashboardPayload {
        guard let runtime else {
            throw PacketTunnelProviderError.runtimeUnavailable
        }
        let raw = try runtimeString { runtime.dashboardJSON($0) }
        return try decoder.decode(TunnelDashboardPayload.self, from: Data(raw.utf8))
    }

    private func currentPacketTunnelSettings() throws -> NEPacketTunnelNetworkSettings {
        let rawSettings = try mobileString { MobileTunnelNetworkSettingsJSON(configPath, $0) }
        let settingsPayload = try decoder.decode(TunnelNetworkSettingsPayload.self, from: Data(rawSettings.utf8))
        return try packetTunnelSettings(from: settingsPayload)
    }

    private func applyCurrentNetworkSettings(completionHandler: @escaping (Error?) -> Void) {
        do {
            setTunnelNetworkSettings(try currentPacketTunnelSettings(), completionHandler: completionHandler)
        } catch {
            completionHandler(error)
        }
    }

    private func reapplyCurrentNetworkSettings() throws {
        let networkSettings = try currentPacketTunnelSettings()
        let semaphore = DispatchSemaphore(value: 0)
        var applyError: Error?
        setTunnelNetworkSettings(networkSettings) { error in
            applyError = error
            semaphore.signal()
        }
        if semaphore.wait(timeout: .now() + 10) == .timedOut {
            throw PacketTunnelProviderError.networkSettingsTimedOut
        }
        if let applyError {
            throw applyError
        }
    }

    private func encodeJSONString<T: Encodable>(_ value: T) throws -> String {
        String(data: try encoder.encode(value), encoding: .utf8) ?? ""
    }
}

private func packetTunnelSettings(from payload: TunnelNetworkSettingsPayload) throws -> NEPacketTunnelNetworkSettings {
    let includedRoutes = try payload.includedRoutePrefixes()
    let excludedRoutes = try payload.excludedRoutePrefixes()
    let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: payload.remoteAddress.isEmpty ? "127.0.0.1" : payload.remoteAddress)
    settings.mtu = NSNumber(value: max(payload.mtu, 1280))
    if !payload.ipv4.isEmpty {
        settings.ipv4Settings = NEIPv4Settings(
            addresses: payload.ipv4.map(\.address),
            subnetMasks: payload.ipv4.map { ipv4Mask(prefixLength: $0.prefixLen) }
        )
        settings.ipv4Settings?.includedRoutes = includedRoutes.filter(\.isIPv4).map(ipv4Route)
        settings.ipv4Settings?.excludedRoutes = excludedRoutes.filter(\.isIPv4).map(ipv4Route)
    }
    if !payload.ipv6.isEmpty {
        settings.ipv6Settings = NEIPv6Settings(
            addresses: payload.ipv6.map(\.address),
            networkPrefixLengths: payload.ipv6.map { NSNumber(value: $0.prefixLen) }
        )
        settings.ipv6Settings?.includedRoutes = includedRoutes.filter(\.isIPv6).map(ipv6Route)
        settings.ipv6Settings?.excludedRoutes = excludedRoutes.filter(\.isIPv6).map(ipv6Route)
    }
    if !payload.dnsServers.isEmpty {
        let dns = NEDNSSettings(servers: payload.dnsServers)
        dns.matchDomains = [""]
        dns.matchDomainsNoSearch = true
        settings.dnsSettings = dns
    }
    if payload.httpProxy != nil || payload.httpsProxy != nil {
        let proxy = NEProxySettings()
        if let http = payload.httpProxy {
            proxy.httpEnabled = true
            proxy.httpServer = NEProxyServer(address: http.host, port: http.port)
        }
        if let https = payload.httpsProxy {
            proxy.httpsEnabled = true
            proxy.httpsServer = NEProxyServer(address: https.host, port: https.port)
        }
        settings.proxySettings = proxy
    }
    return settings
}

private func ipv4Route(_ route: TunnelRoutePrefix) -> NEIPv4Route {
    if route.isIPv4DefaultRoute {
        return NEIPv4Route.default()
    }
    return NEIPv4Route(destinationAddress: route.address, subnetMask: ipv4Mask(prefixLength: route.prefixLen))
}

private func ipv6Route(_ route: TunnelRoutePrefix) -> NEIPv6Route {
    if route.isIPv6DefaultRoute {
        return NEIPv6Route.default()
    }
    return NEIPv6Route(destinationAddress: route.address, networkPrefixLength: NSNumber(value: route.prefixLen))
}

private func ipv4Mask(prefixLength: Int) -> String {
    let clamped = min(max(prefixLength, 0), 32)
    let mask = clamped == 0 ? UInt32(0) : UInt32.max << UInt32(32 - clamped)
    return [
        UInt8((mask >> 24) & 0xff),
        UInt8((mask >> 16) & 0xff),
        UInt8((mask >> 8) & 0xff),
        UInt8(mask & 0xff),
    ].map(String.init).joined(separator: ".")
}

private func runtimeString(_ body: (NSErrorPointer) -> String) throws -> String {
    var error: NSError?
    let value = body(&error)
    if let error {
        throw error
    }
    return value
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
        throw PacketTunnelProviderError.mobileBridgeFailed
    }
}

private enum PacketTunnelProviderError: Error, LocalizedError {
    case runtimeUnavailable
    case missingPayload(String)
    case unsupportedIPVersion(Int)
    case packetWriteFailed
    case mobileBridgeFailed
    case networkSettingsTimedOut
    case unsupported(String)

    var errorDescription: String? {
        switch self {
        case .runtimeUnavailable:
            return "packet tunnel runtime is unavailable"
        case .missingPayload(let name):
            return "missing \(name) payload"
        case .unsupportedIPVersion(let version):
            return "unsupported IP packet version \(version)"
        case .packetWriteFailed:
            return "packet tunnel flow rejected packet write"
        case .mobileBridgeFailed:
            return "mobile tunnel bridge returned failure"
        case .networkSettingsTimedOut:
            return "timed out applying packet tunnel network settings"
        case .unsupported(let message):
            return message
        }
    }
}
