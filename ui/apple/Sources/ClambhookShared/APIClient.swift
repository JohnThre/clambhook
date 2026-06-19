import Foundation

public protocol ClambhookAPIProviding: AnyObject {
    func status() async throws -> StatusPayload
    func profiles() async throws -> ProfilesPayload
    func servers() async throws -> ServersPayload
    func policyGroups() async throws -> PolicyGroupsPayload
    func rules() async throws -> RulesPayload
    func dns() async throws -> DNSPayload
    func testRule(network: String, target: String, profile: String) async throws -> RuleTestResponse
    func traffic() async throws -> TrafficSnapshotPayload
    func connect() async throws
    func disconnect() async throws
    func setActiveProfile(_ name: String) async throws
    func selectPolicyGroup(profile: String, group: String, chain: String) async throws -> PolicyGroupsPayload
}

public protocol ClambhookRuleEditing: AnyObject {
    func replaceRules(_ rules: [RulePayload], profile: String) async throws -> RulesPayload
}

public protocol DeveloperCaptureProviding: AnyObject {
    func developerStatus() async throws -> DeveloperStatusPayload
    func developerEntries() async throws -> DeveloperEntriesPayload
    func developerCAPEM() async throws -> String
    func developerHAR() async throws -> String
    func repeatDeveloperEntry(_ request: DeveloperRepeatRequestPayload) async throws -> DeveloperRepeatResponsePayload
    func developerMapRules() async throws -> DeveloperRuleListPayload<DeveloperMapRulePayload>
    func replaceDeveloperMapRules(_ rules: [DeveloperMapRulePayload]) async throws
    func developerBreakpointRules() async throws -> DeveloperRuleListPayload<DeveloperBreakpointRulePayload>
    func replaceDeveloperBreakpointRules(_ rules: [DeveloperBreakpointRulePayload]) async throws
    func developerPendingBreakpoints() async throws -> DeveloperPendingBreakpointsPayload
    func resolveDeveloperBreakpoint(id: String, resolution: DeveloperBreakpointResolutionPayload) async throws
    func clearDeveloperEntries() async throws
}

public protocol ClambhookDashboardProviding: ClambhookAPIProviding {
    func dashboard() async throws -> TunnelDashboardPayload
}

public enum APIClientError: Error, LocalizedError, Equatable {
    case invalidURL(String)
    case httpStatus(Int, String)
    case unsupportedWebSocketMessage

    public var errorDescription: String? {
        switch self {
        case .invalidURL(let value):
            return "invalid URL: \(value)"
        case .httpStatus(let status, let body):
            if body.isEmpty {
                return "\(status)"
            }
            return "\(status): \(body)"
        case .unsupportedWebSocketMessage:
            return "unsupported WebSocket message"
        }
    }
}

public final class ClambhookAPIClient: ClambhookAPIProviding, ClambhookRuleEditing, DeveloperCaptureProviding {
    private let baseURL: URL
    private let tokenProvider: () -> String?
    private let session: URLSession
    private let decoder = JSONDecoder()
    private let encoder = JSONEncoder()

    public init(
        baseURL: URL,
        tokenProvider: @escaping () -> String? = { nil },
        session: URLSession = .shared
    ) {
        self.baseURL = URL(string: baseURL.absoluteString.trimmingCharacters(in: CharacterSet(charactersIn: "/"))) ?? baseURL
        self.tokenProvider = tokenProvider
        self.session = session
        self.decoder.dateDecodingStrategy = .iso8601
    }

    public func status() async throws -> StatusPayload {
        try await getJSON("/api/v1/status")
    }

    public func profiles() async throws -> ProfilesPayload {
        try await getJSON("/api/v1/profiles")
    }

    public func servers() async throws -> ServersPayload {
        try await getJSON("/api/v1/servers")
    }

    public func policyGroups() async throws -> PolicyGroupsPayload {
        try await getJSON("/api/v1/policy-groups")
    }

    public func rules() async throws -> RulesPayload {
        try await getJSON("/api/v1/rules")
    }

    public func dns() async throws -> DNSPayload {
        try await getJSON("/api/v1/dns")
    }

    public func configSettings(profile: String = "") async throws -> ConfigSettingsPayload {
        if profile.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return try await getJSON("/api/v1/config/settings")
        }
        var components = URLComponents()
        components.path = "/api/v1/config/settings"
        components.queryItems = [URLQueryItem(name: "profile", value: profile)]
        return try await getJSON(components.string ?? "/api/v1/config/settings")
    }

    public func updateConfigSettings(_ request: ConfigSettingsUpdateRequest) async throws -> ConfigSettingsPayload {
        let body = try encoder.encode(request)
        let data = try await send(method: "PUT", path: "/api/v1/config/settings", body: body)
        return try decoder.decode(ConfigSettingsPayload.self, from: data)
    }

    public func ruleSets() async throws -> RuleSetsPayload {
        try await getJSON("/api/v1/rule-sets")
    }

    public func traffic() async throws -> TrafficSnapshotPayload {
        try await getJSON("/api/v1/traffic?limit=200")
    }

    public func createRule(_ rule: RulePayload) async throws -> RulesPayload {
        struct CreateRuleRequest: Encodable {
            var rule: RulePayload
            var position: String
        }
        let body = try encoder.encode(CreateRuleRequest(rule: rule, position: "append"))
        let data = try await send(method: "POST", path: "/api/v1/rules", body: body)
        return try decoder.decode(RulesPayload.self, from: data)
    }

    public func createRuleFromConnection(connID: String, profile: String = "", name: String = "", action: String = "", scope: String = "auto") async throws -> RulesPayload {
        struct CreateRuleFromConnectionRequest: Encodable {
            var connID: String
            var profile: String
            var name: String
            var action: String
            var scope: String
            var position: String

            enum CodingKeys: String, CodingKey {
                case connID = "conn_id"
                case profile
                case name
                case action
                case scope
                case position
            }
        }
        let body = try encoder.encode(CreateRuleFromConnectionRequest(
            connID: connID,
            profile: profile,
            name: name,
            action: action,
            scope: scope,
            position: "append"
        ))
        let data = try await send(method: "POST", path: "/api/v1/rules/from-connection", body: body)
        return try decoder.decode(RulesPayload.self, from: data)
    }

    public func createTemporaryRuleFromConnection(connID: String, profile: String = "", name: String = "", action: String = "", scope: String = "auto", ttlSeconds: Int = 900) async throws -> TemporaryRuleCreateResponsePayload {
        struct CreateTemporaryRuleFromConnectionRequest: Encodable {
            var connID: String
            var profile: String
            var name: String
            var action: String
            var scope: String
            var ttlSeconds: Int

            enum CodingKeys: String, CodingKey {
                case connID = "conn_id"
                case profile
                case name
                case action
                case scope
                case ttlSeconds = "ttl_seconds"
            }
        }
        let body = try encoder.encode(CreateTemporaryRuleFromConnectionRequest(
            connID: connID,
            profile: profile,
            name: name,
            action: action,
            scope: scope,
            ttlSeconds: ttlSeconds
        ))
        let data = try await send(method: "POST", path: "/api/v1/rules/temporary/from-connection", body: body)
        return try decoder.decode(TemporaryRuleCreateResponsePayload.self, from: data)
    }

    public func cleanupRule(_ suggestion: TrafficCleanupSuggestionPayload) async throws -> RulesPayload {
        struct CleanupRuleRequest: Encodable {
            var profile: String
            var kind: String
            var ruleName: String
            var targetRuleName: String
            var operation: String

            enum CodingKeys: String, CodingKey {
                case profile
                case kind
                case ruleName = "rule_name"
                case targetRuleName = "target_rule_name"
                case operation
            }
        }
        let target = suggestion.targetRuleName.isEmpty ? suggestion.ruleName : suggestion.targetRuleName
        let body = try encoder.encode(CleanupRuleRequest(
            profile: suggestion.profile,
            kind: suggestion.kind,
            ruleName: suggestion.ruleName,
            targetRuleName: target,
            operation: suggestion.operation
        ))
        let data = try await send(method: "POST", path: "/api/v1/rules/cleanup", body: body)
        return try decoder.decode(RulesPayload.self, from: data)
    }

    public func replaceRules(_ rules: [RulePayload], profile: String = "") async throws -> RulesPayload {
        struct ReplaceRulesRequest: Encodable {
            var profile: String
            var rules: [RulePayload]
        }
        let body = try encoder.encode(ReplaceRulesRequest(profile: profile, rules: rules))
        let data = try await send(method: "PUT", path: "/api/v1/rules", body: body)
        return try decoder.decode(RulesPayload.self, from: data)
    }

    public func replaceRuleSets(_ ruleSets: [RuleSetPayload], profile: String = "") async throws -> RuleSetsPayload {
        struct ReplaceRuleSetsRequest: Encodable {
            var profile: String
            var ruleSets: [RuleSetPayload]

            enum CodingKeys: String, CodingKey {
                case profile
                case ruleSets = "rule_sets"
            }
        }
        let body = try encoder.encode(ReplaceRuleSetsRequest(profile: profile, ruleSets: ruleSets))
        let data = try await send(method: "PUT", path: "/api/v1/rule-sets", body: body)
        return try decoder.decode(RuleSetsPayload.self, from: data)
    }

    public func replacePolicyGroups(_ groups: [PolicyGroupPayload], profile: String = "") async throws -> PolicyGroupsPayload {
        struct ReplacePolicyGroupsRequest: Encodable {
            var profile: String
            var policyGroups: [PolicyGroupPayload]

            enum CodingKeys: String, CodingKey {
                case profile
                case policyGroups = "policy_groups"
            }
        }
        struct ReplacePolicyGroupsResponse: Decodable {
            var profile: String
            var groups: [PolicyGroupPayload]

            enum CodingKeys: String, CodingKey {
                case profile
                case groups = "policy_groups"
            }
        }
        let body = try encoder.encode(ReplacePolicyGroupsRequest(profile: profile, policyGroups: groups))
        let data = try await send(method: "PUT", path: "/api/v1/policy-groups", body: body)
        let decoded = try decoder.decode(ReplacePolicyGroupsResponse.self, from: data)
        return PolicyGroupsPayload(profile: decoded.profile, groups: decoded.groups)
    }

    public func replaceRuleSubscriptions(_ subscriptions: [RuleSubscriptionPayload], profile: String = "") async throws -> RuleSubscriptionsPayload {
        struct ReplaceRuleSubscriptionsRequest: Encodable {
            var profile: String
            var subscriptions: [RuleSubscriptionPayload]
        }
        struct ReplaceRuleSubscriptionsResponse: Decodable {
            var profile: String
            var subscriptions: [RuleSubscriptionPayload]
        }
        let body = try encoder.encode(ReplaceRuleSubscriptionsRequest(profile: profile, subscriptions: subscriptions))
        let data = try await send(method: "PUT", path: "/api/v1/rule-subscriptions", body: body)
        let decoded = try decoder.decode(ReplaceRuleSubscriptionsResponse.self, from: data)
        return RuleSubscriptionsPayload(profile: decoded.profile, subscriptions: decoded.subscriptions)
    }

    public func refreshRuleSets(names: [String] = [], profile: String = "") async throws -> RuleSetsPayload {
        struct RefreshRuleSetsRequest: Encodable {
            var profile: String
            var names: [String]
        }
        let body = try encoder.encode(RefreshRuleSetsRequest(profile: profile, names: names))
        let data = try await send(method: "POST", path: "/api/v1/rule-sets/refresh", body: body)
        return try decoder.decode(RuleSetsPayload.self, from: data)
    }

    public func ruleSubscriptions(profile: String = "") async throws -> RuleSubscriptionsPayload {
        let query = profile.isEmpty ? "" : "?profile=\(profile.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? profile)"
        return try await getJSON("/api/v1/rule-subscriptions\(query)")
    }

    public func refreshRuleSubscriptions(names: [String] = [], profile: String = "") async throws -> RuleSubscriptionsPayload {
        struct RefreshRuleSubscriptionsRequest: Encodable {
            var profile: String
            var names: [String]
        }
        let body = try encoder.encode(RefreshRuleSubscriptionsRequest(profile: profile, names: names))
        let data = try await send(method: "POST", path: "/api/v1/rule-subscriptions/refresh", body: body)
        return try decoder.decode(RuleSubscriptionsPayload.self, from: data)
    }

    public func developerStatus() async throws -> DeveloperStatusPayload {
        try await getJSON("/api/v1/developer/status")
    }

    public func developerEntries() async throws -> DeveloperEntriesPayload {
        try await getJSON("/api/v1/developer/entries?limit=200")
    }

    public func developerCAPEM() async throws -> String {
        let data = try await send(method: "GET", path: "/api/v1/developer/ca.pem")
        return String(data: data, encoding: .utf8) ?? ""
    }

    public func developerHAR() async throws -> String {
        let data = try await send(method: "GET", path: "/api/v1/developer/har")
        return String(data: data, encoding: .utf8) ?? "{}"
    }

    public func repeatDeveloperEntry(_ request: DeveloperRepeatRequestPayload) async throws -> DeveloperRepeatResponsePayload {
        let body = try encoder.encode(request)
        let data = try await send(method: "POST", path: "/api/v1/developer/repeat", body: body)
        return try decoder.decode(DeveloperRepeatResponsePayload.self, from: data)
    }

    public func developerMapRules() async throws -> DeveloperRuleListPayload<DeveloperMapRulePayload> {
        try await getJSON("/api/v1/developer/map-rules")
    }

    public func replaceDeveloperMapRules(_ rules: [DeveloperMapRulePayload]) async throws {
        struct Request: Encodable {
            var rules: [DeveloperMapRulePayload]
        }
        let body = try encoder.encode(Request(rules: rules))
        _ = try await send(method: "PUT", path: "/api/v1/developer/map-rules", body: body)
    }

    public func developerBreakpointRules() async throws -> DeveloperRuleListPayload<DeveloperBreakpointRulePayload> {
        try await getJSON("/api/v1/developer/breakpoint-rules")
    }

    public func replaceDeveloperBreakpointRules(_ rules: [DeveloperBreakpointRulePayload]) async throws {
        struct Request: Encodable {
            var rules: [DeveloperBreakpointRulePayload]
        }
        let body = try encoder.encode(Request(rules: rules))
        _ = try await send(method: "PUT", path: "/api/v1/developer/breakpoint-rules", body: body)
    }

    public func developerPendingBreakpoints() async throws -> DeveloperPendingBreakpointsPayload {
        try await getJSON("/api/v1/developer/breakpoints/pending")
    }

    public func resolveDeveloperBreakpoint(id: String, resolution: DeveloperBreakpointResolutionPayload) async throws {
        let body = try encoder.encode(resolution)
        _ = try await send(method: "POST", path: "/api/v1/developer/breakpoints/\(id)/resolve", body: body)
    }

    public func clearDeveloperEntries() async throws {
        _ = try await send(method: "DELETE", path: "/api/v1/developer/entries")
    }

    public func testRule(network: String, target: String, profile: String = "") async throws -> RuleTestResponse {
        let body = try encoder.encode(RuleTestRequest(profile: profile, network: network, target: target))
        let data = try await send(method: "POST", path: "/api/v1/rules/test", body: body)
        return try decoder.decode(RuleTestResponse.self, from: data)
    }

    public func explainRoute(network: String, target: String, source: String = "", profile: String = "") async throws -> RuleTestResponse {
        let body = try encoder.encode(RuleTestRequest(profile: profile, network: network, target: target, source: source))
        let data = try await send(method: "POST", path: "/api/v1/routes/explain", body: body)
        return try decoder.decode(RuleTestResponse.self, from: data)
    }

    public func selectPolicyGroup(profile: String = "", group: String, chain: String) async throws -> PolicyGroupsPayload {
        struct SelectPolicyGroupRequest: Encodable {
            var profile: String
            var group: String
            var chain: String
        }
        struct SelectPolicyGroupResponse: Decodable {
            var profile: String
            var groups: [PolicyGroupPayload]
            var policyGroups: PolicyGroupsPayload

            enum CodingKeys: String, CodingKey {
                case profile
                case groups
                case policyGroups = "policy_groups"
            }

            init(from decoder: Decoder) throws {
                let container = try decoder.container(keyedBy: CodingKeys.self)
                self.profile = try container.decodeIfPresent(String.self, forKey: .profile) ?? ""
                self.groups = try container.decodeIfPresent([PolicyGroupPayload].self, forKey: .groups) ?? []
                self.policyGroups = try container.decodeIfPresent(PolicyGroupsPayload.self, forKey: .policyGroups) ?? PolicyGroupsPayload(profile: profile, groups: groups)
            }
        }
        let body = try encoder.encode(SelectPolicyGroupRequest(profile: profile, group: group, chain: chain))
        let data = try await send(method: "PUT", path: "/api/v1/policy-groups/selection", body: body)
        return try decoder.decode(SelectPolicyGroupResponse.self, from: data).policyGroups
    }

    public func connect() async throws {
        _ = try await send(method: "POST", path: "/api/v1/connect")
    }

    public func disconnect() async throws {
        _ = try await send(method: "POST", path: "/api/v1/disconnect")
    }

    public func setActiveProfile(_ name: String) async throws {
        let body = try encoder.encode(["name": name])
        _ = try await send(method: "PUT", path: "/api/v1/profiles/active", body: body)
    }

    public func eventsURL() -> URL {
        var components = URLComponents(url: baseURL, resolvingAgainstBaseURL: false)!
        components.scheme = components.scheme == "https" ? "wss" : "ws"
        components.path = "/api/v1/events"
        let prefix = components.string ?? "ws://127.0.0.1:9090/api/v1/events"
        return URL(string: prefix + "?types=connection.*,rule.*,hop.*,log.*")!
    }

    public func eventStream() -> AsyncThrowingStream<DaemonEvent, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                var request = URLRequest(url: eventsURL())
                if let token = tokenProvider(), !token.isEmpty {
                    request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
                }
                let socket = session.webSocketTask(with: request)
                socket.resume()
                do {
                    while !Task.isCancelled {
                        let message = try await socket.receive()
                        let data: Data
                        switch message {
                        case .data(let value):
                            data = value
                        case .string(let value):
                            data = Data(value.utf8)
                        @unknown default:
                            throw APIClientError.unsupportedWebSocketMessage
                        }
                        continuation.yield(try decoder.decode(DaemonEvent.self, from: data))
                    }
                    socket.cancel(with: .normalClosure, reason: nil)
                    continuation.finish()
                } catch {
                    socket.cancel(with: .goingAway, reason: nil)
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    private func getJSON<T: Decodable>(_ path: String) async throws -> T {
        let data = try await send(method: "GET", path: path)
        return try decoder.decode(T.self, from: data)
    }

    private func send(method: String, path: String, body: Data? = nil) async throws -> Data {
        guard let url = URL(string: path, relativeTo: baseURL)?.absoluteURL else {
            throw APIClientError.invalidURL(path)
        }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.timeoutInterval = 5
        if let token = tokenProvider(), !token.isEmpty {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        if let body {
            request.httpBody = body
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        if method == "DELETE" {
            request.httpBody = body
        }
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else {
            return data
        }
        guard (200...299).contains(http.statusCode) else {
            let bodyText = String(data: data.prefix(1024), encoding: .utf8)?
                .trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            throw APIClientError.httpStatus(http.statusCode, bodyText)
        }
        return data
    }
}
