import XCTest
@testable import ClambhookShared

final class PromptSupportTests: XCTestCase {
    // The client decodes daemon responses with the `.iso8601` date strategy,
    // which rejects fractional seconds. prompt.Pending emits `created_at` as
    // RFC3339Nano, so the payload must parse both forms without failing.
    private func iso8601Decoder() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return decoder
    }

    func testDecodesPendingPromptsWithFractionalSecondsTimestamp() throws {
        let json = """
        {
          "prompts": [
            {
              "id": "p1",
              "conn_id": "c1",
              "profile": "home",
              "network": "tcp",
              "target": "example.com:443",
              "target_host": "example.com",
              "target_port": "443",
              "pid": 4242,
              "process_name": "curl",
              "process_path": "/usr/bin/curl",
              "created_at": "2026-07-14T18:30:00.123456789Z",
              "waiters": 3
            }
          ]
        }
        """
        let payload = try iso8601Decoder().decode(PendingPromptsPayload.self, from: Data(json.utf8))
        XCTAssertEqual(payload.prompts.count, 1)
        let prompt = try XCTUnwrap(payload.prompts.first)
        XCTAssertEqual(prompt.id, "p1")
        XCTAssertEqual(prompt.target, "example.com:443")
        XCTAssertEqual(prompt.processName, "curl")
        XCTAssertEqual(prompt.pid, 4242)
        XCTAssertEqual(prompt.waiters, 3)
        XCTAssertEqual(prompt.processLabel, "curl")
        XCTAssertGreaterThan(prompt.createdAt.timeIntervalSince1970, 0)
    }

    func testDecodesPendingPromptWithoutFractionalSecondsAndSparseFields() throws {
        let json = """
        {
          "prompts": [
            { "id": "p2", "target": "10.0.0.5:53", "created_at": "2026-07-14T18:30:00Z" }
          ]
        }
        """
        let payload = try iso8601Decoder().decode(PendingPromptsPayload.self, from: Data(json.utf8))
        let prompt = try XCTUnwrap(payload.prompts.first)
        XCTAssertEqual(prompt.id, "p2")
        XCTAssertEqual(prompt.target, "10.0.0.5:53")
        XCTAssertEqual(prompt.pid, 0)
        XCTAssertEqual(prompt.processName, "")
        XCTAssertEqual(prompt.processLabel, "Unknown process")
    }

    func testDecodesEmptyPromptsPayload() throws {
        let payload = try iso8601Decoder().decode(PendingPromptsPayload.self, from: Data("{}".utf8))
        XCTAssertTrue(payload.prompts.isEmpty)
    }

    func testResolvePromptRequestEncodesSnakeCaseKeys() throws {
        let request = ResolvePromptRequest(action: .block, scope: .forever, matchHost: true, ttlSeconds: 900)
        let data = try JSONEncoder().encode(request)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(object["action"] as? String, "block")
        XCTAssertEqual(object["scope"] as? String, "forever")
        XCTAssertEqual(object["match_host"] as? Bool, true)
        XCTAssertEqual(object["ttl_seconds"] as? Int, 900)
    }

    func testResolvePromptRequestDefaults() throws {
        let request = ResolvePromptRequest(action: .allow)
        XCTAssertEqual(request.scope, PromptDecisionScope.once.rawValue)
        XCTAssertFalse(request.matchHost)
        XCTAssertEqual(request.ttlSeconds, 0)
    }
}
