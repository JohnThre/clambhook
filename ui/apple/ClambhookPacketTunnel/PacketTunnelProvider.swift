import ClambhookShared
import Darwin
import Foundation
import NetworkExtension
import os.log

#if canImport(Mobile)
import Mobile
#endif

final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let logger = Logger(subsystem: "org.jpfchang.clambhook", category: "PacketTunnel")
    private let decoder = JSONDecoder()
    private let encoder = JSONEncoder()
    private var readTask: Task<Void, Never>?

    #if canImport(Mobile)
    private var runtime: MobileTunnelRuntime?
    #endif

    override func startTunnel(options: [String: NSObject]?) async throws {
        logger.info("Packet tunnel start requested")
        let configPath = tunnelConfigPath(options: options)
        _ = try TunnelConfigStore.loadOrCreateConfig()

        #if canImport(Mobile)
        let settingsJSON = try MobileTunnelNetworkSettingsJSON(configPath)
        let settingsPayload = try decoder.decode(
            TunnelNetworkSettingsPayload.self,
            from: Data(settingsJSON.utf8)
        )
        try await applyTunnelSettings(settingsPayload)

        let runtime = MobileNewTunnelRuntime(self)
        try runtime.start(configPath)
        self.runtime = runtime
        startPacketReadLoop(runtime: runtime)
        #else
        throw NSError(
            domain: "org.jpfchang.clambhook.tunnel",
            code: 1,
            userInfo: [
                NSLocalizedDescriptionKey: "Embedded clambhook runtime is missing. Run make build-ios-mobile-xcframework before building the iOS app for device."
            ]
        )
        #endif
    }

    override func stopTunnel(with reason: NEProviderStopReason) async {
        logger.info("Packet tunnel stopped: \(reason.rawValue, privacy: .public)")
        readTask?.cancel()
        readTask = nil
        #if canImport(Mobile)
        if let runtime {
            try? runtime.stop()
        }
        runtime = nil
        #endif
    }

    override func handleAppMessage(_ messageData: Data) async -> Data? {
        guard let command = try? decoder.decode(TunnelCommand.self, from: messageData) else {
            return nil
        }
        #if canImport(Mobile)
        guard let runtime else {
            return encoded(TunnelDashboardPayload(status: StatusPayload(running: false)))
        }
        do {
            let json: String
            switch command.action {
            case .dashboard:
                json = try runtime.dashboardJSON()
            case .status:
                json = try runtime.statusJSON()
            case .profiles:
                json = try runtime.profilesJSON()
            case .servers:
                json = try runtime.serversJSON()
            case .rules:
                json = try runtime.rulesJSON()
            case .traffic:
                json = try runtime.trafficJSON()
            case .reload:
                try runtime.reload(tunnelConfigPath(options: nil))
                json = try runtime.dashboardJSON()
            case .setActiveProfile:
                if let profile = command.profile {
                    try runtime.setActiveProfile(profile)
                }
                json = try runtime.dashboardJSON()
            }
            return Data(json.utf8)
        } catch {
            logger.error("Provider command failed: \(error.localizedDescription, privacy: .public)")
            return nil
        }
        #else
        return encoded(TunnelDashboardPayload(status: StatusPayload(running: false)))
        #endif
    }

    private func tunnelConfigPath(options: [String: NSObject]?) -> String {
        if let path = options?["configPath"] as? String, !path.isEmpty {
            return path
        }
        if let proto = protocolConfiguration as? NETunnelProviderProtocol,
           let path = proto.providerConfiguration?["configPath"] as? String,
           !path.isEmpty {
            return path
        }
        return TunnelConfigStore.configURL().path
    }

    private func encoded<T: Encodable>(_ value: T) -> Data? {
        try? encoder.encode(value)
    }

    #if canImport(Mobile)
    private func startPacketReadLoop(runtime: MobileTunnelRuntime) {
        readTask?.cancel()
        readTask = Task { [weak self] in
            guard let self else { return }
            while !Task.isCancelled {
                let (packets, _) = await packetFlow.readPackets()
                if Task.isCancelled {
                    break
                }
                for packet in packets {
                    do {
                        try runtime.injectPacket(packet)
                    } catch {
                        logger.error("Inject packet failed: \(error.localizedDescription, privacy: .public)")
                    }
                }
            }
        }
    }
    #endif

    private func applyTunnelSettings(_ payload: TunnelNetworkSettingsPayload) async throws {
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: payload.remoteAddress)
        settings.mtu = NSNumber(value: payload.mtu)

        if !payload.ipv4.isEmpty {
            let ipv4 = NEIPv4Settings(
                addresses: payload.ipv4.map(\.address),
                subnetMasks: payload.ipv4.map { ipv4Mask(prefixLen: $0.prefixLen) }
            )
            ipv4.includedRoutes = payload.includedRoutes.compactMap(ipv4Route)
            ipv4.excludedRoutes = payload.excludedRoutes.compactMap(ipv4Route)
            settings.ipv4Settings = ipv4
        }

        if !payload.ipv6.isEmpty {
            let ipv6 = NEIPv6Settings(
                addresses: payload.ipv6.map(\.address),
                networkPrefixLengths: payload.ipv6.map { NSNumber(value: $0.prefixLen) }
            )
            ipv6.includedRoutes = payload.includedRoutes.compactMap(ipv6Route)
            ipv6.excludedRoutes = payload.excludedRoutes.compactMap(ipv6Route)
            settings.ipv6Settings = ipv6
        }

        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            setTunnelNetworkSettings(settings) { error in
                if let error {
                    continuation.resume(throwing: error)
                } else {
                    continuation.resume()
                }
            }
        }
    }

    private func ipv4Route(_ raw: String) -> NEIPv4Route? {
        let parts = raw.split(separator: "/", maxSplits: 1).map(String.init)
        guard parts.count == 2, let prefixLen = Int(parts[1]), raw.contains(".") else {
            return nil
        }
        return NEIPv4Route(destinationAddress: parts[0], subnetMask: ipv4Mask(prefixLen: prefixLen))
    }

    private func ipv6Route(_ raw: String) -> NEIPv6Route? {
        let parts = raw.split(separator: "/", maxSplits: 1).map(String.init)
        guard parts.count == 2, let prefixLen = Int(parts[1]), raw.contains(":") else {
            return nil
        }
        return NEIPv6Route(destinationAddress: parts[0], networkPrefixLength: NSNumber(value: prefixLen))
    }

    private func ipv4Mask(prefixLen: Int) -> String {
        let clamped = min(max(prefixLen, 0), 32)
        let mask = clamped == 0 ? UInt32(0) : UInt32.max << UInt32(32 - clamped)
        return [
            (mask >> 24) & 0xff,
            (mask >> 16) & 0xff,
            (mask >> 8) & 0xff,
            mask & 0xff,
        ]
        .map(String.init)
        .joined(separator: ".")
    }
}

#if canImport(Mobile)
extension PacketTunnelProvider: MobilePacketWriterProtocol {
    func writePacket(_ packet: Data?) throws {
        guard let packet else { return }
        let protocolFamily: NSNumber
        switch packet.first.map({ $0 >> 4 }) {
        case 4:
            protocolFamily = NSNumber(value: AF_INET)
        case 6:
            protocolFamily = NSNumber(value: AF_INET6)
        default:
            return
        }
        packetFlow.writePackets([packet], withProtocols: [protocolFamily])
    }
}
#endif
