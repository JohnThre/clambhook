import Foundation

/// A connection paused by the daemon's Little Snitch-style interactive prompt,
/// awaiting an allow/block decision. Mirrors the daemon `prompt.Pending` JSON
/// returned by `GET /api/v1/prompts/pending`.
public struct PendingPromptPayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String
    public var connID: String
    public var profile: String
    public var network: String
    public var target: String
    public var targetHost: String
    public var targetPort: String
    public var pid: Int
    public var processName: String
    public var processPath: String
    public var createdAt: Date
    public var waiters: Int

    public init(
        id: String = "",
        connID: String = "",
        profile: String = "",
        network: String = "",
        target: String = "",
        targetHost: String = "",
        targetPort: String = "",
        pid: Int = 0,
        processName: String = "",
        processPath: String = "",
        createdAt: Date = Date(),
        waiters: Int = 0
    ) {
        self.id = id
        self.connID = connID
        self.profile = profile
        self.network = network
        self.target = target
        self.targetHost = targetHost
        self.targetPort = targetPort
        self.pid = pid
        self.processName = processName
        self.processPath = processPath
        self.createdAt = createdAt
        self.waiters = waiters
    }

    enum CodingKeys: String, CodingKey {
        case id
        case connID = "conn_id"
        case profile
        case network
        case target
        case targetHost = "target_host"
        case targetPort = "target_port"
        case pid
        case processName = "process_name"
        case processPath = "process_path"
        case createdAt = "created_at"
        case waiters
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
        connID = try container.decodeIfPresent(String.self, forKey: .connID) ?? ""
        profile = try container.decodeIfPresent(String.self, forKey: .profile) ?? ""
        network = try container.decodeIfPresent(String.self, forKey: .network) ?? ""
        target = try container.decodeIfPresent(String.self, forKey: .target) ?? ""
        targetHost = try container.decodeIfPresent(String.self, forKey: .targetHost) ?? ""
        targetPort = try container.decodeIfPresent(String.self, forKey: .targetPort) ?? ""
        pid = try container.decodeIfPresent(Int.self, forKey: .pid) ?? 0
        processName = try container.decodeIfPresent(String.self, forKey: .processName) ?? ""
        processPath = try container.decodeIfPresent(String.self, forKey: .processPath) ?? ""
        // The daemon emits `created_at` as RFC3339, which may carry fractional
        // seconds (time.Time marshals as RFC3339Nano). Decode leniently so the
        // response never fails to parse regardless of the top-level date
        // strategy; fall back to "now" when absent or unparseable.
        if let raw = try container.decodeIfPresent(String.self, forKey: .createdAt) {
            createdAt = PendingPromptPayload.parseTimestamp(raw) ?? Date()
        } else {
            createdAt = Date()
        }
        waiters = try container.decodeIfPresent(Int.self, forKey: .waiters) ?? 0
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encode(connID, forKey: .connID)
        try container.encode(profile, forKey: .profile)
        try container.encode(network, forKey: .network)
        try container.encode(target, forKey: .target)
        try container.encode(targetHost, forKey: .targetHost)
        try container.encode(targetPort, forKey: .targetPort)
        try container.encode(pid, forKey: .pid)
        try container.encode(processName, forKey: .processName)
        try container.encode(processPath, forKey: .processPath)
        try container.encode(
            PendingPromptPayload.fractionalFormatter.string(from: createdAt),
            forKey: .createdAt
        )
        try container.encode(waiters, forKey: .waiters)
    }

    /// A human-facing label for the requesting process, falling back to the PID
    /// or a generic string when the daemon could not attribute the connection.
    public var processLabel: String {
        if !processName.isEmpty { return processName }
        if pid > 0 { return "PID \(pid)" }
        return "Unknown process"
    }

    private static let fractionalFormatter: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()

    private static let plainFormatter: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime]
        return formatter
    }()

    static func parseTimestamp(_ value: String) -> Date? {
        fractionalFormatter.date(from: value) ?? plainFormatter.date(from: value)
    }
}

/// Response envelope for `GET /api/v1/prompts/pending`.
public struct PendingPromptsPayload: Codable, Equatable, Sendable {
    public var prompts: [PendingPromptPayload]

    public init(prompts: [PendingPromptPayload] = []) {
        self.prompts = prompts
    }

    enum CodingKeys: String, CodingKey {
        case prompts
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        prompts = try container.decodeIfPresent([PendingPromptPayload].self, forKey: .prompts) ?? []
    }
}

/// The decision applied to a pending prompt.
public enum PromptDecisionAction: String, Codable, Sendable, CaseIterable {
    case allow
    case block
}

/// How long a prompt decision is remembered.
public enum PromptDecisionScope: String, Codable, Sendable, CaseIterable {
    /// Applies to this connection only; no rule is created.
    case once
    /// Creates a temporary rule for the session.
    case session
    /// Persists a rule to the active profile.
    case forever
}

/// Request body for `POST /api/v1/prompts/{id}/resolve`.
public struct ResolvePromptRequest: Codable, Equatable, Sendable {
    public var action: String
    public var scope: String
    public var matchHost: Bool
    public var ttlSeconds: Int64

    public init(
        action: PromptDecisionAction,
        scope: PromptDecisionScope = .once,
        matchHost: Bool = false,
        ttlSeconds: Int64 = 0
    ) {
        self.action = action.rawValue
        self.scope = scope.rawValue
        self.matchHost = matchHost
        self.ttlSeconds = ttlSeconds
    }

    enum CodingKeys: String, CodingKey {
        case action
        case scope
        case matchHost = "match_host"
        case ttlSeconds = "ttl_seconds"
    }
}
