import ClambhookShared
import CryptoKit
import Foundation

enum MacCertificateTrustStatus: Equatable {
    case unavailable
    case checking
    case trusted
    case notTrusted
    case failed(String)

    var label: String {
        switch self {
        case .unavailable:
            return "Certificate unavailable"
        case .checking:
            return "Checking certificate trust"
        case .trusted:
            return "Certificate trusted"
        case .notTrusted:
            return "Certificate not trusted"
        case .failed:
            return "Certificate trust check failed"
        }
    }
}

@MainActor
final class MacCertificateManager: ObservableObject {
    @Published private(set) var fingerprint = ""
    @Published private(set) var trustStatus: MacCertificateTrustStatus = .unavailable
    @Published private(set) var isWorking = false
    @Published private(set) var statusMessage = ""

    private let runner: MacCommandRunning

    init(runner: MacCommandRunning = MacCommandRunner()) {
        self.runner = runner
    }

    func refreshFingerprint(pem: String) {
        fingerprint = Self.fingerprint(for: pem)
        refreshTrustStatus(pem: pem)
    }

    func install(pem: String) {
        runTrustCommand(pem: pem, remove: false)
    }

    func remove(pem: String) {
        runTrustCommand(pem: pem, remove: true)
    }

    func refreshTrustStatus(pem: String) {
        let expectedFingerprint = Self.fingerprint(for: pem)
        guard !expectedFingerprint.isEmpty else {
            trustStatus = .unavailable
            return
        }
        trustStatus = .checking
        Task {
            do {
                let url = try writeTemporaryPEM(pem)
                defer { try? FileManager.default.removeItem(at: url) }
                _ = try runner.run("/usr/bin/security", arguments: ["verify-cert", "-c", url.path, "-p", "ssl"])
                if fingerprint == expectedFingerprint {
                    trustStatus = .trusted
                }
            } catch MacCommandError.failed {
                if fingerprint == expectedFingerprint {
                    trustStatus = .notTrusted
                }
            } catch {
                if fingerprint == expectedFingerprint {
                    trustStatus = .failed(error.localizedDescription)
                }
            }
        }
    }

    private func runTrustCommand(pem: String, remove: Bool) {
        isWorking = true
        statusMessage = ""
        Task {
            do {
                let url = try writeTemporaryPEM(pem)
                defer { try? FileManager.default.removeItem(at: url) }
                if remove {
                    _ = try runner.run("/usr/bin/security", arguments: ["remove-trusted-cert", url.path])
                    statusMessage = "CA trust removed from user settings"
                } else {
                    _ = try runner.run("/usr/bin/security", arguments: ["add-trusted-cert", "-r", "trustRoot", "-p", "ssl", "-k", loginKeychainPath(), url.path])
                    statusMessage = "CA trusted for SSL in login keychain"
                }
                refreshTrustStatus(pem: pem)
            } catch {
                statusMessage = error.localizedDescription
            }
            isWorking = false
        }
    }

    private func writeTemporaryPEM(_ pem: String) throws -> URL {
        let url = FileManager.default.temporaryDirectory
            .appendingPathComponent("clambhook-ca-\(UUID().uuidString).pem")
        try pem.write(to: url, atomically: true, encoding: .utf8)
        return url
    }

    private func loginKeychainPath() -> String {
        "\(NSHomeDirectory())/Library/Keychains/login.keychain-db"
    }

    static func fingerprint(for pem: String) -> String {
        guard let der = certificateDER(from: pem) else {
            return ""
        }
        return SHA256.hash(data: der).map { String(format: "%02X", $0) }.joined(separator: ":")
    }

    private static func certificateDER(from pem: String) -> Data? {
        let lines = pem.components(separatedBy: .newlines).filter {
            !$0.hasPrefix("-----BEGIN") && !$0.hasPrefix("-----END") && !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }
        return Data(base64Encoded: lines.joined())
    }
}
