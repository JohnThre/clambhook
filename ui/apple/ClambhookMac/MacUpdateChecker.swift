import ClambhookShared
import Foundation

@MainActor
final class MacUpdateChecker: ObservableObject {
    @Published private(set) var state = MacUpdateState.idle
    @Published private(set) var manifest: MacUpdateManifest?

    private let session: URLSession
    private let decoder: JSONDecoder

    init(session: URLSession = .shared) {
        self.session = session
        self.decoder = JSONDecoder()
        self.decoder.dateDecodingStrategy = .iso8601
    }

    func check(settings: AppSettings) {
        state = .checking
        Task {
            do {
                let (data, response) = try await session.data(from: settings.normalized().updateManifestURL)
                if let http = response as? HTTPURLResponse, !(200...299).contains(http.statusCode) {
                    throw MacUpdateError.httpStatus(http.statusCode)
                }
                let decoded = try decoder.decode(MacUpdateManifest.self, from: data)
                manifest = decoded
                state = MacUpdateComparator.isUpdateAvailable(
                    currentVersion: Self.currentVersion,
                    currentBuild: Self.currentBuild,
                    manifest: decoded
                ) ? .available : .current
            } catch {
                state = .failed(error.localizedDescription)
            }
        }
    }

    private static var currentVersion: String {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "0"
    }

    private static var currentBuild: String {
        Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "0"
    }
}

enum MacUpdateState: Equatable {
    case idle
    case checking
    case current
    case available
    case failed(String)

    var label: String {
        switch self {
        case .idle:
            return "Not checked"
        case .checking:
            return "Checking"
        case .current:
            return "Up to date"
        case .available:
            return "Update available"
        case .failed:
            return "Update check failed"
        }
    }
}

private enum MacUpdateError: Error, LocalizedError {
    case httpStatus(Int)

    var errorDescription: String? {
        switch self {
        case .httpStatus(let status):
            return "Update manifest returned HTTP \(status)."
        }
    }
}
