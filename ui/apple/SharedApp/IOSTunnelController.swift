#if os(iOS)
import ClambhookShared
import Foundation
@preconcurrency import NetworkExtension

private let clambhookTunnelProviderBundleIdentifier = "org.jpfchang.clambhook.tunnel"

@MainActor
final class IOSTunnelController: ObservableObject {
    @Published private(set) var status: NEVPNStatus = .invalid
    @Published private(set) var errorText = ""

    private var manager: NETunnelProviderManager?
    private var statusObserver: NSObjectProtocol?
    private let decoder = JSONDecoder()
    private let encoder = JSONEncoder()

    deinit {
        if let statusObserver {
            NotificationCenter.default.removeObserver(statusObserver)
        }
    }

    func startTunnel() async throws {
        _ = try TunnelConfigStore.loadOrCreateConfig()
        let manager = try await configuredManager()
        guard let session = manager.connection as? NETunnelProviderSession else {
            throw TunnelControllerError.invalidSession
        }
        try session.startTunnel(options: [
            "configPath": TunnelConfigStore.configURL().path
        ])
        updateStatus(from: manager)
    }

    func stopTunnel() async {
        guard let manager = try? await loadManager(),
              let session = manager.connection as? NETunnelProviderSession else {
            status = .disconnected
            return
        }
        session.stopTunnel()
        updateStatus(from: manager)
    }

    func reloadConfiguration() async throws {
        guard status == .connected || status == .connecting || status == .reasserting else {
            return
        }
        _ = try await send(.init(action: .reload))
    }

    func setActiveProfile(_ name: String) async throws {
        _ = try await send(.init(action: .setActiveProfile, profile: name))
    }

    func dashboard() async throws -> TunnelDashboardPayload {
        let manager = try await configuredManager()
        updateStatus(from: manager)
        guard status == .connected || status == .connecting || status == .reasserting else {
            return TunnelDashboardPayload(status: StatusPayload(running: false))
        }
        let data = try await send(.init(action: .dashboard))
        return try decoder.decode(TunnelDashboardPayload.self, from: data)
    }

    private func configuredManager() async throws -> NETunnelProviderManager {
        let manager = try await loadManager()
        let proto = (manager.protocolConfiguration as? NETunnelProviderProtocol) ?? NETunnelProviderProtocol()
        proto.providerBundleIdentifier = clambhookTunnelProviderBundleIdentifier
        proto.serverAddress = "clambhook"
        proto.providerConfiguration = [
            "configPath": TunnelConfigStore.configURL().path
        ]
        manager.localizedDescription = "clambhook"
        manager.protocolConfiguration = proto
        manager.isEnabled = true
        try await save(manager)
        try await reload(manager)
        observeStatus(for: manager)
        updateStatus(from: manager)
        self.manager = manager
        return manager
    }

    private func loadManager() async throws -> NETunnelProviderManager {
        if let manager {
            return manager
        }
        let managers = try await loadAllManagers()
        if let existing = managers.first(where: { manager in
            (manager.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier == clambhookTunnelProviderBundleIdentifier
        }) {
            observeStatus(for: existing)
            updateStatus(from: existing)
            self.manager = existing
            return existing
        }
        let manager = NETunnelProviderManager()
        observeStatus(for: manager)
        self.manager = manager
        return manager
    }

    private func send(_ command: TunnelCommand) async throws -> Data {
        let manager = try await loadManager()
        guard let session = manager.connection as? NETunnelProviderSession else {
            throw TunnelControllerError.invalidSession
        }
        let message = try encoder.encode(command)
        return try await withCheckedThrowingContinuation { continuation in
            do {
                try session.sendProviderMessage(message) { data in
                    if let data {
                        continuation.resume(returning: data)
                    } else {
                        continuation.resume(throwing: TunnelControllerError.emptyProviderResponse)
                    }
                }
            } catch {
                continuation.resume(throwing: error)
            }
        }
    }

    private func observeStatus(for manager: NETunnelProviderManager) {
        if let statusObserver {
            NotificationCenter.default.removeObserver(statusObserver)
        }
        statusObserver = NotificationCenter.default.addObserver(
            forName: .NEVPNStatusDidChange,
            object: manager.connection,
            queue: .main
        ) { [weak self, weak manager] _ in
            guard let manager else { return }
            Task { @MainActor in
                self?.updateStatus(from: manager)
            }
        }
    }

    private func updateStatus(from manager: NETunnelProviderManager) {
        status = manager.connection.status
        if status != .invalid {
            errorText = ""
        }
    }

    private func loadAllManagers() async throws -> [NETunnelProviderManager] {
        try await withCheckedThrowingContinuation { continuation in
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

final class TunnelDashboardClient: ClambhookAPIProviding {
    private let controller: IOSTunnelController

    init(controller: IOSTunnelController) {
        self.controller = controller
    }

    func status() async throws -> StatusPayload {
        try await controller.dashboard().status
    }

    func profiles() async throws -> ProfilesPayload {
        try await controller.dashboard().profiles
    }

    func servers() async throws -> ServersPayload {
        try await controller.dashboard().servers
    }

    func rules() async throws -> RulesPayload {
        try await controller.dashboard().rules
    }

    func traffic() async throws -> TrafficSnapshotPayload {
        try await controller.dashboard().traffic
    }

    func connect() async throws {
        try await controller.startTunnel()
    }

    func disconnect() async throws {
        await controller.stopTunnel()
    }

    func setActiveProfile(_ name: String) async throws {
        try await controller.setActiveProfile(name)
    }

    func reloadConfiguration() async throws {
        try await controller.reloadConfiguration()
    }
}

enum TunnelControllerError: Error, LocalizedError {
    case invalidSession
    case emptyProviderResponse

    var errorDescription: String? {
        switch self {
        case .invalidSession:
            return "packet tunnel session is unavailable"
        case .emptyProviderResponse:
            return "packet tunnel returned no response"
        }
    }
}
#endif
