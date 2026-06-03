import Foundation

public enum CaptureFilterKind: String, CaseIterable, Identifiable, Sendable {
    case all
    case http
    case https
    case metadataOnly

    public var id: Self { self }
}

public struct CaptureSnapshotPayload: Codable, Equatable, Sendable {
    public var version: Int
    public var generatedAt: Date
    public var entries: [CaptureEntryPayload]
    public var note: String

    enum CodingKeys: String, CodingKey {
        case version
        case generatedAt = "generated_at"
        case entries
        case note
    }

    public init(
        version: Int = 1,
        generatedAt: Date = Date(),
        entries: [CaptureEntryPayload] = [],
        note: String = CaptureSupport.captureNote
    ) {
        self.version = version
        self.generatedAt = generatedAt
        self.entries = entries
        self.note = note
    }
}

public struct CaptureEntryPayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var connectionID: String
    public var startedAtNs: Int64
    public var updatedAtNs: Int64
    public var state: String
    public var method: String
    public var scheme: String
    public var host: String
    public var port: String
    public var path: String
    public var statusCode: Int
    public var sslState: String
    public var ruleName: String
    public var ruleAction: String
    public var chainName: String
    public var requestBody: CaptureBodyPayload
    public var responseBody: CaptureBodyPayload
    public var rxTotal: UInt64
    public var txTotal: UInt64
    public var durationNs: Int64

    enum CodingKeys: String, CodingKey {
        case id
        case connectionID = "connection_id"
        case startedAtNs = "started_at_ns"
        case updatedAtNs = "updated_at_ns"
        case state
        case method
        case scheme
        case host
        case port
        case path
        case statusCode = "status_code"
        case sslState = "ssl_state"
        case ruleName = "rule_name"
        case ruleAction = "rule_action"
        case chainName = "chain_name"
        case requestBody = "request_body"
        case responseBody = "response_body"
        case rxTotal = "rx_total"
        case txTotal = "tx_total"
        case durationNs = "duration_ns"
    }

    public init(
        id: String = "",
        connectionID: String = "",
        startedAtNs: Int64 = 0,
        updatedAtNs: Int64 = 0,
        state: String = "",
        method: String = "",
        scheme: String = "",
        host: String = "",
        port: String = "",
        path: String = "",
        statusCode: Int = 0,
        sslState: String = "metadata_only",
        ruleName: String = "",
        ruleAction: String = "",
        chainName: String = "",
        requestBody: CaptureBodyPayload = CaptureBodyPayload(),
        responseBody: CaptureBodyPayload = CaptureBodyPayload(),
        rxTotal: UInt64 = 0,
        txTotal: UInt64 = 0,
        durationNs: Int64 = 0
    ) {
        self.id = id
        self.connectionID = connectionID
        self.startedAtNs = startedAtNs
        self.updatedAtNs = updatedAtNs
        self.state = state
        self.method = method
        self.scheme = scheme
        self.host = host
        self.port = port
        self.path = path
        self.statusCode = statusCode
        self.sslState = sslState
        self.ruleName = ruleName
        self.ruleAction = ruleAction
        self.chainName = chainName
        self.requestBody = requestBody
        self.responseBody = responseBody
        self.rxTotal = rxTotal
        self.txTotal = txTotal
        self.durationNs = durationNs
    }

    public var displayTarget: String {
        var value = host
        if !port.isEmpty {
            value += ":\(port)"
        }
        if !path.isEmpty {
            value += path
        }
        return value
    }

    public var hasBodyPreview: Bool {
        requestBody.available || responseBody.available
    }
}

public struct CaptureBodyPayload: Codable, Equatable, Sendable {
    public var available: Bool
    public var preview: String
    public var contentType: String
    public var byteCount: UInt64
    public var truncated: Bool
    public var reason: String

    enum CodingKeys: String, CodingKey {
        case available
        case preview
        case contentType = "content_type"
        case byteCount = "byte_count"
        case truncated
        case reason
    }

    public init(
        available: Bool = false,
        preview: String = "",
        contentType: String = "",
        byteCount: UInt64 = 0,
        truncated: Bool = false,
        reason: String = "Payload bodies are not captured in v1."
    ) {
        self.available = available
        self.preview = preview
        self.contentType = contentType
        self.byteCount = byteCount
        self.truncated = truncated
        self.reason = reason
    }
}

public enum CaptureSupport {
    public static let captureNote = "Metadata-only export. ClambHook v1 does not install a certificate authority, perform TLS MITM, store request or response bodies, or export HAR files. HTTPS entries contain CONNECT metadata only."

    public static func snapshot(
        traffic: TrafficSnapshotPayload,
        generatedAt: Date = Date()
    ) -> CaptureSnapshotPayload {
        CaptureSnapshotPayload(
            generatedAt: generatedAt,
            entries: captureEntries(from: traffic),
            note: captureNote
        )
    }

    public static func captureEntries(from traffic: TrafficSnapshotPayload) -> [CaptureEntryPayload] {
        traffic.connections
            .compactMap(captureEntry)
            .sorted { $0.updatedAtNs > $1.updatedAtNs }
    }

    public static func filteredEntries(
        _ entries: [CaptureEntryPayload],
        filter: CaptureFilterKind,
        query: String = ""
    ) -> [CaptureEntryPayload] {
        let normalizedQuery = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return entries.filter { entry in
            switch filter {
            case .all:
                break
            case .http:
                guard entry.scheme.lowercased() == "http" else { return false }
            case .https:
                guard entry.scheme.lowercased() == "https" else { return false }
            case .metadataOnly:
                guard !entry.hasBodyPreview else { return false }
            }
            guard !normalizedQuery.isEmpty else { return true }
            return [
                entry.method,
                entry.scheme,
                entry.host,
                entry.port,
                entry.path,
                entry.state,
                entry.sslState,
                entry.ruleName,
                entry.ruleAction,
                entry.chainName,
            ].contains { $0.lowercased().contains(normalizedQuery) }
        }
    }

    public static func exportString(
        traffic: TrafficSnapshotPayload,
        entries: [CaptureEntryPayload],
        generatedAt: Date = Date()
    ) -> String {
        let payload = CaptureSnapshotPayload(
            generatedAt: generatedAt,
            entries: entries,
            note: captureNote
        )
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        encoder.dateEncodingStrategy = .iso8601
        guard let data = try? encoder.encode(payload) else {
            return "{}"
        }
        return String(data: data, encoding: .utf8) ?? "{}"
    }

    private static func captureEntry(_ connection: TrafficConnectionPayload) -> CaptureEntryPayload? {
        guard let visibility = connection.visibility else {
            return nil
        }
        let kind = visibility.kind.lowercased()
        guard kind == "http" || kind == "http_connect" else {
            return nil
        }
        let scheme = visibility.scheme.isEmpty ? (kind == "http_connect" ? "https" : "http") : visibility.scheme
        let host = visibility.host.isEmpty ? connection.targetHost : visibility.host
        let path = kind == "http_connect" ? "" : visibility.path
        return CaptureEntryPayload(
            id: connection.connID,
            connectionID: connection.connID,
            startedAtNs: connection.startTsNs,
            updatedAtNs: connection.updatedTsNs,
            state: connection.state,
            method: visibility.method.isEmpty ? (kind == "http_connect" ? "CONNECT" : "") : visibility.method,
            scheme: scheme,
            host: host,
            port: visibility.port.isEmpty ? connection.targetPort : visibility.port,
            path: path,
            statusCode: 0,
            sslState: kind == "http_connect" ? "metadata_only" : "not_tls",
            ruleName: connection.ruleName,
            ruleAction: connection.ruleAction,
            chainName: connection.chainName,
            requestBody: CaptureBodyPayload(reason: bodyUnavailableReason(kind: kind)),
            responseBody: CaptureBodyPayload(reason: bodyUnavailableReason(kind: kind)),
            rxTotal: connection.rxTotal,
            txTotal: connection.txTotal,
            durationNs: connection.durationNs
        )
    }

    private static func bodyUnavailableReason(kind: String) -> String {
        if kind == "http_connect" {
            return "HTTPS payload bodies are not captured in v1; only CONNECT metadata is recorded."
        }
        return "HTTP payload bodies are not captured in v1; only request metadata is recorded."
    }
}
