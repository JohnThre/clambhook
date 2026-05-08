import Combine
import Foundation

public struct StandaloneConfigDocument: Codable, Equatable, Sendable {
    public var toml: String
    public var activeProfile: String
    public var updatedAt: Date

    public init(toml: String = "", activeProfile: String = "", updatedAt: Date = Date(timeIntervalSince1970: 0)) {
        self.toml = toml
        self.activeProfile = activeProfile
        self.updatedAt = updatedAt
    }
}

public enum StandaloneConfigError: Error, Equatable, LocalizedError {
    case emptyConfig

    public var errorDescription: String? {
        switch self {
        case .emptyConfig:
            return "configuration is empty"
        }
    }
}

@MainActor
public final class StandaloneConfigStore: ObservableObject {
    @Published public private(set) var document: StandaloneConfigDocument

    private let defaults: UserDefaults
    private let key: String
    private let autosave: Bool

    public init(
        defaults: UserDefaults = UserDefaults(suiteName: defaultAppGroupIdentifier) ?? .standard,
        key: String = "clambhook.apple.standalone-config",
        autosave: Bool = true
    ) {
        self.defaults = defaults
        self.key = key
        self.autosave = autosave
        if let data = defaults.data(forKey: key),
           let decoded = try? JSONDecoder().decode(StandaloneConfigDocument.self, from: data) {
            document = decoded
        } else {
            document = StandaloneConfigDocument()
        }
    }

    public func save(_ next: StandaloneConfigDocument) throws {
        guard !next.toml.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw StandaloneConfigError.emptyConfig
        }
        document = next
        if autosave {
            persist()
        }
    }

    public func persist() {
        if let data = try? JSONEncoder().encode(document) {
            defaults.set(data, forKey: key)
        }
    }
}
