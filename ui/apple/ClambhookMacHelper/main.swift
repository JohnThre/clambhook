import Foundation
import Security

private let allowedClientRequirement = """
anchor apple generic and certificate leaf[subject.OU] = "\(defaultAllowedTeamIdentifier)" and identifier "\(defaultAllowedClientIdentifier)"
"""
private let defaultAllowedTeamIdentifier = "V6GG4HYABJ"
private let defaultAllowedClientIdentifier = "org.jpfchang.clambhook.mac"
private let daemonAPITokenEnvironmentKey = "CLAMBHOOK_API_TOKEN"
// Must match StandardOutPath / StandardErrorPath in the helper launchd plist.
private let helperLogPath = "/var/log/clambhook-helper.log"

final class ClambhookPrivilegedHelperService: NSObject, ClambhookPrivilegedHelperProtocol {
    private let lock = NSLock()
    private var daemonProcess: Process?

    // Crash-recovery state. When the daemon exits unexpectedly (not stopped
    // via stopDaemon), the helper relaunches it up to maxRelaunchAttempts
    // times with exponential backoff. The last launch parameters are captured
    // so relaunch uses the same config, address, and token. relaunchAttempts
    // resets to 0 on a successful stop or a fresh startDaemon call.
    // isStopping distinguishes an intentional stopDaemon (SIGTERM → non-zero
    // exit) from a genuine crash.
    private var lastConfigPath: String?
    private var lastAPIAddress: String?
    private var lastAPIToken: String?
    private var relaunchAttempts = 0
    private var isStopping = false
    private let maxRelaunchAttempts = 3
    private let relaunchBaseDelay: TimeInterval = 1.0

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

        // A fresh startDaemon call resets crash-recovery state.
        lastConfigPath = configPath
        lastAPIAddress = apiAddress
        lastAPIToken = apiToken
        relaunchAttempts = 0
        isStopping = false
        do {
            let executable = try bundledDaemonURL()
            let process = Process()
            process.executableURL = executable
            process.arguments = daemonArguments(configPath: configPath, apiAddress: apiAddress)
            process.environment = daemonEnvironment(apiToken: apiToken)
            process.terminationHandler = { [weak self] proc in
                self?.handleDaemonTermination(proc)
            }
            try process.run()
            daemonProcess = process
            reply(statusReply(locked: true, message: "daemon started"))
        } catch {
            reply(errorReply(error.localizedDescription))
        }
    }

    // handleDaemonTermination clears the process reference and, if the exit
    // was not an intentional stopDaemon, attempts a bounded relaunch with
    // exponential backoff. Process.terminate() sends SIGTERM, so the exit
    // status is non-zero on an intentional stop — the isStopping flag
    // distinguishes that from a genuine crash.
    private func handleDaemonTermination(_ proc: Process) {
        lock.lock()
        daemonProcess = nil
        let wasIntentional = isStopping
        isStopping = false
        let shouldRelaunch = !wasIntentional && relaunchAttempts < maxRelaunchAttempts
        if shouldRelaunch {
            relaunchAttempts += 1
            let attempt = relaunchAttempts
            let configPath = lastConfigPath
            let apiAddress = lastAPIAddress
            let apiToken = lastAPIToken
            lock.unlock()
            let delay = relaunchBaseDelay * pow(2.0, Double(attempt - 1))
            logHelper("daemon exited unexpectedly (status \(proc.terminationStatus)); relaunch attempt \(attempt)/\(maxRelaunchAttempts) in \(Int(delay))s")
            DispatchQueue.global().asyncAfter(deadline: .now() + delay) { [weak self] in
                self?.relaunchDaemon(configPath: configPath, apiAddress: apiAddress, apiToken: apiToken)
            }
        } else {
            lock.unlock()
            if !wasIntentional {
                logHelper("daemon exited unexpectedly; exhausted \(maxRelaunchAttempts) relaunch attempts")
            }
        }
    }

    // relaunchDaemon attempts to restart the daemon with the last-known
    // parameters. Called off the lock after a backoff delay.
    private func relaunchDaemon(configPath: String?, apiAddress: String?, apiToken: String?) {
        lock.lock()
        defer { lock.unlock() }
        guard let configPath, let apiAddress, let apiToken else { return }
        guard daemonProcess?.isRunning != true else { return }
        do {
            let executable = try bundledDaemonURL()
            let process = Process()
            process.executableURL = executable
            process.arguments = daemonArguments(configPath: configPath, apiAddress: apiAddress)
            process.environment = daemonEnvironment(apiToken: apiToken)
            process.terminationHandler = { [weak self] proc in
                self?.handleDaemonTermination(proc)
            }
            try process.run()
            daemonProcess = process
            logHelper("daemon relaunched (attempt \(relaunchAttempts)/\(maxRelaunchAttempts))")
        } catch {
            logHelper("daemon relaunch failed: \(error.localizedDescription)")
        }
    }

    func stopDaemon(withReply reply: @escaping (NSDictionary) -> Void) {
        lock.lock()
        defer { lock.unlock() }

        // An intentional stop sets isStopping so the termination handler
        // does not relaunch, and clears the launch parameters.
        isStopping = true
        lastConfigPath = nil
        lastAPIAddress = nil
        lastAPIToken = nil

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

    private func daemonArguments(configPath: String, apiAddress: String) -> [String] {
        var args: [String] = []
        let trimmedAPI = apiAddress.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmedAPI.isEmpty {
            args += ["-api", trimmedAPI]
        }
        let trimmedConfig = configPath.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmedConfig.isEmpty {
            args += ["-config", trimmedConfig]
        }
        return args
    }

    // The API bearer token is passed through the child environment instead of the
    // argument vector so it never appears in `ps` output. The daemon reads
    // CLAMBHOOK_API_TOKEN as the default for its `-api-token` flag.
    private func daemonEnvironment(apiToken: String) -> [String: String] {
        var env = ProcessInfo.processInfo.environment
        let trimmedToken = apiToken.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmedToken.isEmpty {
            env.removeValue(forKey: daemonAPITokenEnvironmentKey)
        } else {
            env[daemonAPITokenEnvironmentKey] = trimmedToken
        }
        return env
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
        // Pin the peer by its kernel-provided audit token rather than its PID:
        // a PID can be recycled or spoofed in a check/use window.
        guard let auditToken = connection.clambhookAuditToken else {
            return false
        }
        return isAllowed(auditToken: auditToken)
    }

    static func isAllowed(auditToken: audit_token_t) -> Bool {
        let tokenData = withUnsafeBytes(of: auditToken) { Data($0) }
        var code: SecCode?
        let attrs = [kSecGuestAttributeAudit as String: tokenData] as CFDictionary
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

private extension NSXPCConnection {
    /// The audit token of the peer process.
    ///
    /// `NSXPCConnection` exposes `auditToken` as SPI; it is read here via KVC so
    /// no bridging header is required. Returns `nil` (fail-closed) if the
    /// property is unavailable.
    var clambhookAuditToken: audit_token_t? {
        let selector = Selector(("auditToken"))
        guard responds(to: selector),
              let value = value(forKey: "auditToken") as? NSValue
        else {
            return nil
        }
        var token = audit_token_t()
        withUnsafeMutableBytes(of: &token) { raw in
            value.getValue(raw.baseAddress!, size: raw.count)
        }
        return token
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

// The launchd-redirected helper log can contain sensitive daemon output, so it
// must never be world-readable. launchd may create it with the process umask
// before this runs, so tighten it explicitly on startup.
func restrictHelperLog() {
    let fm = FileManager.default
    let attrs: [FileAttributeKey: Any] = [.posixPermissions: 0o600]
    if fm.fileExists(atPath: helperLogPath) {
        try? fm.setAttributes(attrs, ofItemAtPath: helperLogPath)
    } else {
        fm.createFile(atPath: helperLogPath, contents: nil, attributes: attrs)
    }
}

/// logHelper writes a timestamped message to the helper log file and stderr.
/// Used for crash-recovery diagnostics that must survive the daemon's exit.
func logHelper(_ message: String) {
    let line = "\(Date()) clambhook-helper: \(message)\n"
    FileHandle(forWritingAtPath: helperLogPath)?.write(Data(line.utf8))
    FileHandle.standardError.write(Data(line.utf8))
}

restrictHelperLog()
let delegate = ClambhookPrivilegedHelperListenerDelegate()
let listener = NSXPCListener(machServiceName: macPrivilegedHelperMachServiceName)
listener.delegate = delegate
listener.resume()
RunLoop.main.run()
