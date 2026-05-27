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
            "ws://127.0.0.1:9090/api/v1/events?types=connection.*,rule.*,log.*"
        )

        let httpsClient = ClambhookAPIClient(baseURL: URL(string: "https://proxy.example.test")!)
        XCTAssertEqual(
            httpsClient.eventsURL().absoluteString,
            "wss://proxy.example.test/api/v1/events?types=connection.*,rule.*,log.*"
        )
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
