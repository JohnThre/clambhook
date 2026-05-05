import ClambhookShared
import Foundation

@MainActor
final class DaemonSupervisor: ObservableObject {
    @Published private(set) var isRunning = false
    private var process: Process?

    func launch(settings: AppSettings, token: String) throws {
        if process?.isRunning == true {
            isRunning = true
            return
        }
        let executable = try daemonExecutable(settings: settings)
        let process = Process()
        process.executableURL = executable
        process.arguments = daemonArguments(settings: settings, token: token)
        process.terminationHandler = { [weak self] _ in
            Task { @MainActor in
                self?.isRunning = false
                self?.process = nil
            }
        }
        try process.run()
        self.process = process
        isRunning = true
    }

    func stop() {
        guard let process else {
            isRunning = false
            return
        }
        if process.isRunning {
            process.terminate()
        }
        self.process = nil
        isRunning = false
    }

    private func daemonExecutable(settings: AppSettings) throws -> URL {
        if !settings.daemonBinaryPath.isEmpty {
            return URL(fileURLWithPath: settings.daemonBinaryPath)
        }
        if let bundled = Bundle.main.url(forResource: "clambhook", withExtension: nil) {
            return bundled
        }
        throw DaemonSupervisorError.missingBinary
    }

    private func daemonArguments(settings: AppSettings, token: String) -> [String] {
        var args: [String] = ["-api", settings.apiEndpoint.hostPort]
        if !token.isEmpty {
            args += ["-api-token", token]
        }
        if !settings.daemonConfigPath.isEmpty {
            args += ["-config", settings.daemonConfigPath]
        }
        return args
    }
}

enum DaemonSupervisorError: Error, LocalizedError {
    case missingBinary

    var errorDescription: String? {
        switch self {
        case .missingBinary:
            return "Set a daemon binary path in Settings or include clambhook in the app bundle."
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
