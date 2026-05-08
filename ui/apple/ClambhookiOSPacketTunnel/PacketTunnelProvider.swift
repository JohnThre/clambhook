import ClambhookTunnelCore
import ClambhookShared
import Darwin
import Foundation
import NetworkExtension

final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let packetQueue = DispatchQueue(label: "org.jpfchang.clambhook.packet-tunnel.packets", qos: .userInitiated)
    private var core: TunnelCore?
    private var running = false

    override func startTunnel(options: [String: NSObject]?, completionHandler: @escaping (Error?) -> Void) {
        let config = Self.loadConfigDocument()
        guard !config.toml.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            completionHandler(Self.providerError(code: 1, message: "No Clambhook TOML configuration is available."))
            return
        }

        do {
            let core = try TunnelCore(config: config.toml)
            try core.start()
            setTunnelNetworkSettings(Self.networkSettings()) { [weak self] error in
                guard let self else { return }
                if let error {
                    core.stop()
                    completionHandler(error)
                    return
                }
                self.core = core
                self.running = true
                self.readPacketsFromSystem()
                self.writePacketsToSystem()
                completionHandler(nil)
            }
        } catch {
            completionHandler(error)
        }
    }

    override func stopTunnel(with reason: NEProviderStopReason, completionHandler: @escaping () -> Void) {
        running = false
        core?.stop()
        core = nil
        completionHandler()
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)?) {
        let config = Self.loadConfigDocument()
        let response = TunnelProviderMessage(
            running: running,
            activeProfile: config.activeProfile,
            hasConfig: !config.toml.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
            dataPlaneReady: true
        )
        completionHandler?(try? JSONEncoder().encode(response))
    }

    private func readPacketsFromSystem() {
        packetFlow.readPackets { [weak self] packets, _ in
            guard let self, self.running, let core = self.core else { return }
            for packet in packets {
                do {
                    try core.inject(packet: packet)
                } catch {
                    self.cancelTunnelWithError(error)
                    return
                }
            }
            self.readPacketsFromSystem()
        }
    }

    private func writePacketsToSystem() {
        packetQueue.async { [weak self] in
            while let self, self.running, let core = self.core {
                do {
                    guard let packet = try core.readPacket(timeoutMillis: 100) else {
                        continue
                    }
                    self.packetFlow.writePackets([packet], withProtocols: [Self.protocolNumber(for: packet)])
                } catch {
                    self.cancelTunnelWithError(error)
                    return
                }
            }
        }
    }

    private static func networkSettings() -> NEPacketTunnelNetworkSettings {
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "10.255.0.1")
        settings.mtu = 1500

        let ipv4 = NEIPv4Settings(addresses: ["10.255.0.2"], subnetMasks: ["255.255.255.255"])
        ipv4.includedRoutes = [NEIPv4Route.default()]
        settings.ipv4Settings = ipv4

        let ipv6 = NEIPv6Settings(addresses: ["fd00:636c:616d::2"], networkPrefixLengths: [128])
        ipv6.includedRoutes = [NEIPv6Route.default()]
        settings.ipv6Settings = ipv6

        let dns = NEDNSSettings(servers: ["1.1.1.1", "8.8.8.8"])
        dns.matchDomains = [""]
        settings.dnsSettings = dns
        return settings
    }

    private static func protocolNumber(for packet: Data) -> NSNumber {
        if let first = packet.first, first >> 4 == 6 {
            return NSNumber(value: AF_INET6)
        }
        return NSNumber(value: AF_INET)
    }

    private static func loadConfigDocument() -> StandaloneConfigDocument {
        let defaults = UserDefaults(suiteName: defaultAppGroupIdentifier) ?? .standard
        guard
            let data = defaults.data(forKey: "clambhook.apple.standalone-config"),
            let decoded = try? JSONDecoder().decode(StandaloneConfigDocument.self, from: data)
        else {
            return StandaloneConfigDocument()
        }
        return decoded
    }

    fileprivate static func providerError(code: Int, message: String) -> NSError {
        NSError(domain: "org.jpfchang.clambhook.packet-tunnel", code: code, userInfo: [
            NSLocalizedDescriptionKey: message
        ])
    }
}

private struct TunnelProviderMessage: Codable {
    var running: Bool
    var activeProfile: String
    var hasConfig: Bool
    var dataPlaneReady: Bool
}

private final class TunnelCore {
    private let handle: Int64

    init(config: String) throws {
        var errorPointer: UnsafeMutablePointer<CChar>?
        let createdHandle = config.withCString { configPointer in
            ClambhookTunnelCreate(UnsafeMutablePointer(mutating: configPointer), &errorPointer)
        }
        guard createdHandle != 0 else {
            throw Self.bridgeError(errorPointer)
        }
        handle = Int64(createdHandle)
    }

    deinit {
        ClambhookTunnelRelease(handle)
    }

    func start() throws {
        var errorPointer: UnsafeMutablePointer<CChar>?
        guard ClambhookTunnelStart(handle, &errorPointer) == 1 else {
            throw Self.bridgeError(errorPointer)
        }
    }

    func stop() {
        ClambhookTunnelStop(handle)
    }

    func inject(packet: Data) throws {
        var errorPointer: UnsafeMutablePointer<CChar>?
        let result = packet.withUnsafeBytes { buffer in
            guard let baseAddress = buffer.baseAddress else { return CInt(0) }
            return ClambhookTunnelInjectPacket(
                handle,
                UnsafeMutableRawPointer(mutating: baseAddress),
                CInt(buffer.count),
                &errorPointer
            )
        }
        guard result == 1 else {
            throw Self.bridgeError(errorPointer)
        }
    }

    func readPacket(timeoutMillis: Int32) throws -> Data? {
        var packetPointer: UnsafeMutableRawPointer?
        var packetLength = CInt(0)
        var errorPointer: UnsafeMutablePointer<CChar>?
        let result = ClambhookTunnelReadPacket(handle, CInt(timeoutMillis), &packetPointer, &packetLength, &errorPointer)
        if result < 0 {
            throw Self.bridgeError(errorPointer)
        }
        guard result == 1, let packetPointer, packetLength > 0 else {
            return nil
        }
        defer { ClambhookTunnelFree(packetPointer) }
        return Data(bytes: packetPointer, count: Int(packetLength))
    }

    private static func bridgeError(_ errorPointer: UnsafeMutablePointer<CChar>?) -> Error {
        let message = errorPointer.map { String(cString: $0) } ?? "Unknown Clambhook tunnel core error"
        if let errorPointer {
            ClambhookTunnelFree(UnsafeMutableRawPointer(errorPointer))
        }
        return PacketTunnelProvider.providerError(code: 10, message: message)
    }
}
