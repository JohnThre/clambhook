import ClambhookShared
import Foundation
import NetworkExtension

@MainActor
final class IOSTunnelController: ObservableObject {
    @Published private(set) var status: NEVPNStatus = .invalid
    @Published private(set) var isConfigured = false
    @Published var message = ""

    private var manager: NETunnelProviderManager?

    var isRunning: Bool {
        status == .connected || status == .connecting || status == .reasserting
    }

    func load() async {
        do {
            manager = try await loadManager()
            status = manager?.connection.status ?? .invalid
            isConfigured = manager != nil
            observeStatus()
        } catch {
            message = error.localizedDescription
        }
    }

    func saveConfiguration(_ document: StandaloneConfigDocument) async {
        do {
            let manager = self.manager ?? NETunnelProviderManager()
            let proto = NETunnelProviderProtocol()
            proto.providerBundleIdentifier = appleIOSPacketTunnelBundleIdentifier
            proto.serverAddress = "clambhook"
            proto.providerConfiguration = [
                "appGroup": defaultAppGroupIdentifier,
                "configKey": "clambhook.apple.standalone-config",
                "activeProfile": document.activeProfile,
            ]
            manager.protocolConfiguration = proto
            manager.localizedDescription = "clambhook"
            manager.isEnabled = true
            try await save(manager)
            try await reload(manager)
            self.manager = manager
            status = manager.connection.status
            isConfigured = true
            observeStatus()
            message = "VPN configuration saved"
        } catch {
            message = error.localizedDescription
        }
    }

    func start(document: StandaloneConfigDocument) async {
        if manager == nil {
            await saveConfiguration(document)
        }
        do {
            try manager?.connection.startVPNTunnel()
            status = manager?.connection.status ?? .invalid
        } catch {
            message = error.localizedDescription
        }
    }

    func stop() {
        manager?.connection.stopVPNTunnel()
        status = manager?.connection.status ?? .invalid
    }

    private func observeStatus() {
        NotificationCenter.default.removeObserver(self, name: .NEVPNStatusDidChange, object: nil)
        NotificationCenter.default.addObserver(
            forName: .NEVPNStatusDidChange,
            object: manager?.connection,
            queue: .main
        ) { [weak self] _ in
            Task { @MainActor in
                self?.status = self?.manager?.connection.status ?? .invalid
            }
        }
    }

    private func loadManager() async throws -> NETunnelProviderManager? {
        try await withCheckedThrowingContinuation { continuation in
            NETunnelProviderManager.loadAllFromPreferences { managers, error in
                if let error {
                    continuation.resume(throwing: error)
                    return
                }
                continuation.resume(returning: managers?.first(where: { manager in
                    (manager.protocolConfiguration as? NETunnelProviderProtocol)?
                        .providerBundleIdentifier == appleIOSPacketTunnelBundleIdentifier
                }))
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

    private func reload(_ manager: NETunnelProviderManager) async throws {
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
}
