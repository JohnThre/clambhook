import Foundation

public protocol ClambhookAPIProviding: AnyObject {
    func status() async throws -> StatusPayload
    func profiles() async throws -> ProfilesPayload
    func servers() async throws -> ServersPayload
    func rules() async throws -> RulesPayload
    func testRule(network: String, target: String, profile: String) async throws -> RuleTestResponse
    func traffic() async throws -> TrafficSnapshotPayload
    func connect() async throws
    func disconnect() async throws
    func setActiveProfile(_ name: String) async throws
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

public final class ClambhookAPIClient: ClambhookAPIProviding {
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

    public func rules() async throws -> RulesPayload {
        try await getJSON("/api/v1/rules")
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

    public func testRule(network: String, target: String, profile: String = "") async throws -> RuleTestResponse {
        let body = try encoder.encode(RuleTestRequest(profile: profile, network: network, target: target))
        let data = try await send(method: "POST", path: "/api/v1/rules/test", body: body)
        return try decoder.decode(RuleTestResponse.self, from: data)
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
