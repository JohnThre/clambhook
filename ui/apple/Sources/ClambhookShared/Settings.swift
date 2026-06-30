import Foundation

public let defaultAPIEndpoint = URL(string: "http://127.0.0.1:9090")!
public let defaultAppGroupIdentifier = "group.org.jpfchang.clambhook"
public let defaultAppleDeveloperTeamIdentifier = "V6GG4HYABJ"
public let defaultAppleKeychainAccessGroup = "\(defaultAppleDeveloperTeamIdentifier).org.jpfchang.clambhook"
public let clambhookMacAppBundleIdentifier = "org.jpfchang.clambhook.mac"
public let clambhookMacWidgetBundleIdentifier = "org.jpfchang.clambhook.mac.widgets"
public let clambhookMacPrivilegedHelperLabel = "org.jpfchang.clambhook.mac.helper"
public let clambhookMacPrivilegedHelperPlistName = "\(clambhookMacPrivilegedHelperLabel).plist"
public let defaultPrivacyPolicyURL = URL(string: "https://store.clambercloud.com/clambhook/privacy")!
public let defaultSupportURL = URL(string: "https://store.clambercloud.com/clambhook/support")!
public let minRefreshIntervalSeconds: Double = 1
public let maxRefreshIntervalSeconds: Double = 30
public let minLogRetention = 50
public let maxLogRetention = 500
public let defaultStableUpdateManifestURL = URL(string: "https://store.clambercloud.com/api/clambhook/update-manifest")!
public let defaultBetaUpdateManifestURL = URL(string: "https://store.clambercloud.com/api/clambhook/update-manifest?channel=beta")!
public let defaultStableAppcastURL = URL(string: "https://store.clambercloud.com/api/clambhook/appcast.xml")!
public let defaultBetaAppcastURL = URL(string: "https://store.clambercloud.com/api/clambhook/appcast.xml?channel=beta")!
private let legacyStableUpdateManifestURLStrings: Set<String> = [
    "https://jpfchang.org/clambhook/clambhook-update-manifest.json",
    "https://jpfchang.org/api/clambhook/update-manifest",
]
private let legacyBetaUpdateManifestURLStrings: Set<String> = [
    "https://jpfchang.org/clambhook/clambhook-beta-update-manifest.json",
    "https://jpfchang.org/clambhook/clambhook-update-manifest.json?channel=beta",
    "https://jpfchang.org/api/clambhook/update-manifest?channel=beta",
]
public let vpnDataUseDisclosure = """
ClambHook routes device network traffic according to your profiles and rules. macOS inspection is metadata-only: connection targets, routing decisions, byte counts, timing, and hop status. The macOS app does not install a certificate authority, perform TLS MITM, store request or response bodies, export HAR files, or provide body-level redaction workflows. Profile data, connection metadata, traffic logs, and diagnostics stay on this device unless you export them. ClambHook does not sell, use, or disclose routed traffic data to third parties. Apple diagnostics may include crash and performance data if enabled.
"""

public let macOSProxyScopeDisclosure = """
System Proxy mode applies macOS HTTP, HTTPS, and SOCKS proxy settings for apps that honor system proxy configuration. Enhanced Mode starts the privileged daemon with a utun interface for device-wide routing.
"""

public enum AppRoutingMode: String, Codable, CaseIterable, Identifiable, Sendable {
    case systemProxy = "system_proxy"
    case enhancedTUN = "enhanced_tun"

    public var id: String { rawValue }

    public var displayName: String {
        switch self {
        case .systemProxy:
            return "System Proxy"
        case .enhancedTUN:
            return "Enhanced Mode"
        }
    }

    public var requiresPrivilegedHelper: Bool {
        self == .enhancedTUN
    }

    public static func decoded(_ rawValue: String) -> AppRoutingMode {
        switch rawValue {
        case AppRoutingMode.enhancedTUN.rawValue:
            return .enhancedTUN
        case AppRoutingMode.systemProxy.rawValue, "daemon_proxy", "network_extension":
            return .systemProxy
        default:
            return .systemProxy
        }
    }
}

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
    public var licenseValidationEndpoint: URL
    public var systemProxyEnabled: Bool
    public var routingMode: AppRoutingMode
    public var usePrivilegedHelper: Bool
    public var updateChannel: String
    public var stableUpdateManifestURL: URL
    public var betaUpdateManifestURL: URL

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
        pinnedConnectionIDs: [String] = [],
        licenseValidationEndpoint: URL = defaultLicenseValidationURL,
        systemProxyEnabled: Bool = false,
        routingMode: AppRoutingMode = .systemProxy,
        usePrivilegedHelper: Bool = true,
        updateChannel: String = "stable",
        stableUpdateManifestURL: URL = defaultStableUpdateManifestURL,
        betaUpdateManifestURL: URL = defaultBetaUpdateManifestURL
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
        self.licenseValidationEndpoint = licenseValidationEndpoint
        self.systemProxyEnabled = systemProxyEnabled
        self.routingMode = routingMode
        self.usePrivilegedHelper = usePrivilegedHelper
        self.updateChannel = updateChannel
        self.stableUpdateManifestURL = stableUpdateManifestURL
        self.betaUpdateManifestURL = betaUpdateManifestURL
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
        case licenseValidationEndpoint
        case systemProxyEnabled
        case routingMode
        case usePrivilegedHelper
        case updateChannel
        case stableUpdateManifestURL
        case betaUpdateManifestURL
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
        self.licenseValidationEndpoint = try container.decodeIfPresent(URL.self, forKey: .licenseValidationEndpoint) ?? defaultLicenseValidationURL
        self.systemProxyEnabled = try container.decodeIfPresent(Bool.self, forKey: .systemProxyEnabled) ?? false
        let decodedRoutingMode = try container.decodeIfPresent(String.self, forKey: .routingMode) ?? AppRoutingMode.systemProxy.rawValue
        self.routingMode = AppRoutingMode.decoded(decodedRoutingMode)
        self.usePrivilegedHelper = try container.decodeIfPresent(Bool.self, forKey: .usePrivilegedHelper) ?? true
        self.updateChannel = try container.decodeIfPresent(String.self, forKey: .updateChannel) ?? "stable"
        self.stableUpdateManifestURL = try container.decodeIfPresent(URL.self, forKey: .stableUpdateManifestURL) ?? defaultStableUpdateManifestURL
        self.betaUpdateManifestURL = try container.decodeIfPresent(URL.self, forKey: .betaUpdateManifestURL) ?? defaultBetaUpdateManifestURL
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
        if !Self.isSupportedAPIEndpoint(copy.licenseValidationEndpoint) {
            copy.licenseValidationEndpoint = defaultLicenseValidationURL
        }
        copy.routingMode = AppRoutingMode.decoded(copy.routingMode.rawValue)
        if copy.routingMode.requiresPrivilegedHelper {
            copy.usePrivilegedHelper = true
        }
        copy.updateChannel = Self.normalizedUpdateChannel(copy.updateChannel)
        copy.stableUpdateManifestURL = Self.normalizedUpdateManifestEndpoint(
            copy.stableUpdateManifestURL,
            defaultURL: defaultStableUpdateManifestURL,
            legacyAbsoluteStrings: legacyStableUpdateManifestURLStrings
        )
        copy.betaUpdateManifestURL = Self.normalizedUpdateManifestEndpoint(
            copy.betaUpdateManifestURL,
            defaultURL: defaultBetaUpdateManifestURL,
            legacyAbsoluteStrings: legacyBetaUpdateManifestURLStrings
        )
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

    public static func normalizedUpdateChannel(_ value: String) -> String {
        let channel = value.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return channel == "beta" ? "beta" : "stable"
    }

    private static func normalizedUpdateManifestEndpoint(
        _ url: URL,
        defaultURL: URL,
        legacyAbsoluteStrings: Set<String>
    ) -> URL {
        if legacyAbsoluteStrings.contains(url.absoluteString) {
            return defaultURL
        }
        if !isSupportedAPIEndpoint(url) {
            return defaultURL
        }
        return url
    }

    public var updateManifestURL: URL {
        updateChannel == "beta" ? betaUpdateManifestURL : stableUpdateManifestURL
    }

    public var appcastFeedURL: URL {
        updateChannel == "beta" ? defaultBetaAppcastURL : defaultStableAppcastURL
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
    private let accessGroup: String?

    public init(service: String = "org.jpfchang.clambhook.api-token", accessGroup: String? = nil) {
        self.service = service
        self.accessGroup = accessGroup?.isEmpty == true ? nil : accessGroup
    }

    public func readToken(account: String) throws -> String? {
        var missingEntitlement: KeychainError?
        for var query in queryCandidates(account: account) {
            query[kSecReturnData as String] = true
            query[kSecMatchLimit as String] = kSecMatchLimitOne
            var result: CFTypeRef?
            let status = SecItemCopyMatching(query as CFDictionary, &result)
            if status == errSecItemNotFound {
                continue
            }
            if status == errSecMissingEntitlement {
                missingEntitlement = KeychainError(status: status)
                continue
            }
            guard status == errSecSuccess else {
                throw KeychainError(status: status)
            }
            guard let data = result as? Data else {
                return nil
            }
            return String(data: data, encoding: .utf8)
        }
        if let missingEntitlement, accessGroup == nil {
            throw missingEntitlement
        }
        return nil
    }

    public func saveToken(_ token: String?, account: String) throws {
        let candidates = queryCandidates(account: account)
        for query in candidates {
            let deleteStatus = SecItemDelete(query as CFDictionary)
            guard deleteStatus == errSecSuccess || deleteStatus == errSecItemNotFound || deleteStatus == errSecMissingEntitlement else {
                throw KeychainError(status: deleteStatus)
            }
        }
        guard let token, !token.isEmpty else {
            return
        }
        var lastMissingEntitlement: KeychainError?
        for var item in candidates {
            item[kSecValueData as String] = Data(token.utf8)
            let addStatus = SecItemAdd(item as CFDictionary, nil)
            if addStatus == errSecMissingEntitlement {
                lastMissingEntitlement = KeychainError(status: addStatus)
                continue
            }
            guard addStatus == errSecSuccess else {
                throw KeychainError(status: addStatus)
            }
            return
        }
        if let lastMissingEntitlement {
            throw lastMissingEntitlement
        }
    }

    private func queryCandidates(account: String) -> [[String: Any]] {
        let base: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        guard let accessGroup else {
            return [base]
        }
        var shared = base
        shared[kSecAttrAccessGroup as String] = accessGroup
        return [shared, base]
    }
}

public struct KeychainError: Error, LocalizedError {
    public var status: OSStatus

    public var errorDescription: String? {
        "keychain error \(status)"
    }
}
#endif
