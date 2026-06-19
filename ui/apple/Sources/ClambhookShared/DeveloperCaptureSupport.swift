import Foundation

public struct DeveloperStatusPayload: Codable, Equatable, Sendable {
    public var enabled: Bool
    public var mitmEnabled: Bool
    public var captureLimit: Int
    public var bodyLimitBytes: UInt64
    public var headerValueLimitBytes: Int
    public var caCertPath: String
    public var caFingerprintSHA256: String
    public var captureCount: Int

    enum CodingKeys: String, CodingKey {
        case enabled
        case mitmEnabled = "mitm_enabled"
        case captureLimit = "capture_limit"
        case bodyLimitBytes = "body_limit_bytes"
        case headerValueLimitBytes = "header_value_limit_bytes"
        case caCertPath = "ca_cert_path"
        case caFingerprintSHA256 = "ca_fingerprint_sha256"
        case captureCount = "capture_count"
    }

    public init(
        enabled: Bool = false,
        mitmEnabled: Bool = false,
        captureLimit: Int = 0,
        bodyLimitBytes: UInt64 = 0,
        headerValueLimitBytes: Int = 0,
        caCertPath: String = "",
        caFingerprintSHA256: String = "",
        captureCount: Int = 0
    ) {
        self.enabled = enabled
        self.mitmEnabled = mitmEnabled
        self.captureLimit = captureLimit
        self.bodyLimitBytes = bodyLimitBytes
        self.headerValueLimitBytes = headerValueLimitBytes
        self.caCertPath = caCertPath
        self.caFingerprintSHA256 = caFingerprintSHA256
        self.captureCount = captureCount
    }
}

public struct DeveloperEntriesPayload: Codable, Equatable, Sendable {
    public var entries: [DeveloperEntryPayload]

    public init(entries: [DeveloperEntryPayload] = []) {
        self.entries = entries
    }
}

public struct DeveloperEntryPayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var connID: String
    public var profile: String
    public var clientAddr: String
    public var chainName: String
    public var startedAt: String
    public var finishedAt: String
    public var method: String
    public var url: String
    public var scheme: String
    public var host: String
    public var status: Int
    public var request: DeveloperMessagePayload
    public var response: DeveloperMessagePayload
    public var error: String

    enum CodingKeys: String, CodingKey {
        case id
        case connID = "conn_id"
        case profile
        case clientAddr = "client_addr"
        case chainName = "chain_name"
        case startedAt = "started_at"
        case finishedAt = "finished_at"
        case method
        case url
        case scheme
        case host
        case status
        case request
        case response
        case error
    }

    public init(
        id: String = "",
        connID: String = "",
        profile: String = "",
        clientAddr: String = "",
        chainName: String = "",
        startedAt: String = "",
        finishedAt: String = "",
        method: String = "",
        url: String = "",
        scheme: String = "",
        host: String = "",
        status: Int = 0,
        request: DeveloperMessagePayload = DeveloperMessagePayload(),
        response: DeveloperMessagePayload = DeveloperMessagePayload(),
        error: String = ""
    ) {
        self.id = id
        self.connID = connID
        self.profile = profile
        self.clientAddr = clientAddr
        self.chainName = chainName
        self.startedAt = startedAt
        self.finishedAt = finishedAt
        self.method = method
        self.url = url
        self.scheme = scheme
        self.host = host
        self.status = status
        self.request = request
        self.response = response
        self.error = error
    }
}

public struct DeveloperMessagePayload: Codable, Equatable, Sendable {
    public var headers: [DeveloperHeaderPayload]
    public var body: DeveloperBodyPayload

    public init(headers: [DeveloperHeaderPayload] = [], body: DeveloperBodyPayload = DeveloperBodyPayload()) {
        self.headers = headers
        self.body = body
    }
}

public struct DeveloperHeaderPayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String { name }
    public var name: String
    public var value: String
    public var redacted: Bool
    public var truncated: Bool

    public init(name: String = "", value: String = "", redacted: Bool = false, truncated: Bool = false) {
        self.name = name
        self.value = value
        self.redacted = redacted
        self.truncated = truncated
    }
}

public struct DeveloperBodyPayload: Codable, Equatable, Sendable {
    public var size: UInt64
    public var preview: String
    public var previewBytes: UInt64
    public var truncated: Bool
    public var truncatedAfter: UInt64

    enum CodingKeys: String, CodingKey {
        case size
        case preview
        case previewBytes = "preview_bytes"
        case truncated
        case truncatedAfter = "truncated_after"
    }

    public init(size: UInt64 = 0, preview: String = "", previewBytes: UInt64 = 0, truncated: Bool = false, truncatedAfter: UInt64 = 0) {
        self.size = size
        self.preview = preview
        self.previewBytes = previewBytes
        self.truncated = truncated
        self.truncatedAfter = truncatedAfter
    }

    public var hasPreview: Bool {
        !preview.isEmpty || previewBytes > 0 || size > 0
    }
}

public struct DeveloperCAPayload: Codable, Equatable, Sendable {
    public var pem: String

    public init(pem: String = "") {
        self.pem = pem
    }
}

public struct DeveloperMatchPayload: Codable, Equatable, Sendable {
    public var methods: [String]
    public var host: String
    public var pathPrefix: String
    public var urlContains: String

    enum CodingKeys: String, CodingKey {
        case methods
        case host
        case pathPrefix = "path_prefix"
        case urlContains = "url_contains"
    }

    public init(methods: [String] = [], host: String = "", pathPrefix: String = "", urlContains: String = "") {
        self.methods = methods
        self.host = host
        self.pathPrefix = pathPrefix
        self.urlContains = urlContains
    }
}

public struct DeveloperMapRulePayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var name: String
    public var enabled: Bool
    public var match: DeveloperMatchPayload
    public var kind: String
    public var localPath: String
    public var remoteURL: String
    public var status: Int
    public var headers: [String: String]

    enum CodingKeys: String, CodingKey {
        case id
        case name
        case enabled
        case match
        case kind
        case localPath = "local_path"
        case remoteURL = "remote_url"
        case status
        case headers
    }

    public init(id: String = UUID().uuidString, name: String = "", enabled: Bool = true, match: DeveloperMatchPayload = DeveloperMatchPayload(), kind: String = "local", localPath: String = "", remoteURL: String = "", status: Int = 0, headers: [String: String] = [:]) {
        self.id = id
        self.name = name
        self.enabled = enabled
        self.match = match
        self.kind = kind
        self.localPath = localPath
        self.remoteURL = remoteURL
        self.status = status
        self.headers = headers
    }
}

public struct DeveloperBreakpointRulePayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var name: String
    public var enabled: Bool
    public var match: DeveloperMatchPayload
    public var stage: String

    public init(id: String = UUID().uuidString, name: String = "", enabled: Bool = true, match: DeveloperMatchPayload = DeveloperMatchPayload(), stage: String = "both") {
        self.id = id
        self.name = name
        self.enabled = enabled
        self.match = match
        self.stage = stage
    }
}

public struct DeveloperRuleListPayload<T: Codable & Equatable & Sendable>: Codable, Equatable, Sendable {
    public var rules: [T]

    public init(rules: [T] = []) {
        self.rules = rules
    }
}

public struct DeveloperRepeatRequestPayload: Codable, Equatable, Sendable {
    public var entryID: String
    public var method: String
    public var url: String
    public var headers: [DeveloperHeaderPayload]
    public var body: String?

    enum CodingKeys: String, CodingKey {
        case entryID = "entry_id"
        case method
        case url
        case headers
        case body
    }

    public init(entryID: String, method: String = "", url: String = "", headers: [DeveloperHeaderPayload] = [], body: String? = nil) {
        self.entryID = entryID
        self.method = method
        self.url = url
        self.headers = headers
        self.body = body
    }
}

public struct DeveloperRepeatResponsePayload: Codable, Equatable, Sendable {
    public var entry: DeveloperEntryPayload
}

public struct DeveloperPendingBreakpointsPayload: Codable, Equatable, Sendable {
    public var breakpoints: [DeveloperPendingBreakpointPayload]
}

public struct DeveloperPendingBreakpointPayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var ruleID: String
    public var ruleName: String
    public var stage: String
    public var createdAt: String
    public var request: DeveloperBreakpointMessagePayload
    public var response: DeveloperBreakpointMessagePayload?

    enum CodingKeys: String, CodingKey {
        case id
        case ruleID = "rule_id"
        case ruleName = "rule_name"
        case stage
        case createdAt = "created_at"
        case request
        case response
    }
}

public struct DeveloperBreakpointMessagePayload: Codable, Equatable, Sendable {
    public var method: String
    public var url: String
    public var status: Int
    public var headers: [DeveloperHeaderPayload]
    public var body: String

    public init(method: String = "", url: String = "", status: Int = 0, headers: [DeveloperHeaderPayload] = [], body: String = "") {
        self.method = method
        self.url = url
        self.status = status
        self.headers = headers
        self.body = body
    }
}

public struct DeveloperBreakpointResolutionPayload: Codable, Equatable, Sendable {
    public var action: String
    public var request: DeveloperBreakpointMessagePayload?
    public var response: DeveloperBreakpointMessagePayload?

    public init(action: String = "continue", request: DeveloperBreakpointMessagePayload? = nil, response: DeveloperBreakpointMessagePayload? = nil) {
        self.action = action
        self.request = request
        self.response = response
    }
}

public let developerCaptureDisclosure = """
HTTPS body capture is opt-in and local. When enabled, ClambHook creates a local certificate authority for devices you explicitly trust, decrypts traffic routed through the configured HTTP proxy, stores bounded request and response body previews on this device, redacts configured sensitive headers, and exports captures only when you share them.
"""
