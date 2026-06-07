#if os(iOS)
import ClambhookShared
import Foundation
@preconcurrency import NetworkExtension
#if canImport(ClambhookMobile)
import ClambhookMobile
#endif

private let clambhookTunnelProviderBundleIdentifier = "org.jpfchang.clambhook.tunnel"

#if canImport(ClambhookMobile)
private func mobileString(_ operation: (NSErrorPointer) -> String) throws -> String {
    var error: NSError?
    let value = operation(&error)
    if let error {
        throw error
    }
    return value
}

private func mobileBool(_ operation: (NSErrorPointer) -> Bool) throws {
    var error: NSError?
    if !operation(&error) {
        throw error ?? TunnelControllerError.mobileValidationFailed
    }
}
#endif

@MainActor
final class IOSTunnelController: ObservableObject {
    @Published private(set) var status: NEVPNStatus = .invalid
    @Published private(set) var errorText = ""

    private var manager: NETunnelProviderManager?
    private var statusObserver: NSObjectProtocol?
    private let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return decoder
    }()
    private let encoder = JSONEncoder()

    deinit {
        if let statusObserver {
            NotificationCenter.default.removeObserver(statusObserver)
        }
    }

    func startTunnel() async throws {
        try MobileLicenseRuntimeGuard.requireFeatureAccess(.tunnelRouting)
        _ = try TunnelConfigStore.loadOrCreateConfig()
        #if canImport(ClambhookMobile)
        try mobileBool {
            MobileValidateUsableTunnelConfig(TunnelConfigStore.configURL().path, $0)
        }
        #endif
        do {
            let manager = try await configuredManager()
            guard let session = manager.connection as? NETunnelProviderSession else {
                throw TunnelControllerError.invalidSession
            }
            try session.startTunnel(options: [
                "configPath": TunnelConfigStore.configURL().path
            ])
            updateStatus(from: manager)
        } catch {
            let issue = Self.recoveryIssue(for: error)
            errorText = issue.message
            throw TunnelRecoveryError(issue)
        }
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
        guard canSendProviderMessage else {
            return
        }
        _ = try await send(.init(action: .reload))
    }

    func resetVPNProfile() async throws {
        let managers = try await loadAllManagers()
        for manager in managers {
            let bundleID = (manager.protocolConfiguration as? NETunnelProviderProtocol)?.providerBundleIdentifier
            guard bundleID == clambhookTunnelProviderBundleIdentifier else {
                continue
            }
            manager.connection.stopVPNTunnel()
            try await remove(manager)
        }
        manager = nil
        let next = try await configuredManager()
        updateStatus(from: next)
    }

    func setActiveProfile(_ name: String) async throws {
        _ = try await send(.init(action: .setActiveProfile, profile: name))
    }

    func dashboard() async throws -> TunnelDashboardPayload {
        let manager = try await configuredManager()
        updateStatus(from: manager)
        if canSendProviderMessage {
            let data = try await send(.init(action: .dashboard))
            return try decoder.decode(TunnelDashboardPayload.self, from: data)
        }
        return try disconnectedDashboard()
    }

    func developerStatus() async throws -> DeveloperStatusPayload {
        guard canSendProviderMessage else {
            return DeveloperStatusPayload()
        }
        let data = try await send(.init(action: .developerStatus))
        return try decoder.decode(DeveloperStatusPayload.self, from: data)
    }

    func developerEntries() async throws -> DeveloperEntriesPayload {
        guard canSendProviderMessage else {
            return DeveloperEntriesPayload()
        }
        let data = try await send(.init(action: .developerEntries))
        return try decoder.decode(DeveloperEntriesPayload.self, from: data)
    }

    func developerCAPEM() async throws -> String {
        guard canSendProviderMessage else {
            throw TunnelControllerError.tunnelNotRunning
        }
        let data = try await send(.init(action: .developerCA))
        return try decoder.decode(DeveloperCAPayload.self, from: data).pem
    }

    func developerHAR() async throws -> String {
        guard canSendProviderMessage else {
            return #"{"log":{"version":"1.2","entries":[]}}"#
        }
        let data = try await send(.init(action: .developerHAR))
        return String(data: data, encoding: .utf8) ?? #"{"log":{"version":"1.2","entries":[]}}"#
    }

    func clearDeveloperEntries() async throws {
        guard canSendProviderMessage else {
            return
        }
        _ = try await send(.init(action: .clearDeveloperEntries))
    }

    private var canSendProviderMessage: Bool {
        status == .connected || status == .connecting || status == .reasserting
    }

    private func disconnectedDashboard() throws -> TunnelDashboardPayload {
        #if canImport(ClambhookMobile)
        _ = try TunnelConfigStore.loadOrCreateConfig()
        let json = try mobileString {
            MobileTunnelConfigDashboardJSON(TunnelConfigStore.configURL().path, $0)
        }
        return try decoder.decode(TunnelDashboardPayload.self, from: Data(json.utf8))
        #else
        return TunnelDashboardPayload(status: StatusPayload(running: false))
        #endif
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
                        if let envelope = try? self.decoder.decode(TunnelProviderErrorEnvelope.self, from: data) {
                            continuation.resume(throwing: TunnelRecoveryError(envelope.error))
                        } else {
                            continuation.resume(returning: data)
                        }
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

    private func remove(_ manager: NETunnelProviderManager) async throws {
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

    private static func recoveryIssue(for error: Error) -> TunnelRecoveryIssue {
        if let recoveryError = error as? TunnelRecoveryError {
            return recoveryError.issue
        }
        let nsError = error as NSError
        if nsError.domain == NEVPNErrorDomain, let code = NEVPNError.Code(rawValue: nsError.code) {
            switch code {
            case .configurationInvalid, .configurationStale, .configurationReadWriteFailed, .configurationDisabled:
                return TunnelRecoveryIssue(
                    kind: .invalidEntitlementOrProfile,
                    title: "VPN profile is not usable",
                    message: "The saved VPN configuration is invalid or disabled. Rebuild the local VPN profile and refresh.",
                    actions: [.rebuildVPNProfile, .refresh],
                    rawError: nsError.localizedDescription
                )
            case .connectionFailed:
                return TunnelRecoveryClassifier.issue(forRawError: nsError.localizedDescription)
            default:
                break
            }
        }
        return TunnelRecoveryClassifier.issue(for: error)
    }
}

final class TunnelDashboardClient: ClambhookDashboardProviding, DeveloperCaptureProviding {
    private let controller: IOSTunnelController

    init(controller: IOSTunnelController) {
        self.controller = controller
    }

    func dashboard() async throws -> TunnelDashboardPayload {
        try await controller.dashboard()
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

    func policyGroups() async throws -> PolicyGroupsPayload {
        try await controller.dashboard().policyGroups
    }

    func rules() async throws -> RulesPayload {
        try await controller.dashboard().rules
    }

    func testRule(network: String, target: String, profile: String = "") async throws -> RuleTestResponse {
        let dashboard = try await controller.dashboard()
        return try RuleTester.test(
            network: network,
            target: target,
            profile: profile.isEmpty ? dashboard.profiles.active : profile,
            rules: dashboard.rules.rules,
            effectiveRules: dashboard.rules.effectiveRules,
            chains: dashboard.servers.chains
        )
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

    func developerStatus() async throws -> DeveloperStatusPayload {
        try await controller.developerStatus()
    }

    func developerEntries() async throws -> DeveloperEntriesPayload {
        try await controller.developerEntries()
    }

    func developerCAPEM() async throws -> String {
        try await controller.developerCAPEM()
    }

    func developerHAR() async throws -> String {
        try await controller.developerHAR()
    }

    func clearDeveloperEntries() async throws {
        try await controller.clearDeveloperEntries()
    }

    func reloadConfiguration() async throws {
        try await controller.reloadConfiguration()
    }
}

enum TunnelControllerError: Error, LocalizedError {
    case invalidSession
    case emptyProviderResponse
    case mobileValidationFailed
    case tunnelNotRunning

    var errorDescription: String? {
        switch self {
        case .invalidSession:
            return "packet tunnel session is unavailable"
        case .emptyProviderResponse:
            return "packet tunnel returned no response"
        case .mobileValidationFailed:
            return "tunnel validation failed"
        case .tunnelNotRunning:
            return "start the tunnel before exporting the capture CA"
        }
    }
}
#endif
