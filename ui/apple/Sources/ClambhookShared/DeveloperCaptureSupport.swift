import Foundation

public struct DeveloperStatusPayload: Codable, Equatable, Sendable {
    public var enabled: Bool
    public var mitmEnabled: Bool
    public var noCacheEnabled: Bool
    public var captureLimit: Int
    public var bodyLimitBytes: UInt64
    public var headerValueLimitBytes: Int
    public var caCertPath: String
    public var caFingerprintSHA256: String
    public var caNotBefore: String
    public var caNotAfter: String
    public var captureCount: Int

    enum CodingKeys: String, CodingKey {
        case enabled
        case mitmEnabled = "mitm_enabled"
        case noCacheEnabled = "no_cache_enabled"
        case captureLimit = "capture_limit"
        case bodyLimitBytes = "body_limit_bytes"
        case headerValueLimitBytes = "header_value_limit_bytes"
        case caCertPath = "ca_cert_path"
        case caFingerprintSHA256 = "ca_fingerprint_sha256"
        case caNotBefore = "ca_not_before"
        case caNotAfter = "ca_not_after"
        case captureCount = "capture_count"
    }

    public init(
        enabled: Bool = false,
        mitmEnabled: Bool = false,
        noCacheEnabled: Bool = false,
        captureLimit: Int = 0,
        bodyLimitBytes: UInt64 = 0,
        headerValueLimitBytes: Int = 0,
        caCertPath: String = "",
        caFingerprintSHA256: String = "",
        caNotBefore: String = "",
        caNotAfter: String = "",
        captureCount: Int = 0
    ) {
        self.enabled = enabled
        self.mitmEnabled = mitmEnabled
        self.noCacheEnabled = noCacheEnabled
        self.captureLimit = captureLimit
        self.bodyLimitBytes = bodyLimitBytes
        self.headerValueLimitBytes = headerValueLimitBytes
        self.caCertPath = caCertPath
        self.caFingerprintSHA256 = caFingerprintSHA256
        self.caNotBefore = caNotBefore
        self.caNotAfter = caNotAfter
        self.captureCount = captureCount
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.enabled = try container.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        self.mitmEnabled = try container.decodeIfPresent(Bool.self, forKey: .mitmEnabled) ?? false
        self.noCacheEnabled = try container.decodeIfPresent(Bool.self, forKey: .noCacheEnabled) ?? false
        self.captureLimit = try container.decodeIfPresent(Int.self, forKey: .captureLimit) ?? 0
        self.bodyLimitBytes = try container.decodeIfPresent(UInt64.self, forKey: .bodyLimitBytes) ?? 0
        self.headerValueLimitBytes = try container.decodeIfPresent(Int.self, forKey: .headerValueLimitBytes) ?? 0
        self.caCertPath = try container.decodeIfPresent(String.self, forKey: .caCertPath) ?? ""
        self.caFingerprintSHA256 = try container.decodeIfPresent(String.self, forKey: .caFingerprintSHA256) ?? ""
        self.caNotBefore = try container.decodeIfPresent(String.self, forKey: .caNotBefore) ?? ""
        self.caNotAfter = try container.decodeIfPresent(String.self, forKey: .caNotAfter) ?? ""
        self.captureCount = try container.decodeIfPresent(Int.self, forKey: .captureCount) ?? 0
    }
}

public let developerDefaultRedactHeaders = [
    "authorization",
    "proxy-authorization",
    "cookie",
    "set-cookie",
    "x-api-key",
    "api-key",
    "x-auth-token",
    "x-csrf-token",
    "x-xsrf-token",
    "csrf-token",
    "xsrf-token"
]

public let developerDefaultRedactQueryParams = [
    "token",
    "access_token",
    "refresh_token",
    "id_token",
    "api_key",
    "apikey",
    "key",
    "secret",
    "password",
    "passwd",
    "code",
    "session",
    "auth"
]

public struct DeveloperSettingsPayload: Codable, Equatable, Sendable {
    public var enabled: Bool
    public var mitmEnabled: Bool
    public var noCacheEnabled: Bool
    public var captureLimit: Int
    public var bodyLimitBytes: UInt64
    public var headerValueLimitBytes: Int
    public var redactHeaders: [String]
    public var redactQueryParams: [String]
    public var sslDecryptHosts: [String]
    public var backupPath: String

    enum CodingKeys: String, CodingKey {
        case enabled
        case mitmEnabled = "mitm_enabled"
        case noCacheEnabled = "no_cache_enabled"
        case captureLimit = "capture_limit"
        case bodyLimitBytes = "body_limit_bytes"
        case headerValueLimitBytes = "header_value_limit_bytes"
        case redactHeaders = "redact_headers"
        case redactQueryParams = "redact_query_params"
        case sslDecryptHosts = "ssl_decrypt_hosts"
        case backupPath = "backup_path"
    }

    public init(
        enabled: Bool = false,
        mitmEnabled: Bool = false,
        noCacheEnabled: Bool = false,
        captureLimit: Int = 200,
        bodyLimitBytes: UInt64 = 65_536,
        headerValueLimitBytes: Int = 8_192,
        redactHeaders: [String] = developerDefaultRedactHeaders,
        redactQueryParams: [String] = developerDefaultRedactQueryParams,
        sslDecryptHosts: [String] = [],
        backupPath: String = ""
    ) {
        self.enabled = enabled
        self.mitmEnabled = mitmEnabled
        self.noCacheEnabled = noCacheEnabled
        self.captureLimit = captureLimit
        self.bodyLimitBytes = bodyLimitBytes
        self.headerValueLimitBytes = headerValueLimitBytes
        self.redactHeaders = redactHeaders
        self.redactQueryParams = redactQueryParams
        self.sslDecryptHosts = sslDecryptHosts
        self.backupPath = backupPath
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.enabled = try container.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        self.mitmEnabled = try container.decodeIfPresent(Bool.self, forKey: .mitmEnabled) ?? false
        self.noCacheEnabled = try container.decodeIfPresent(Bool.self, forKey: .noCacheEnabled) ?? false
        self.captureLimit = try container.decodeIfPresent(Int.self, forKey: .captureLimit) ?? 200
        self.bodyLimitBytes = try container.decodeIfPresent(UInt64.self, forKey: .bodyLimitBytes) ?? 65_536
        self.headerValueLimitBytes = try container.decodeIfPresent(Int.self, forKey: .headerValueLimitBytes) ?? 8_192
        self.redactHeaders = try container.decodeIfPresent([String].self, forKey: .redactHeaders) ?? developerDefaultRedactHeaders
        self.redactQueryParams = try container.decodeIfPresent([String].self, forKey: .redactQueryParams) ?? developerDefaultRedactQueryParams
        self.sslDecryptHosts = try container.decodeIfPresent([String].self, forKey: .sslDecryptHosts) ?? []
        self.backupPath = try container.decodeIfPresent(String.self, forKey: .backupPath) ?? ""
    }
}

public struct DeveloperSettingsUpdateRequest: Codable, Equatable, Sendable {
    public var enabled: Bool?
    public var mitmEnabled: Bool?
    public var noCacheEnabled: Bool?
    public var captureLimit: Int?
    public var bodyLimitBytes: UInt64?
    public var headerValueLimitBytes: Int?
    public var redactHeaders: [String]?
    public var redactQueryParams: [String]?
    public var sslDecryptHosts: [String]?
    public var httpsCaptureAck: Bool

    enum CodingKeys: String, CodingKey {
        case enabled
        case mitmEnabled = "mitm_enabled"
        case noCacheEnabled = "no_cache_enabled"
        case captureLimit = "capture_limit"
        case bodyLimitBytes = "body_limit_bytes"
        case headerValueLimitBytes = "header_value_limit_bytes"
        case redactHeaders = "redact_headers"
        case redactQueryParams = "redact_query_params"
        case sslDecryptHosts = "ssl_decrypt_hosts"
        case httpsCaptureAck = "https_capture_ack"
    }

    public init(
        enabled: Bool? = nil,
        mitmEnabled: Bool? = nil,
        noCacheEnabled: Bool? = nil,
        captureLimit: Int? = nil,
        bodyLimitBytes: UInt64? = nil,
        headerValueLimitBytes: Int? = nil,
        redactHeaders: [String]? = nil,
        redactQueryParams: [String]? = nil,
        sslDecryptHosts: [String]? = nil,
        httpsCaptureAck: Bool = false
    ) {
        self.enabled = enabled
        self.mitmEnabled = mitmEnabled
        self.noCacheEnabled = noCacheEnabled
        self.captureLimit = captureLimit
        self.bodyLimitBytes = bodyLimitBytes
        self.headerValueLimitBytes = headerValueLimitBytes
        self.redactHeaders = redactHeaders
        self.redactQueryParams = redactQueryParams
        self.sslDecryptHosts = sslDecryptHosts
        self.httpsCaptureAck = httpsCaptureAck
    }
}

public struct DeveloperEntriesPayload: Codable, Equatable, Sendable {
    public var entries: [DeveloperEntryPayload]

    public init(entries: [DeveloperEntryPayload] = []) {
        self.entries = entries
    }
}

public struct DeveloperDecodedFramePayload: Codable, Equatable, Identifiable, Sendable {
    public let id = UUID()
    public var direction: String
    public var opcode: String
    public var preview: String
    public var truncated: Bool

    enum CodingKeys: String, CodingKey {
        case direction
        case opcode
        case preview
        case truncated
    }

    public init(direction: String = "", opcode: String = "", preview: String = "", truncated: Bool = false) {
        self.direction = direction
        self.opcode = opcode
        self.preview = preview
        self.truncated = truncated
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.direction = try container.decodeIfPresent(String.self, forKey: .direction) ?? ""
        self.opcode = try container.decodeIfPresent(String.self, forKey: .opcode) ?? ""
        self.preview = try container.decodeIfPresent(String.self, forKey: .preview) ?? ""
        self.truncated = try container.decodeIfPresent(Bool.self, forKey: .truncated) ?? false
    }

    public static func == (lhs: DeveloperDecodedFramePayload, rhs: DeveloperDecodedFramePayload) -> Bool {
        lhs.direction == rhs.direction
            && lhs.opcode == rhs.opcode
            && lhs.preview == rhs.preview
            && lhs.truncated == rhs.truncated
    }
}

public struct DeveloperDecodedPayload: Codable, Equatable, Sendable {
    public var kind: String
    public var frames: [DeveloperDecodedFramePayload]

    enum CodingKeys: String, CodingKey {
        case kind
        case frames
    }

    public init(kind: String = "", frames: [DeveloperDecodedFramePayload] = []) {
        self.kind = kind
        self.frames = frames
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.kind = try container.decodeIfPresent(String.self, forKey: .kind) ?? ""
        self.frames = try container.decodeIfPresent([DeveloperDecodedFramePayload].self, forKey: .frames) ?? []
    }
}

public struct DeveloperTimingsPayload: Codable, Equatable, Sendable {
    public var connect: Double
    public var ssl: Double
    public var send: Double
    public var wait: Double
    public var receive: Double

    enum CodingKeys: String, CodingKey {
        case connect
        case ssl
        case send
        case wait
        case receive
    }

    public init(connect: Double = 0, ssl: Double = 0, send: Double = 0, wait: Double = 0, receive: Double = 0) {
        self.connect = connect
        self.ssl = ssl
        self.send = send
        self.wait = wait
        self.receive = receive
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.connect = try container.decodeIfPresent(Double.self, forKey: .connect) ?? 0
        self.ssl = try container.decodeIfPresent(Double.self, forKey: .ssl) ?? 0
        self.send = try container.decodeIfPresent(Double.self, forKey: .send) ?? 0
        self.wait = try container.decodeIfPresent(Double.self, forKey: .wait) ?? 0
        self.receive = try container.decodeIfPresent(Double.self, forKey: .receive) ?? 0
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
    public var decoded: DeveloperDecodedPayload?
    public var timings: DeveloperTimingsPayload?

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
        case decoded
        case timings
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
        error: String = "",
        decoded: DeveloperDecodedPayload? = nil,
        timings: DeveloperTimingsPayload? = nil
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
        self.decoded = decoded
        self.timings = timings
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
        self.connID = try container.decodeIfPresent(String.self, forKey: .connID) ?? ""
        self.profile = try container.decodeIfPresent(String.self, forKey: .profile) ?? ""
        self.clientAddr = try container.decodeIfPresent(String.self, forKey: .clientAddr) ?? ""
        self.chainName = try container.decodeIfPresent(String.self, forKey: .chainName) ?? ""
        self.startedAt = try container.decodeIfPresent(String.self, forKey: .startedAt) ?? ""
        self.finishedAt = try container.decodeIfPresent(String.self, forKey: .finishedAt) ?? ""
        self.method = try container.decodeIfPresent(String.self, forKey: .method) ?? ""
        self.url = try container.decodeIfPresent(String.self, forKey: .url) ?? ""
        self.scheme = try container.decodeIfPresent(String.self, forKey: .scheme) ?? ""
        self.host = try container.decodeIfPresent(String.self, forKey: .host) ?? ""
        self.status = try container.decodeIfPresent(Int.self, forKey: .status) ?? 0
        self.request = try container.decodeIfPresent(DeveloperMessagePayload.self, forKey: .request) ?? DeveloperMessagePayload()
        self.response = try container.decodeIfPresent(DeveloperMessagePayload.self, forKey: .response) ?? DeveloperMessagePayload()
        self.error = try container.decodeIfPresent(String.self, forKey: .error) ?? ""
        self.decoded = try container.decodeIfPresent(DeveloperDecodedPayload.self, forKey: .decoded)
        self.timings = try container.decodeIfPresent(DeveloperTimingsPayload.self, forKey: .timings)
    }

    public var hasDecoded: Bool { (decoded?.frames.isEmpty == false) }
}

public struct DeveloperMessagePayload: Codable, Equatable, Sendable {
    public var headers: [DeveloperHeaderPayload]
    public var cookies: [DeveloperCookiePayload]
    public var body: DeveloperBodyPayload

    public init(headers: [DeveloperHeaderPayload] = [], cookies: [DeveloperCookiePayload] = [], body: DeveloperBodyPayload = DeveloperBodyPayload()) {
        self.headers = headers
        self.cookies = cookies
        self.body = body
    }

    enum CodingKeys: String, CodingKey {
        case headers
        case cookies
        case body
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.headers = try container.decodeIfPresent([DeveloperHeaderPayload].self, forKey: .headers) ?? []
        self.cookies = try container.decodeIfPresent([DeveloperCookiePayload].self, forKey: .cookies) ?? []
        self.body = try container.decodeIfPresent(DeveloperBodyPayload.self, forKey: .body) ?? DeveloperBodyPayload()
    }
}

public struct DeveloperHeaderPayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String { name }
    public var name: String
    public var value: String
    public var redacted: Bool
    public var truncated: Bool

    enum CodingKeys: String, CodingKey {
        case name
        case value
        case redacted
        case truncated
    }

    public init(name: String = "", value: String = "", redacted: Bool = false, truncated: Bool = false) {
        self.name = name
        self.value = value
        self.redacted = redacted
        self.truncated = truncated
    }

    // Tolerant decode: the daemon omits redacted/truncated (omitempty) for
    // normal headers and for parsed-cURL headers, so decode them as false when
    // absent rather than throwing.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        self.name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        self.value = try c.decodeIfPresent(String.self, forKey: .value) ?? ""
        self.redacted = try c.decodeIfPresent(Bool.self, forKey: .redacted) ?? false
        self.truncated = try c.decodeIfPresent(Bool.self, forKey: .truncated) ?? false
    }
}

public struct DeveloperBodyViewerPayload: Codable, Equatable, Sendable {
    public var kind: String
    public var pretty: String
    public var prettyTruncated: Bool
    public var hex: String

    enum CodingKeys: String, CodingKey {
        case kind
        case pretty
        case prettyTruncated = "pretty_truncated"
        case hex
    }

    public init(kind: String = "", pretty: String = "", prettyTruncated: Bool = false, hex: String = "") {
        self.kind = kind
        self.pretty = pretty
        self.prettyTruncated = prettyTruncated
        self.hex = hex
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.kind = try container.decodeIfPresent(String.self, forKey: .kind) ?? ""
        self.pretty = try container.decodeIfPresent(String.self, forKey: .pretty) ?? ""
        self.prettyTruncated = try container.decodeIfPresent(Bool.self, forKey: .prettyTruncated) ?? false
        self.hex = try container.decodeIfPresent(String.self, forKey: .hex) ?? ""
    }
}

public struct DeveloperBodyPayload: Codable, Equatable, Sendable {
    public var size: UInt64
    public var preview: String
    public var previewBase64: String
    public var previewBytes: UInt64
    public var truncated: Bool
    public var truncatedAfter: UInt64
    public var mimeType: String
    public var encoding: String
    public var viewer: DeveloperBodyViewerPayload?

    enum CodingKeys: String, CodingKey {
        case size
        case preview
        case previewBase64 = "preview_base64"
        case previewBytes = "preview_bytes"
        case truncated
        case truncatedAfter = "truncated_after"
        case mimeType = "mime_type"
        case encoding
        case viewer
    }

    // pi-lens-ignore: line_length
    public init(size: UInt64 = 0, preview: String = "", previewBase64: String = "", previewBytes: UInt64 = 0, truncated: Bool = false, truncatedAfter: UInt64 = 0, mimeType: String = "", encoding: String = "",
        viewer: DeveloperBodyViewerPayload? = nil
    ) {
        self.size = size
        self.preview = preview
        self.previewBase64 = previewBase64
        self.previewBytes = previewBytes
        self.truncated = truncated
        self.truncatedAfter = truncatedAfter
        self.mimeType = mimeType
        self.encoding = encoding
        self.viewer = viewer
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.size = try container.decodeIfPresent(UInt64.self, forKey: .size) ?? 0
        self.preview = try container.decodeIfPresent(String.self, forKey: .preview) ?? ""
        self.previewBase64 = try container.decodeIfPresent(String.self, forKey: .previewBase64) ?? ""
        self.previewBytes = try container.decodeIfPresent(UInt64.self, forKey: .previewBytes) ?? 0
        self.truncated = try container.decodeIfPresent(Bool.self, forKey: .truncated) ?? false
        self.truncatedAfter = try container.decodeIfPresent(UInt64.self, forKey: .truncatedAfter) ?? 0
        self.mimeType = try container.decodeIfPresent(String.self, forKey: .mimeType) ?? ""
        self.encoding = try container.decodeIfPresent(String.self, forKey: .encoding) ?? ""
        self.viewer = try container.decodeIfPresent(DeveloperBodyViewerPayload.self, forKey: .viewer)
    }

    public var hasPreview: Bool {
        !preview.isEmpty || previewBytes > 0 || size > 0
    }
}

public struct DeveloperCookiePayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String { name }
    public var name: String
    public var value: String
    public var redacted: Bool
    public var domain: String
    public var path: String
    public var expires: String
    public var maxAge: Int
    public var secure: Bool
    public var httpOnly: Bool
    public var sameSite: String

    enum CodingKeys: String, CodingKey {
        case name
        case value
        case redacted
        case domain
        case path
        case expires
        case maxAge = "max_age"
        case secure
        case httpOnly = "http_only"
        case sameSite = "same_site"
    }

    // pi-lens-ignore: line_length
    public init(name: String = "", value: String = "", redacted: Bool = false, domain: String = "", path: String = "", expires: String = "", maxAge: Int = 0, secure: Bool = false, httpOnly: Bool = false, sameSite: String = "") {
        self.name = name
        self.value = value
        self.redacted = redacted
        self.domain = domain
        self.path = path
        self.expires = expires
        self.maxAge = maxAge
        self.secure = secure
        self.httpOnly = httpOnly
        self.sameSite = sameSite
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.name = try container.decodeIfPresent(String.self, forKey: .name) ?? ""
        self.value = try container.decodeIfPresent(String.self, forKey: .value) ?? ""
        self.redacted = try container.decodeIfPresent(Bool.self, forKey: .redacted) ?? false
        self.domain = try container.decodeIfPresent(String.self, forKey: .domain) ?? ""
        self.path = try container.decodeIfPresent(String.self, forKey: .path) ?? ""
        self.expires = try container.decodeIfPresent(String.self, forKey: .expires) ?? ""
        self.maxAge = try container.decodeIfPresent(Int.self, forKey: .maxAge) ?? 0
        self.secure = try container.decodeIfPresent(Bool.self, forKey: .secure) ?? false
        self.httpOnly = try container.decodeIfPresent(Bool.self, forKey: .httpOnly) ?? false
        self.sameSite = try container.decodeIfPresent(String.self, forKey: .sameSite) ?? ""
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

    // pi-lens-ignore: line_length
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

public struct DeveloperRewriteOpPayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var target: String  // header|body|status
    public var action: String  // header: add|set|remove  body: set|replace  status: set
    public var field: String   // header name (target=header)
    public var value: String
    public var replace: String // body replace replacement

    enum CodingKeys: String, CodingKey {
        case id
        case target
        case action
        case field
        case value
        case replace
    }

    public init(id: String = UUID().uuidString, target: String = "header", action: String = "add", field: String = "", value: String = "", replace: String = "") {
        self.id = id
        self.target = target
        self.action = action
        self.field = field
        self.value = value
        self.replace = replace
    }
}

public struct DeveloperRewriteRulePayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var name: String
    public var enabled: Bool
    public var match: DeveloperMatchPayload
    public var stage: String  // request|response|both
    public var ops: [DeveloperRewriteOpPayload]

    enum CodingKeys: String, CodingKey {
        case id
        case name
        case enabled
        case match
        case stage
        case ops
    }

    public init(
        id: String = UUID().uuidString,
        name: String = "",
        enabled: Bool = true,
        match: DeveloperMatchPayload = DeveloperMatchPayload(),
        stage: String = "both",
        ops: [DeveloperRewriteOpPayload] = []
    ) {
        self.id = id
        self.name = name
        self.enabled = enabled
        self.match = match
        self.stage = stage
        self.ops = ops
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
    public var bodySet: Bool

    enum CodingKeys: String, CodingKey {
        case method
        case url
        case status
        case headers
        case body
        case bodySet = "body_set"
    }

    public init(method: String = "", url: String = "", status: Int = 0, headers: [DeveloperHeaderPayload] = [], body: String = "", bodySet: Bool = false) {
        self.method = method
        self.url = url
        self.status = status
        self.headers = headers
        self.body = body
        self.bodySet = bodySet
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.method = try container.decodeIfPresent(String.self, forKey: .method) ?? ""
        self.url = try container.decodeIfPresent(String.self, forKey: .url) ?? ""
        self.status = try container.decodeIfPresent(Int.self, forKey: .status) ?? 0
        self.headers = try container.decodeIfPresent([DeveloperHeaderPayload].self, forKey: .headers) ?? []
        self.body = try container.decodeIfPresent(String.self, forKey: .body) ?? ""
        self.bodySet = try container.decodeIfPresent(Bool.self, forKey: .bodySet) ?? false
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
// pi-lens-ignore: line_length
HTTP Capture is opt-in and local. When enabled, ClambHook stores bounded HTTP request and response previews on this Mac for traffic routed through the daemon HTTP proxy. Sensitive headers and configured query parameters are redacted before captures are stored.
"""

public let developerHTTPSCaptureDisclosure = """
// pi-lens-ignore: line_length
HTTPS capture is a separate opt-in. It creates a local certificate authority, requires you to trust that CA in your user keychain, and decrypts HTTPS traffic routed through the daemon HTTP proxy. Only enable it for devices and test traffic you control.
"""

public let developerSSLDecryptHostsDisclosure = """
// pi-lens-ignore: line_length
Leave blank to decrypt every HTTPS host. Enter comma-separated hostnames or wildcard patterns (for example example.com, *.example.com) to restrict decryption to matching hosts only; other hosts pass through as an opaque tunnel.
"""

public let developerHARExportDisclosure = """
HAR exports can include URLs, headers, cookies, and request or response body previews. Review the file before sharing it outside this Mac.
"""


// MARK: - Capture filter, cURL, and compose

public struct DeveloperEntriesFilter: Equatable, Sendable {
    public var method: String
    public var statusMin: Int
    public var statusMax: Int
    public var host: String
    public var scheme: String
    public var contentType: String
    public var query: String
    public var errorOnly: Bool

    public init(method: String = "", statusMin: Int = 0, statusMax: Int = 0, host: String = "", scheme: String = "", contentType: String = "", query: String = "", errorOnly: Bool = false) {
        self.method = method
        self.statusMin = statusMin
        self.statusMax = statusMax
        self.host = host
        self.scheme = scheme
        self.contentType = contentType
        self.query = query
        self.errorOnly = errorOnly
    }

    public var isEmpty: Bool {
        method.isEmpty && statusMin == 0 && statusMax == 0 && host.isEmpty && scheme.isEmpty && contentType.isEmpty && query.isEmpty && !errorOnly
    }

    /// entriesPath returns the /developer/entries path with this filter's query items.
    public func entriesPath() -> String {
        var items: [String] = ["limit=200"]
        if !method.isEmpty { items.append("method=" + percentEncode(method)) }
        if statusMin > 0 { items.append("status_min=\(statusMin)") }
        if statusMax > 0 { items.append("status_max=\(statusMax)") }
        if !host.isEmpty { items.append("host=" + percentEncode(host)) }
        if !scheme.isEmpty { items.append("scheme=" + percentEncode(scheme)) }
        if !contentType.isEmpty { items.append("content_type=" + percentEncode(contentType)) }
        if !query.isEmpty { items.append("q=" + percentEncode(query)) }
        if errorOnly { items.append("error_only=1") }
        return "/api/v1/developer/entries?" + items.joined(separator: "&")
    }

    private func percentEncode(_ value: String) -> String {
        value.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? value
    }
}

public struct CurlExportResponse: Codable, Equatable, Sendable {
    public var curl: String
    public init(curl: String = "") { self.curl = curl }
}

public struct CurlImportRequest: Codable, Equatable, Sendable {
    public var curl: String
    public init(curl: String = "") { self.curl = curl }
}

public struct ParsedCurlResponse: Codable, Equatable, Sendable {
    public var method: String
    public var url: String
    public var headers: [DeveloperHeaderPayload]
    public var body: String

    enum CodingKeys: String, CodingKey {
        case method
        case url
        case headers
        case body
    }

    public init(method: String = "GET", url: String = "", headers: [DeveloperHeaderPayload] = [], body: String = "") {
        self.method = method
        self.url = url
        self.headers = headers
        self.body = body
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.method = try container.decodeIfPresent(String.self, forKey: .method) ?? "GET"
        self.url = try container.decodeIfPresent(String.self, forKey: .url) ?? ""
        self.headers = try container.decodeIfPresent([DeveloperHeaderPayload].self, forKey: .headers) ?? []
        self.body = try container.decodeIfPresent(String.self, forKey: .body) ?? ""
    }
}

public struct ComposedRequestPayload: Codable, Equatable, Sendable {
    public var method: String
    public var url: String
    public var headers: [DeveloperHeaderPayload]
    public var body: String?

    enum CodingKeys: String, CodingKey {
        case method
        case url
        case headers
        case body
    }

    public init(method: String = "GET", url: String = "", headers: [DeveloperHeaderPayload] = [], body: String? = nil) {
        self.method = method
        self.url = url
        self.headers = headers
        self.body = body
    }
}
