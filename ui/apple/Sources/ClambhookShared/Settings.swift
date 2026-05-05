import Foundation

public let defaultAPIEndpoint = URL(string: "http://127.0.0.1:9090")!
public let defaultAppGroupIdentifier = "group.com.clambhook.shared"

public struct AppSettings: Codable, Equatable, Sendable {
    public var apiEndpoint: URL
    public var daemonBinaryPath: String
    public var daemonConfigPath: String
    public var launchDaemonOnStart: Bool
    public var stopDaemonOnQuit: Bool
    public var refreshIntervalSeconds: Double
    public var logRetention: Int
    public var appGroupIdentifier: String

    public init(
        apiEndpoint: URL = defaultAPIEndpoint,
        daemonBinaryPath: String = "",
        daemonConfigPath: String = "",
        launchDaemonOnStart: Bool = true,
        stopDaemonOnQuit: Bool = true,
        refreshIntervalSeconds: Double = 2,
        logRetention: Int = maxLogLines,
        appGroupIdentifier: String = defaultAppGroupIdentifier
    ) {
        self.apiEndpoint = apiEndpoint
        self.daemonBinaryPath = daemonBinaryPath
        self.daemonConfigPath = daemonConfigPath
        self.launchDaemonOnStart = launchDaemonOnStart
        self.stopDaemonOnQuit = stopDaemonOnQuit
        self.refreshIntervalSeconds = refreshIntervalSeconds
        self.logRetention = logRetention
        self.appGroupIdentifier = appGroupIdentifier
    }
}

@MainActor
public final class AppSettingsStore: ObservableObject {
    @Published public var settings: AppSettings {
        didSet { save() }
    }

    private let defaults: UserDefaults
    private let key: String

    public init(defaults: UserDefaults = .standard, key: String = "clambhook.apple.settings") {
        self.defaults = defaults
        self.key = key
        if let data = defaults.data(forKey: key),
           let decoded = try? JSONDecoder().decode(AppSettings.self, from: data) {
            settings = decoded
        } else {
            settings = AppSettings()
        }
    }

    public func save() {
        if let data = try? JSONEncoder().encode(settings) {
            defaults.set(data, forKey: key)
        }
    }
}

public protocol CredentialStoring {
    func readToken(account: String) throws -> String?
    func saveToken(_ token: String?, account: String) throws
}

public final class InMemoryCredentialStore: CredentialStoring {
    private var tokens: [String: String] = [:]

    public init() {}

    public func readToken(account: String) throws -> String? {
        tokens[account]
    }

    public func saveToken(_ token: String?, account: String) throws {
        tokens[account] = token?.isEmpty == true ? nil : token
    }
}

#if canImport(Security)
import Security

public final class KeychainCredentialStore: CredentialStoring {
    private let service: String

    public init(service: String = "com.clambhook.apple.api-token") {
        self.service = service
    }

    public func readToken(account: String) throws -> String? {
        var query = baseQuery(account: account)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound {
            return nil
        }
        guard status == errSecSuccess else {
            throw KeychainError(status: status)
        }
        guard let data = result as? Data else {
            return nil
        }
        return String(data: data, encoding: .utf8)
    }

    public func saveToken(_ token: String?, account: String) throws {
        let query = baseQuery(account: account)
        let deleteStatus = SecItemDelete(query as CFDictionary)
        guard deleteStatus == errSecSuccess || deleteStatus == errSecItemNotFound else {
            throw KeychainError(status: deleteStatus)
        }
        guard let token, !token.isEmpty else {
            return
        }
        var item = query
        item[kSecValueData as String] = Data(token.utf8)
        let addStatus = SecItemAdd(item as CFDictionary, nil)
        guard addStatus == errSecSuccess else {
            throw KeychainError(status: addStatus)
        }
    }

    private func baseQuery(account: String) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
    }
}

public struct KeychainError: Error, LocalizedError {
    public var status: OSStatus

    public var errorDescription: String? {
        "keychain error \(status)"
    }
}
#endif
