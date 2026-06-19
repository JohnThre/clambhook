import Foundation
import Security

private let allowedClientRequirement = """
anchor apple generic and certificate leaf[subject.OU] = "\(defaultAllowedTeamIdentifier)" and identifier "\(defaultAllowedClientIdentifier)"
"""
private let defaultAllowedTeamIdentifier = "V6GG4HYABJ"
private let defaultAllowedClientIdentifier = "org.jpfchang.clambhook.mac"

final class ClambhookPrivilegedHelperService: NSObject, ClambhookPrivilegedHelperProtocol {
    private let lock = NSLock()
    private var daemonProcess: Process?

    func status(withReply reply: @escaping (NSDictionary) -> Void) {
        reply(statusReply())
    }

    func startDaemon(
        configPath: String,
        apiAddress: String,
        apiToken: String,
        withReply reply: @escaping (NSDictionary) -> Void
    ) {
        lock.lock()
        defer { lock.unlock() }

        if let daemonProcess, daemonProcess.isRunning {
            reply(statusReply(locked: true, message: "daemon already running"))
            return
        }

        do {
            let executable = try bundledDaemonURL()
            let process = Process()
            process.executableURL = executable
            process.arguments = daemonArguments(configPath: configPath, apiAddress: apiAddress, apiToken: apiToken)
            process.terminationHandler = { [weak self] _ in
                self?.lock.lock()
                defer { self?.lock.unlock() }
                self?.daemonProcess = nil
            }
            try process.run()
            daemonProcess = process
            reply(statusReply(locked: true, message: "daemon started"))
        } catch {
            reply(errorReply(error.localizedDescription))
        }
    }

    func stopDaemon(withReply reply: @escaping (NSDictionary) -> Void) {
        lock.lock()
        defer { lock.unlock() }

        guard let daemonProcess else {
            reply(statusReply(locked: true, message: "daemon stopped"))
            return
        }
        if daemonProcess.isRunning {
            daemonProcess.terminate()
        }
        self.daemonProcess = nil
        reply(statusReply(locked: true, message: "daemon stopped"))
    }

    private func daemonArguments(configPath: String, apiAddress: String, apiToken: String) -> [String] {
        var args: [String] = []
        let trimmedAPI = apiAddress.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmedAPI.isEmpty {
            args += ["-api", trimmedAPI]
        }
        let trimmedToken = apiToken.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmedToken.isEmpty {
            args += ["-api-token", trimmedToken]
        }
        let trimmedConfig = configPath.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmedConfig.isEmpty {
            args += ["-config", trimmedConfig]
        }
        return args
    }

    private func bundledDaemonURL() throws -> URL {
        let helperURL = URL(fileURLWithPath: CommandLine.arguments[0]).resolvingSymlinksInPath()
        let contentsURL = helperURL
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
        let daemonURL = contentsURL.appendingPathComponent("MacOS/clambhook")
        guard FileManager.default.isExecutableFile(atPath: daemonURL.path) else {
            throw HelperError.missingDaemon(daemonURL.path)
        }
        return daemonURL
    }

    private func statusReply(locked: Bool = false, message: String = "") -> NSDictionary {
        if !locked {
            lock.lock()
        }
        defer {
            if !locked {
                lock.unlock()
            }
        }
        let running = daemonProcess?.isRunning == true
        var payload: [String: Any] = [
            MacPrivilegedHelperReplyKey.ok: true,
            MacPrivilegedHelperReplyKey.running: running,
            MacPrivilegedHelperReplyKey.message: message,
        ]
        if let daemonProcess, running {
            payload[MacPrivilegedHelperReplyKey.pid] = daemonProcess.processIdentifier
        }
        payload[MacPrivilegedHelperReplyKey.executablePath] = (try? bundledDaemonURL().path) ?? ""
        return payload as NSDictionary
    }

    private func errorReply(_ message: String) -> NSDictionary {
        [
            MacPrivilegedHelperReplyKey.ok: false,
            MacPrivilegedHelperReplyKey.running: false,
            MacPrivilegedHelperReplyKey.message: message,
        ] as NSDictionary
    }
}

final class ClambhookPrivilegedHelperListenerDelegate: NSObject, NSXPCListenerDelegate {
    private let service = ClambhookPrivilegedHelperService()

    func listener(_ listener: NSXPCListener, shouldAcceptNewConnection connection: NSXPCConnection) -> Bool {
        guard ClientCodeRequirementValidator.isAllowed(connection: connection) else {
            NSLog("clambhook helper rejected XPC client pid \(connection.processIdentifier)")
            return false
        }
        connection.exportedInterface = NSXPCInterface(with: ClambhookPrivilegedHelperProtocol.self)
        connection.exportedObject = service
        connection.resume()
        return true
    }
}

enum ClientCodeRequirementValidator {
    static func isAllowed(connection: NSXPCConnection) -> Bool {
        var code: SecCode?
        let attrs = [kSecGuestAttributePid as String: connection.processIdentifier] as CFDictionary
        guard SecCodeCopyGuestWithAttributes(nil, attrs, SecCSFlags(), &code) == errSecSuccess,
              let code
        else {
            return false
        }

        var requirement: SecRequirement?
        guard SecRequirementCreateWithString(allowedClientRequirement as CFString, SecCSFlags(), &requirement) == errSecSuccess,
              let requirement
        else {
            return false
        }
        return SecCodeCheckValidity(code, SecCSFlags(), requirement) == errSecSuccess
    }
}

enum HelperError: LocalizedError {
    case missingDaemon(String)

    var errorDescription: String? {
        switch self {
        case .missingDaemon(let path):
            return "missing bundled clambhook daemon at \(path)"
        }
    }
}

let delegate = ClambhookPrivilegedHelperListenerDelegate()
let listener = NSXPCListener(machServiceName: macPrivilegedHelperMachServiceName)
listener.delegate = delegate
listener.resume()
RunLoop.main.run()
