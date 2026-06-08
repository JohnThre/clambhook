import XCTest
@testable import ClambhookShared

final class APIClientTests: XCTestCase {
    override func tearDown() {
        MockURLProtocol.reset()
        super.tearDown()
    }

    func testStatusRequestUsesBearerTokenAndDecodesPayload() async throws {
        MockURLProtocol.responseData = Data("""
        {"running":true,"profile":"A","listeners":[{"protocol":"socks5","addr":"127.0.0.1:1080","active_conns":2}]}
        """.utf8)
        let client = ClambhookAPIClient(
            baseURL: URL(string: "http://127.0.0.1:9090")!,
            tokenProvider: { "secret-token" },
            session: mockSession()
        )

        let status = try await client.status()

        XCTAssertEqual(status.profile, "A")
        XCTAssertTrue(status.running)
        XCTAssertEqual(status.listeners.first?.activeConns, 2)
        XCTAssertEqual(MockURLProtocol.lastRequest?.url?.absoluteString, "http://127.0.0.1:9090/api/v1/status")
        XCTAssertEqual(MockURLProtocol.lastRequest?.value(forHTTPHeaderField: "Authorization"), "Bearer secret-token")
    }

    func testSetActiveProfileSendsJSONBody() async throws {
        MockURLProtocol.responseData = Data("{}".utf8)
        let client = ClambhookAPIClient(
            baseURL: URL(string: "http://localhost:9090/")!,
            tokenProvider: { nil },
            session: mockSession()
        )

        try await client.setActiveProfile("B")

        XCTAssertEqual(MockURLProtocol.lastRequest?.httpMethod, "PUT")
        XCTAssertEqual(MockURLProtocol.lastRequest?.url?.absoluteString, "http://localhost:9090/api/v1/profiles/active")
        XCTAssertEqual(MockURLProtocol.lastRequest?.value(forHTTPHeaderField: "Content-Type"), "application/json")
        let body = try XCTUnwrap(MockURLProtocol.lastBody)
        let decoded = try JSONDecoder().decode([String: String].self, from: body)
        XCTAssertEqual(decoded, ["name": "B"])
    }

    func testEventsURLUsesWebSocketSchemeAndFiltersConnectionAndLogEvents() {
        let httpClient = ClambhookAPIClient(baseURL: URL(string: "http://127.0.0.1:9090")!)
        XCTAssertEqual(
            httpClient.eventsURL().absoluteString,
            "ws://127.0.0.1:9090/api/v1/events?types=connection.*,rule.*,hop.*,log.*"
        )

        let httpsClient = ClambhookAPIClient(baseURL: URL(string: "https://proxy.example.test")!)
        XCTAssertEqual(
            httpsClient.eventsURL().absoluteString,
            "wss://proxy.example.test/api/v1/events?types=connection.*,rule.*,hop.*,log.*"
        )
    }

    func testCreateRuleSendsAppendRequest() async throws {
        MockURLProtocol.responseData = Data("""
        {"profile":"Work","rules":[{"name":"monitor-example-com","action":"direct","domains":["example.com"]}]}
        """.utf8)
        let client = ClambhookAPIClient(
            baseURL: URL(string: "http://127.0.0.1:9090")!,
            tokenProvider: { "secret-token" },
            session: mockSession()
        )

        let response = try await client.createRule(RulePayload(
            name: "monitor-example-com",
            action: "direct",
            domains: ["example.com"]
        ))

        XCTAssertEqual(response.profile, "Work")
        XCTAssertEqual(response.rules.first?.name, "monitor-example-com")
        XCTAssertEqual(MockURLProtocol.lastRequest?.httpMethod, "POST")
        XCTAssertEqual(MockURLProtocol.lastRequest?.url?.absoluteString, "http://127.0.0.1:9090/api/v1/rules")
        XCTAssertEqual(MockURLProtocol.lastRequest?.value(forHTTPHeaderField: "Authorization"), "Bearer secret-token")
        let body = try XCTUnwrap(MockURLProtocol.lastBody)
        let decoded = try JSONDecoder().decode(CreateRuleRequestBody.self, from: body)
        XCTAssertEqual(decoded.position, "append")
        XCTAssertEqual(decoded.rule.name, "monitor-example-com")
        XCTAssertEqual(decoded.rule.action, "direct")
        XCTAssertEqual(decoded.rule.domains, ["example.com"])
    }

    func testTrafficDecodesMonitorAnalytics() async throws {
        MockURLProtocol.responseData = Data("""
        {
          "updated_ts_ns": 99,
          "summary": {"active_connections": 1},
          "profile_context": {"active": "Work", "profiles": ["Work", "Home"]},
          "quick_filters": [{"key": "block", "label": "Block", "count": 2}],
          "rule_hits": [{"profile": "Work", "rule_name": "ads", "action": "block", "count": 2, "last_target": "ads.example.com:443"}],
          "block_decisions": [{"conn_id": "c1", "profile": "Work", "rule_name": "ads", "action": "block", "target_host": "ads.example.com", "ts_ns": 88}],
          "cleanup_suggestions": [{"kind": "unused_in_history", "profile": "Work", "rule_name": "old", "target_rule_name": "old", "operation": "delete_rule", "message": "No recent traffic-history entries matched this rule."}],
          "connections": [{"conn_id": "c1", "profile": "Work", "state": "closed", "rule_action": "block", "default": true, "target_host": "ads.example.com"}]
        }
        """.utf8)
        let client = ClambhookAPIClient(
            baseURL: URL(string: "http://127.0.0.1:9090")!,
            session: mockSession()
        )

        let traffic = try await client.traffic()

        XCTAssertEqual(traffic.profileContext.active, "Work")
        XCTAssertEqual(traffic.quickFilters.first?.key, "block")
        XCTAssertEqual(traffic.ruleHits.first?.ruleName, "ads")
        XCTAssertEqual(traffic.blockDecisions.first?.targetHost, "ads.example.com")
        XCTAssertEqual(traffic.cleanupSuggestions.first?.ruleName, "old")
        XCTAssertEqual(traffic.cleanupSuggestions.first?.targetRuleName, "old")
        XCTAssertEqual(traffic.cleanupSuggestions.first?.operation, "delete_rule")
        XCTAssertEqual(traffic.connections.first?.profile, "Work")
        XCTAssertEqual(traffic.connections.first?.isDefault, true)
    }

    func testDNSRequestDecodesUpstreamRoute() async throws {
        MockURLProtocol.responseData = Data("""
        {
          "profile": "Work",
          "strategy": "encrypted",
          "enabled": true,
          "timeout": "5s",
          "intercepts_port_53": true,
          "upstreams": [{"name": "cf", "protocol": "doh", "url": "https://cloudflare-dns.com/dns-query"}],
          "upstream_routes": [{"name": "cf", "protocol": "doh", "target": "cloudflare-dns.com:443", "network": "tcp", "action": "group", "group_name": "manual", "chain_name": "proxy"}]
        }
        """.utf8)
        let client = ClambhookAPIClient(
            baseURL: URL(string: "http://127.0.0.1:9090")!,
            session: mockSession()
        )

        let dns = try await client.dns()

        XCTAssertEqual(MockURLProtocol.lastRequest?.url?.absoluteString, "http://127.0.0.1:9090/api/v1/dns")
        XCTAssertTrue(dns.enabled)
        XCTAssertEqual(dns.upstreams.first?.name, "cf")
        XCTAssertEqual(dns.upstreamRoutes.first?.groupName, "manual")
        XCTAssertEqual(dns.upstreamRoutes.first?.chainName, "proxy")
    }

    func testRefreshRuleSubscriptionsSendsSelectedNames() async throws {
        MockURLProtocol.responseData = Data("""
        {"profile":"Work","subscriptions":[{"name":"ads","url":"https://lists.example.invalid/ads.txt","format":"auto","action":"block","cached":true,"domain_count":1}]}
        """.utf8)
        let client = ClambhookAPIClient(
            baseURL: URL(string: "http://127.0.0.1:9090")!,
            session: mockSession()
        )

        let response = try await client.refreshRuleSubscriptions(names: ["ads"], profile: "Work")

        XCTAssertEqual(response.subscriptions.first?.name, "ads")
        XCTAssertEqual(MockURLProtocol.lastRequest?.httpMethod, "POST")
        XCTAssertEqual(MockURLProtocol.lastRequest?.url?.absoluteString, "http://127.0.0.1:9090/api/v1/rule-subscriptions/refresh")
        let body = try XCTUnwrap(MockURLProtocol.lastBody)
        let decoded = try JSONDecoder().decode(RefreshRuleSubscriptionsRequestBody.self, from: body)
        XCTAssertEqual(decoded.profile, "Work")
        XCTAssertEqual(decoded.names, ["ads"])
    }

    func testCreateRuleFromConnectionSendsAppendRequest() async throws {
        MockURLProtocol.responseData = Data("""
        {"profile":"Work","rules":[{"name":"api","action":"chain:proxy","domains":["api.example.com"]}]}
        """.utf8)
        let client = ClambhookAPIClient(
            baseURL: URL(string: "http://127.0.0.1:9090")!,
            session: mockSession()
        )

        _ = try await client.createRuleFromConnection(connID: "c1", profile: "Work", name: "api", action: "chain:proxy")

        XCTAssertEqual(MockURLProtocol.lastRequest?.httpMethod, "POST")
        XCTAssertEqual(MockURLProtocol.lastRequest?.url?.absoluteString, "http://127.0.0.1:9090/api/v1/rules/from-connection")
        let body = try XCTUnwrap(MockURLProtocol.lastBody)
        let decoded = try JSONDecoder().decode(CreateRuleFromConnectionRequestBody.self, from: body)
        XCTAssertEqual(decoded.connID, "c1")
        XCTAssertEqual(decoded.profile, "Work")
        XCTAssertEqual(decoded.name, "api")
        XCTAssertEqual(decoded.action, "chain:proxy")
        XCTAssertEqual(decoded.scope, "auto")
        XCTAssertEqual(decoded.position, "append")
    }

    func testCleanupRuleSendsSuggestionIdentity() async throws {
        MockURLProtocol.responseData = Data("""
        {"profile":"Work","rules":[{"name":"keep","action":"direct","domains":["keep.example.com"]}]}
        """.utf8)
        let client = ClambhookAPIClient(
            baseURL: URL(string: "http://127.0.0.1:9090")!,
            session: mockSession()
        )

        _ = try await client.cleanupRule(TrafficCleanupSuggestionPayload(
            kind: "unused_in_history",
            profile: "Work",
            ruleName: "old",
            targetRuleName: "old",
            operation: "delete_rule"
        ))

        XCTAssertEqual(MockURLProtocol.lastRequest?.httpMethod, "POST")
        XCTAssertEqual(MockURLProtocol.lastRequest?.url?.absoluteString, "http://127.0.0.1:9090/api/v1/rules/cleanup")
        let body = try XCTUnwrap(MockURLProtocol.lastBody)
        let decoded = try JSONDecoder().decode(CleanupRuleRequestBody.self, from: body)
        XCTAssertEqual(decoded.profile, "Work")
        XCTAssertEqual(decoded.kind, "unused_in_history")
        XCTAssertEqual(decoded.ruleName, "old")
        XCTAssertEqual(decoded.targetRuleName, "old")
        XCTAssertEqual(decoded.operation, "delete_rule")
    }

    func testHTTPErrorIncludesResponseBody() async {
        MockURLProtocol.statusCode = 401
        MockURLProtocol.responseData = Data("unauthorized\n".utf8)
        let client = ClambhookAPIClient(
            baseURL: URL(string: "http://127.0.0.1:9090")!,
            session: mockSession()
        )

        do {
            _ = try await client.status()
            XCTFail("status() succeeded, want HTTP error")
        } catch let error as APIClientError {
            XCTAssertEqual(error.localizedDescription, "401: unauthorized")
        } catch {
            XCTFail("unexpected error: \(error)")
        }
    }
}

private struct CreateRuleRequestBody: Decodable {
    var rule: RulePayload
    var position: String
}

private struct CreateRuleFromConnectionRequestBody: Decodable {
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

private struct CleanupRuleRequestBody: Decodable {
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

private struct RefreshRuleSubscriptionsRequestBody: Decodable {
    var profile: String
    var names: [String]
}

private func mockSession() -> URLSession {
    let config = URLSessionConfiguration.ephemeral
    config.protocolClasses = [MockURLProtocol.self]
    return URLSession(configuration: config)
}

private final class MockURLProtocol: URLProtocol {
    static var responseData = Data()
    static var statusCode = 200
    static var lastRequest: URLRequest?
    static var lastBody: Data?

    static func reset() {
        responseData = Data()
        statusCode = 200
        lastRequest = nil
        lastBody = nil
    }

    override class func canInit(with request: URLRequest) -> Bool {
        true
    }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest {
        request
    }

    override func startLoading() {
        Self.lastRequest = request
        if let stream = request.httpBodyStream {
            stream.open()
            defer { stream.close() }
            var data = Data()
            var buffer = [UInt8](repeating: 0, count: 1024)
            while stream.hasBytesAvailable {
                let count = stream.read(&buffer, maxLength: buffer.count)
                if count > 0 {
                    data.append(buffer, count: count)
                } else {
                    break
                }
            }
            Self.lastBody = data
        } else {
            Self.lastBody = request.httpBody
        }
        let response = HTTPURLResponse(
            url: request.url!,
            statusCode: Self.statusCode,
            httpVersion: nil,
            headerFields: ["Content-Type": "application/json"]
        )!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Self.responseData)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}
}
