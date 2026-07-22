import AppKit
import ClambhookShared
import Foundation
import ServiceManagement

enum MacPrivilegedHelperServiceStatus: Equatable {
    case notRegistered
    case enabled
    case requiresApproval
    case notFound
    case unknown(String)

    var label: String {
        switch self {
        case .notRegistered:
            return "Helper not installed"
        case .enabled:
            return "Helper enabled"
        case .requiresApproval:
            return "Helper requires approval"
        case .notFound:
            return "Helper not found"
        case .unknown(let value):
            return "Helper \(value)"
        }
    }
}

@MainActor
final class MacPrivilegedHelperManager: ObservableObject {
    @Published private(set) var serviceStatus: MacPrivilegedHelperServiceStatus = .notRegistered
    @Published private(set) var daemonRunning = false
    @Published private(set) var daemonPID: Int32?
    @Published private(set) var executablePath = ""
    @Published private(set) var isWorking = false
    @Published private(set) var statusMessage = ""

    private var service: SMAppService {
        .daemon(plistName: macPrivilegedHelperPlistName)
    }

    init() {
        refreshStatus()
    }

    func refreshStatus() {
        serviceStatus = map(status: service.status)
    }

    func registerHelper() async {
        await performServiceAction("helper registered") {
            try registerIfNeeded()
        }
    }

    func unregisterHelper() async {
        if daemonRunning {
            await stopDaemon()
        }
        await performServiceAction("helper unregistered") {
            do {
                try service.unregister()
            } catch let error as NSError where error.code == 4 {
                // kSMErrorJobNotFound: treat an already-removed helper as removed.
            }
            daemonRunning = false
            daemonPID = nil
        }
    }

    func openSystemSettings() {
        SMAppService.openSystemSettingsLoginItems()
    }

    func startDaemon(settings: AppSettings, token: String) async throws {
        isWorking = true
        statusMessage = "helper starting daemon"
        defer {
            isWorking = false
            refreshStatus()
        }
        try registerIfNeeded()
        guard service.status == .enabled else {
            statusMessage = "approve the privileged helper in System Settings"
            throw MacPrivilegedHelperError.approvalRequired
        }
        let reply = try await sendHelperMessage { proxy, completion in
            proxy.startDaemon(
                configPath: settings.daemonConfigPath,
                apiAddress: settings.apiEndpoint.hostPort,
                apiToken: token,
                licensePath: MobileLicenseSnapshotStore.daemonSnapshotPath(groupIdentifier: settings.appGroupIdentifier) ?? "",
                withReply: completion
            )
        }
        try apply(reply: reply)
    }

    func stopDaemon() async {
        isWorking = true
        statusMessage = "helper stopping daemon"
        defer {
            isWorking = false
            refreshStatus()
        }
        do {
            let reply = try await sendHelperMessage { proxy, completion in
                proxy.stopDaemon(withReply: completion)
            }
            try apply(reply: reply)
        } catch {
            statusMessage = error.localizedDescription
        }
    }

    func refreshDaemonStatus() async {
        do {
            let reply = try await sendHelperMessage { proxy, completion in
                proxy.status(withReply: completion)
            }
            try apply(reply: reply)
        } catch {
            daemonRunning = false
            daemonPID = nil
            statusMessage = error.localizedDescription
        }
    }

    private func performServiceAction(_ successMessage: String, operation: () throws -> Void) async {
        isWorking = true
        statusMessage = ""
        defer {
            isWorking = false
            refreshStatus()
        }
        do {
            try operation()
            statusMessage = successMessage
        } catch let error as NSError where error.code == 10 {
            statusMessage = "approve the privileged helper in System Settings"
            serviceStatus = .requiresApproval
        } catch {
            statusMessage = error.localizedDescription
        }
    }

    private func registerIfNeeded() throws {
        refreshStatus()
        guard service.status != .enabled else {
            return
        }
        do {
            try service.register()
        } catch let error as NSError where error.code == 11 {
            // kSMErrorAlreadyRegistered: refresh status and continue.
        }
        refreshStatus()
    }

    private func sendHelperMessage(
        _ operation: @escaping (ClambhookPrivilegedHelperProtocol, @escaping (NSDictionary) -> Void) -> Void
    ) async throws -> [String: Any] {
        let connection = NSXPCConnection(machServiceName: macPrivilegedHelperMachServiceName, options: .privileged)
        connection.remoteObjectInterface = NSXPCInterface(with: ClambhookPrivilegedHelperProtocol.self)
        connection.resume()

        return try await withCheckedThrowingContinuation { continuation in
            var didResume = false
            func finish(_ result: Result<NSDictionary, Error>) {
                guard !didResume else { return }
                didResume = true
                connection.invalidate()
                switch result {
                case .success(let reply):
                    continuation.resume(returning: reply as? [String: Any] ?? [:])
                case .failure(let error):
                    continuation.resume(throwing: error)
                }
            }

            guard let proxy = connection.remoteObjectProxyWithErrorHandler({ error in
                finish(.failure(error))
            }) as? ClambhookPrivilegedHelperProtocol else {
                finish(.failure(MacPrivilegedHelperError.invalidProxy))
                return
            }
            operation(proxy) { reply in
                finish(.success(reply))
            }
        }
    }

    private func apply(reply: [String: Any]) throws {
        let ok = reply[MacPrivilegedHelperReplyKey.ok] as? Bool ?? false
        let message = reply[MacPrivilegedHelperReplyKey.message] as? String ?? ""
        guard ok else {
            statusMessage = message
            throw MacPrivilegedHelperError.helper(message)
        }
        daemonRunning = reply[MacPrivilegedHelperReplyKey.running] as? Bool ?? false
        if let pid = reply[MacPrivilegedHelperReplyKey.pid] as? Int32 {
            daemonPID = pid
        } else if let pid = reply[MacPrivilegedHelperReplyKey.pid] as? NSNumber {
            daemonPID = pid.int32Value
        } else {
            daemonPID = nil
        }
        executablePath = reply[MacPrivilegedHelperReplyKey.executablePath] as? String ?? ""
        statusMessage = message
    }

    private func map(status: SMAppService.Status) -> MacPrivilegedHelperServiceStatus {
        switch status {
        case .notRegistered:
            return .notRegistered
        case .enabled:
            return .enabled
        case .requiresApproval:
            return .requiresApproval
        case .notFound:
            return .notFound
        @unknown default:
            return .unknown("\(status)")
        }
    }
}

private enum MacPrivilegedHelperError: LocalizedError {
    case invalidProxy
    case approvalRequired
    case helper(String)

    var errorDescription: String? {
        switch self {
        case .invalidProxy:
            return "Privileged helper XPC proxy is unavailable."
        case .approvalRequired:
            return "Approve the ClambHook privileged helper in System Settings."
        case .helper(let message):
            return message.isEmpty ? "Privileged helper request failed." : message
        }
    }
}

private extension URL {
    var hostPort: String {
        var host = host ?? "127.0.0.1"
        if host.contains(":") && !host.hasPrefix("[") {
            host = "[\(host)]"
        }
        if let port {
            return "\(host):\(port)"
        }
        return host
    }
}
