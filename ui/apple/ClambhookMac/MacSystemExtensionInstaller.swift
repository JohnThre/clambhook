import AppKit
import ClambhookShared
import Foundation
import SystemExtensions

enum MacSystemExtensionInstallStatus: Equatable {
    case notActivated
    case activating
    case activated
    case requiresApproval
    case rebootRequired
    case failed(String)

    var label: String {
        switch self {
        case .notActivated:
            return "System extension not activated"
        case .activating:
            return "System extension activating"
        case .activated:
            return "System extension activated"
        case .requiresApproval:
            return "System extension requires approval"
        case .rebootRequired:
            return "Restart required"
        case .failed:
            return "System extension failed"
        }
    }
}

@MainActor
final class MacSystemExtensionInstaller: ObservableObject {
    static let shared = MacSystemExtensionInstaller()

    @Published private(set) var status: MacSystemExtensionInstallStatus = .notActivated
    @Published private(set) var isWorking = false
    @Published private(set) var statusMessage = ""

    private var activationDelegate: MacSystemExtensionActivationDelegate?

    func activate() async {
        isWorking = true
        status = .activating
        statusMessage = ""
        defer { isWorking = false }
        do {
            try await prepareForTunnelStart()
            status = .activated
            statusMessage = "system extension activated"
        } catch MacSystemExtensionInstallerError.userApprovalRequired {
            status = .requiresApproval
            statusMessage = MacSystemExtensionInstallerError.userApprovalRequired.localizedDescription
        } catch MacSystemExtensionInstallerError.rebootRequired {
            status = .rebootRequired
            statusMessage = MacSystemExtensionInstallerError.rebootRequired.localizedDescription
        } catch {
            status = .failed(error.localizedDescription)
            statusMessage = error.localizedDescription
        }
    }

    func prepareForTunnelStart() async throws {
        do {
            try await submitActivationRequest()
            status = .activated
            statusMessage = "system extension activated"
        } catch MacSystemExtensionInstallerError.userApprovalRequired {
            status = .requiresApproval
            statusMessage = MacSystemExtensionInstallerError.userApprovalRequired.localizedDescription
            throw MacSystemExtensionInstallerError.userApprovalRequired
        } catch MacSystemExtensionInstallerError.rebootRequired {
            status = .rebootRequired
            statusMessage = MacSystemExtensionInstallerError.rebootRequired.localizedDescription
            throw MacSystemExtensionInstallerError.rebootRequired
        } catch {
            status = .failed(error.localizedDescription)
            statusMessage = error.localizedDescription
            throw error
        }
    }

    private func submitActivationRequest() async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            let delegate = MacSystemExtensionActivationDelegate { [weak self] result in
                Task { @MainActor in
                    self?.activationDelegate = nil
                    continuation.resume(with: result)
                }
            }
            activationDelegate = delegate
            let request = OSSystemExtensionRequest.activationRequest(
                forExtensionWithIdentifier: clambhookMacTunnelBundleIdentifier,
                queue: .main
            )
            request.delegate = delegate
            OSSystemExtensionManager.shared.submitRequest(request)
        }
    }

    func openSystemSettings() {
        let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?General")!
        NSWorkspace.shared.open(url)
    }
}

private final class MacSystemExtensionActivationDelegate: NSObject, OSSystemExtensionRequestDelegate {
    private let completion: (Result<Void, Error>) -> Void
    private var completed = false

    init(completion: @escaping (Result<Void, Error>) -> Void) {
        self.completion = completion
    }

    func request(
        _ request: OSSystemExtensionRequest,
        actionForReplacingExtension existing: OSSystemExtensionProperties,
        withExtension ext: OSSystemExtensionProperties
    ) -> OSSystemExtensionRequest.ReplacementAction {
        .replace
    }

    func requestNeedsUserApproval(_ request: OSSystemExtensionRequest) {
        complete(.failure(MacSystemExtensionInstallerError.userApprovalRequired))
    }

    func request(_ request: OSSystemExtensionRequest, didFinishWithResult result: OSSystemExtensionRequest.Result) {
        switch result {
        case .completed:
            complete(.success(()))
        case .willCompleteAfterReboot:
            complete(.failure(MacSystemExtensionInstallerError.rebootRequired))
        @unknown default:
            complete(.success(()))
        }
    }

    func request(_ request: OSSystemExtensionRequest, didFailWithError error: Error) {
        complete(.failure(error))
    }

    private func complete(_ result: Result<Void, Error>) {
        guard !completed else { return }
        completed = true
        completion(result)
    }
}

enum MacSystemExtensionInstallerError: Error, LocalizedError, Equatable {
    case userApprovalRequired
    case rebootRequired

    var errorDescription: String? {
        switch self {
        case .userApprovalRequired:
            return "Approve the ClambHook system extension in System Settings, then connect again."
        case .rebootRequired:
            return "Restart macOS to finish activating the ClambHook system extension."
        }
    }
}
