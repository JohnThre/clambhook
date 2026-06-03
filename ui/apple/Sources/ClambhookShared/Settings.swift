import Foundation

public let defaultAPIEndpoint = URL(string: "http://127.0.0.1:9090")!
public let defaultAppGroupIdentifier = "group.org.jpfchang.clambhook"
public let defaultPrivacyPolicyURL = URL(string: "https://jpfchang.org/clambhook/privacy")!
public let defaultSupportURL = URL(string: "https://jpfchang.org/clambhook/support")!
public let minRefreshIntervalSeconds: Double = 1
public let maxRefreshIntervalSeconds: Double = 30
public let minLogRetention = 50
public let maxLogRetention = 500
public let vpnDataUseDisclosure = """
ClambHook creates a local VPN configuration to route device network traffic according to your profiles and rules. Default inspection is metadata-only: connection targets, routing decisions, byte counts, timing, and hop status. HTTPS body capture is opt-in and local; when enabled through developer capture config, ClambHook creates a local certificate authority for devices you explicitly trust, stores bounded request and response previews on this device, and exports HAR data only when you share it. Profile data, connection metadata, body previews, logs, and diagnostics stay on this device unless you export them. ClambHook does not sell, use, or disclose VPN traffic data to third parties. Apple diagnostics may include crash and performance data if enabled.
"""

public struct AppSettings: Codable, Equatable, Sendable {
    public var apiEndpoint: URL
    public var daemonBinaryPath: String
    public var daemonConfigPath: String
    public var daemonBinaryBookmark: Data?
    public var daemonConfigBookmark: Data?
    public var launchDaemonOnStart: Bool
    public var stopDaemonOnQuit: Bool
    public var refreshIntervalSeconds: Double
    public var logRetention: Int
    public var appGroupIdentifier: String
    public var inspectionLockEnabled: Bool
    public var pinnedConnectionIDs: [String]

    public init(
        apiEndpoint: URL = defaultAPIEndpoint,
        daemonBinaryPath: String = "",
        daemonConfigPath: String = "",
        daemonBinaryBookmark: Data? = nil,
        daemonConfigBookmark: Data? = nil,
        launchDaemonOnStart: Bool = true,
        stopDaemonOnQuit: Bool = true,
        refreshIntervalSeconds: Double = 2,
        logRetention: Int = maxLogLines,
        appGroupIdentifier: String = defaultAppGroupIdentifier,
        inspectionLockEnabled: Bool = false,
        pinnedConnectionIDs: [String] = []
    ) {
        self.apiEndpoint = apiEndpoint
        self.daemonBinaryPath = daemonBinaryPath
        self.daemonConfigPath = daemonConfigPath
        self.daemonBinaryBookmark = daemonBinaryBookmark
        self.daemonConfigBookmark = daemonConfigBookmark
        self.launchDaemonOnStart = launchDaemonOnStart
        self.stopDaemonOnQuit = stopDaemonOnQuit
        self.refreshIntervalSeconds = refreshIntervalSeconds
        self.logRetention = logRetention
        self.appGroupIdentifier = appGroupIdentifier
        self.inspectionLockEnabled = inspectionLockEnabled
        self.pinnedConnectionIDs = pinnedConnectionIDs
    }

    enum CodingKeys: String, CodingKey {
        case apiEndpoint
        case daemonBinaryPath
        case daemonConfigPath
        case daemonBinaryBookmark
        case daemonConfigBookmark
        case launchDaemonOnStart
        case stopDaemonOnQuit
        case refreshIntervalSeconds
        case logRetention
        case appGroupIdentifier
        case inspectionLockEnabled
        case pinnedConnectionIDs
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.apiEndpoint = try container.decodeIfPresent(URL.self, forKey: .apiEndpoint) ?? defaultAPIEndpoint
        self.daemonBinaryPath = try container.decodeIfPresent(String.self, forKey: .daemonBinaryPath) ?? ""
        self.daemonConfigPath = try container.decodeIfPresent(String.self, forKey: .daemonConfigPath) ?? ""
        self.daemonBinaryBookmark = try container.decodeIfPresent(Data.self, forKey: .daemonBinaryBookmark)
        self.daemonConfigBookmark = try container.decodeIfPresent(Data.self, forKey: .daemonConfigBookmark)
        self.launchDaemonOnStart = try container.decodeIfPresent(Bool.self, forKey: .launchDaemonOnStart) ?? true
        self.stopDaemonOnQuit = try container.decodeIfPresent(Bool.self, forKey: .stopDaemonOnQuit) ?? true
        self.refreshIntervalSeconds = try container.decodeIfPresent(Double.self, forKey: .refreshIntervalSeconds) ?? 2
        self.logRetention = try container.decodeIfPresent(Int.self, forKey: .logRetention) ?? maxLogLines
        self.appGroupIdentifier = try container.decodeIfPresent(String.self, forKey: .appGroupIdentifier) ?? defaultAppGroupIdentifier
        self.inspectionLockEnabled = try container.decodeIfPresent(Bool.self, forKey: .inspectionLockEnabled) ?? false
        self.pinnedConnectionIDs = try container.decodeIfPresent([String].self, forKey: .pinnedConnectionIDs) ?? []
    }

    public func normalized() -> AppSettings {
        var copy = self
        if !Self.isSupportedAPIEndpoint(copy.apiEndpoint) {
            copy.apiEndpoint = defaultAPIEndpoint
        }
        copy.daemonBinaryPath = copy.daemonBinaryPath.trimmingCharacters(in: .whitespacesAndNewlines)
        copy.daemonConfigPath = copy.daemonConfigPath.trimmingCharacters(in: .whitespacesAndNewlines)
        copy.refreshIntervalSeconds = min(max(copy.refreshIntervalSeconds, minRefreshIntervalSeconds), maxRefreshIntervalSeconds)
        copy.logRetention = min(max(copy.logRetention, minLogRetention), maxLogRetention)
        if copy.appGroupIdentifier.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            copy.appGroupIdentifier = defaultAppGroupIdentifier
        }
        copy.pinnedConnectionIDs = Array(Set(copy.pinnedConnectionIDs.map {
            $0.trimmingCharacters(in: .whitespacesAndNewlines)
        }.filter {
            !$0.isEmpty
        })).sorted()
        return copy
    }

    public static func isSupportedAPIEndpoint(_ url: URL) -> Bool {
        guard let scheme = url.scheme?.lowercased(),
              scheme == "http" || scheme == "https",
              url.host?.isEmpty == false
        else {
            return false
        }
        return true
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
            settings = decoded.normalized()
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

    public init(service: String = "org.jpfchang.clambhook.api-token") {
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
