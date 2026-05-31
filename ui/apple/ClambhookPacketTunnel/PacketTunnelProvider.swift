import NetworkExtension
import os.log

final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let logger = Logger(subsystem: "org.jpfchang.clambhook", category: "PacketTunnel")

    override func startTunnel(options: [String: NSObject]?) async throws {
        logger.info("Packet tunnel start requested")
        throw NSError(
            domain: "org.jpfchang.clambhook.tunnel",
            code: 1,
            userInfo: [
                NSLocalizedDescriptionKey: "ClambHook packet forwarding is not available in this build."
            ]
        )
    }

    override func stopTunnel(with reason: NEProviderStopReason) async {
        logger.info("Packet tunnel stopped: \(reason.rawValue, privacy: .public)")
    }

    override func handleAppMessage(_ messageData: Data) async -> Data? {
        nil
    }
}
