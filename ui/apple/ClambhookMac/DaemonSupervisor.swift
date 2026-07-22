import ClambhookShared
import Foundation

enum DaemonState: Equatable {
    case stopped
    case starting
    case running
    case stopping
    case failed(String)

    var isBusy: Bool {
        self == .starting || self == .stopping
    }

    var label: String {
        switch self {
        case .stopped:
            return "Daemon stopped"
        case .starting:
            return "Daemon starting"
        case .running:
            return "Daemon running"
        case .stopping:
            return "Daemon stopping"
        case .failed:
            return "Daemon failed"
        }
    }
}

@MainActor
final class DaemonSupervisor: ObservableObject {
    @Published private(set) var state: DaemonState = .stopped
    private var process: Process?
    private var securityScopedURLs: [URL] = []

    var isRunning: Bool {
        process?.isRunning == true
    }

    func launch(settings: AppSettings, token: String) throws {
        if process?.isRunning == true {
            state = .running
            return
        }
        cleanupSecurityScopes()
        state = .starting
        do {
            let normalized = settings.normalized()
            let executable = try daemonExecutable(settings: normalized)
            beginSecurityScope(for: executable, bookmark: normalized.daemonBinaryBookmark)
            let configURL = daemonConfigURL(settings: normalized)
            if let configURL {
                beginSecurityScope(for: configURL, bookmark: normalized.daemonConfigBookmark)
            }
            let process = Process()
            process.executableURL = executable
            process.arguments = DaemonLaunchPlanner.arguments(
                apiHostPort: normalized.apiEndpoint.hostPort,
                configPath: configURL?.path
            )
            process.environment = DaemonLaunchPlanner.environment(
                base: ProcessInfo.processInfo.environment,
                token: token
            )
            process.terminationHandler = { [weak self] proc in
                Task { @MainActor in
                    guard let self else { return }
                    self.process = nil
                    self.cleanupSecurityScopes()
                    if case .stopping = self.state {
                        self.state = .stopped
                        return
                    }
                    if case .failed = self.state {
                        return
                    }
                    // An unexpected exit (non-zero) surfaces as .failed so
                    // the app can show crash diagnostics rather than silently
                    // showing .stopped.
                    if proc.terminationStatus != 0 {
                        self.state = .failed("daemon exited with status \(proc.terminationStatus)")
                    } else {
                        self.state = .stopped
                    }
                }
            }
            try process.run()
            self.process = process
            state = .running
        } catch {
            cleanupSecurityScopes()
            state = .failed(error.localizedDescription)
            throw error
        }
    }

    func stop() {
        guard let process else {
            state = .stopped
            cleanupSecurityScopes()
            return
        }
        state = .stopping
        if process.isRunning {
            process.terminate()
        }
        self.process = nil
        cleanupSecurityScopes()
        state = .stopped
    }

    private func daemonExecutable(settings: AppSettings) throws -> URL {
        if let bookmarked = bookmarkedURL(settings.daemonBinaryBookmark) {
            return bookmarked
        }
        if !settings.daemonBinaryPath.isEmpty {
            return URL(fileURLWithPath: settings.daemonBinaryPath)
        }
        if let bundled = Bundle.main.url(forAuxiliaryExecutable: "clambhook") {
            return bundled
        }
        if let bundled = Bundle.main.url(forResource: "clambhook", withExtension: nil) {
            return bundled
        }
        throw DaemonSupervisorError.missingBinary
    }

    private func daemonConfigURL(settings: AppSettings) -> URL? {
        if let bookmarked = bookmarkedURL(settings.daemonConfigBookmark) {
            return bookmarked
        }
        if !settings.daemonConfigPath.isEmpty {
            return URL(fileURLWithPath: settings.daemonConfigPath)
        }
        return nil
    }

    private func bookmarkedURL(_ data: Data?) -> URL? {
        guard let data else {
            return nil
        }
        var stale = false
        return try? URL(
            resolvingBookmarkData: data,
            options: [.withSecurityScope],
            relativeTo: nil,
            bookmarkDataIsStale: &stale
        )
    }

    private func beginSecurityScope(for url: URL, bookmark: Data?) {
        guard bookmark != nil else {
            return
        }
        if url.startAccessingSecurityScopedResource() {
            securityScopedURLs.append(url)
        }
    }

    private func cleanupSecurityScopes() {
        for url in securityScopedURLs {
            url.stopAccessingSecurityScopedResource()
        }
        securityScopedURLs.removeAll()
    }
}

enum DaemonSupervisorError: Error, LocalizedError {
    case missingBinary

    var errorDescription: String? {
        switch self {
        case .missingBinary:
            return "Set a daemon binary path in Settings or include clambhook in the app bundle executables."
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
