import Foundation

public struct ConfigSettingsPayload: Codable, Equatable, Sendable {
    public var profile: String
    public var listen: ConfigListenSettingsPayload
    public var dns: ConfigDNSSettingsPayload
    public var backupPath: String

    enum CodingKeys: String, CodingKey {
        case profile
        case listen
        case dns
        case backupPath = "backup_path"
    }

    public init(
        profile: String = "",
        listen: ConfigListenSettingsPayload = ConfigListenSettingsPayload(),
        dns: ConfigDNSSettingsPayload = ConfigDNSSettingsPayload(),
        backupPath: String = ""
    ) {
        self.profile = profile
        self.listen = listen
        self.dns = dns
        self.backupPath = backupPath
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.profile = try container.decodeIfPresent(String.self, forKey: .profile) ?? ""
        self.listen = try container.decodeIfPresent(ConfigListenSettingsPayload.self, forKey: .listen) ?? ConfigListenSettingsPayload()
        self.dns = try container.decodeIfPresent(ConfigDNSSettingsPayload.self, forKey: .dns) ?? ConfigDNSSettingsPayload()
        self.backupPath = try container.decodeIfPresent(String.self, forKey: .backupPath) ?? ""
    }
}

public struct ConfigListenSettingsPayload: Codable, Equatable, Sendable {
    public var socks5: String
    public var socks5Chain: String
    public var http: String
    public var httpChain: String

    enum CodingKeys: String, CodingKey {
        case socks5
        case socks5Chain = "socks5_chain"
        case http
        case httpChain = "http_chain"
    }

    public init(socks5: String = "", socks5Chain: String = "", http: String = "", httpChain: String = "") {
        self.socks5 = socks5
        self.socks5Chain = socks5Chain
        self.http = http
        self.httpChain = httpChain
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.socks5 = try container.decodeIfPresent(String.self, forKey: .socks5) ?? ""
        self.socks5Chain = try container.decodeIfPresent(String.self, forKey: .socks5Chain) ?? ""
        self.http = try container.decodeIfPresent(String.self, forKey: .http) ?? ""
        self.httpChain = try container.decodeIfPresent(String.self, forKey: .httpChain) ?? ""
    }
}

public struct ConfigDNSSettingsPayload: Codable, Equatable, Sendable {
    public var enabled: Bool
    public var timeout: String
    public var upstreams: [DNSUpstreamPayload]

    enum CodingKeys: String, CodingKey {
        case enabled
        case timeout
        case upstreams
    }

    public init(enabled: Bool = false, timeout: String = "5s", upstreams: [DNSUpstreamPayload] = []) {
        self.enabled = enabled
        self.timeout = timeout
        self.upstreams = upstreams
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.enabled = try container.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        self.timeout = try container.decodeIfPresent(String.self, forKey: .timeout) ?? "5s"
        self.upstreams = try container.decodeIfPresent([DNSUpstreamPayload].self, forKey: .upstreams) ?? []
    }
}

public struct ConfigSettingsUpdateRequest: Codable, Equatable, Sendable {
    public var profile: String
    public var listen: ConfigListenSettingsPayload?
    public var dns: ConfigDNSSettingsPayload?

    public init(profile: String = "", listen: ConfigListenSettingsPayload? = nil, dns: ConfigDNSSettingsPayload? = nil) {
        self.profile = profile
        self.listen = listen
        self.dns = dns
    }
}

public struct MacUpdateManifest: Codable, Equatable, Sendable {
    public var version: String
    public var build: String
    public var publishedAt: Date?
    public var minimumOSVersion: String
    public var url: URL
    public var filename: String
    public var sha256: String
    public var size: Int64

    enum CodingKeys: String, CodingKey {
        case version
        case build
        case publishedAt = "published_at"
        case minimumOSVersion = "minimum_os_version"
        case url
        case filename
        case sha256
        case size
    }

    public init(
        version: String = "",
        build: String = "",
        publishedAt: Date? = nil,
        minimumOSVersion: String = "",
        url: URL = URL(string: "https://jpfchang.org/clambhook")!,
        filename: String = "",
        sha256: String = "",
        size: Int64 = 0
    ) {
        self.version = version
        self.build = build
        self.publishedAt = publishedAt
        self.minimumOSVersion = minimumOSVersion
        self.url = url
        self.filename = filename
        self.sha256 = sha256
        self.size = size
    }
}

public enum MacUpdateComparator {
    public static func isUpdateAvailable(currentVersion: String, currentBuild: String, manifest: MacUpdateManifest) -> Bool {
        let versionOrder = manifest.version.compare(currentVersion, options: .numeric)
        if versionOrder == .orderedDescending {
            return true
        }
        if versionOrder == .orderedAscending {
            return false
        }
        return manifest.build.compare(currentBuild, options: .numeric) == .orderedDescending
    }
}
