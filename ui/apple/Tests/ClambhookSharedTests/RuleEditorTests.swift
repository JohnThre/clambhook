import XCTest
@testable import ClambhookShared

final class RuleEditorTests: XCTestCase {
    func testRowsExpandSameFamilyMatcherValues() {
        let rows = RuleEditor.rows(from: [
            RulePayload(
                name: "apple",
                action: "direct",
                domainSuffixes: ["apple.com", "icloud.com"]
            )
        ])

        XCTAssertEqual(rows.count, 2)
        XCTAssertEqual(rows.map(\.matcherKind), [.domainSuffix, .domainSuffix])
        XCTAssertEqual(rows.map(\.value), ["apple.com", "icloud.com"])
        XCTAssertEqual(rows.map(\.policyKind), [.direct, .direct])
    }

    func testRowsPreserveCombinedRuleMatchers() throws {
        var rows = RuleEditor.rows(from: [
            RulePayload(
                name: "api-https",
                action: "chain:proxy",
                domains: ["api.example.com"],
                ports: [443],
                networks: ["tcp"]
            )
        ])

        XCTAssertEqual(rows.count, 1)
        XCTAssertEqual(rows[0].matcherKind, .combined)
        XCTAssertEqual(rows[0].compatibilityRule?.domains, ["api.example.com"])

        rows[0].policyKind = .direct
        let rules = try RuleEditor.rules(from: rows, chainNames: ["proxy"])

        XCTAssertEqual(rules, [
            RulePayload(
                name: "api-https",
                action: "direct",
                domains: ["api.example.com"],
                ports: [443],
                networks: ["tcp"]
            )
        ])
    }

    func testRulesConvertStructuredRowsToRulePayloads() throws {
        let rows = [
            RuleEditorRow(
                name: "ads",
                matcherKind: .domainSuffix,
                value: "ads.example.com",
                policyKind: .block
            ),
            RuleEditorRow(
                name: "web",
                matcherKind: .port,
                value: "443",
                policyKind: .proxy,
                chainName: "proxy"
            ),
            RuleEditorRow(
                name: "final",
                matcherKind: .allTraffic,
                policyKind: .direct
            ),
        ]

        let rules = try RuleEditor.rules(from: rows, chainNames: ["proxy"])

        XCTAssertEqual(rules, [
            RulePayload(name: "ads", action: "block", domainSuffixes: ["ads.example.com"]),
            RulePayload(name: "web", action: "chain:proxy", ports: [443]),
            RulePayload(name: "final", action: "direct"),
        ])
    }

    func testRowsCanAppendUnstoredVirtualFinal() throws {
        let rows = RuleEditor.rows(
            from: [
                RulePayload(name: "ads", action: "block", domainSuffixes: ["ads.example.com"])
            ],
            defaultChainName: "proxy",
            includeVirtualFinal: true
        )

        XCTAssertEqual(rows.count, 2)
        XCTAssertEqual(rows[1].source, .virtualFinal)
        XCTAssertEqual(rows[1].name, "FINAL")
        XCTAssertEqual(rows[1].matcherKind, .allTraffic)
        XCTAssertEqual(rows[1].chainName, "proxy")

        let rules = try RuleEditor.rules(from: rows, chainNames: ["proxy"], defaultChainName: "proxy")

        XCTAssertEqual(rules, [
            RulePayload(name: "ads", action: "block", domainSuffixes: ["ads.example.com"])
        ])
    }

    func testEditedVirtualFinalBecomesStoredRule() throws {
        var rows = RuleEditor.rows(from: [], defaultChainName: "proxy", includeVirtualFinal: true)
        rows[0].policyKind = .direct

        let rules = try RuleEditor.rules(from: rows, chainNames: ["proxy"], defaultChainName: "proxy")

        XCTAssertEqual(rules, [
            RulePayload(name: "FINAL", action: "direct")
        ])
    }

    func testGeneratedRowsAreReadOnlyForPersistence() throws {
        let rows = RuleEditor.rows(
            from: [
                RulePayload(name: "subscription:ads:domains", action: "block", domainSuffixes: ["ads.example.com"])
            ],
            source: .generated
        )

        XCTAssertEqual(rows.first?.source, .generated)
        XCTAssertEqual(try RuleEditor.rules(from: rows, chainNames: []), [])
    }

    func testValidationRejectsAllTrafficBeforeEnd() {
        let rows = [
            RuleEditorRow(name: "final", matcherKind: .allTraffic, policyKind: .direct),
            RuleEditorRow(name: "later", matcherKind: .domain, value: "example.com", policyKind: .direct),
        ]

        let errors = RuleEditor.validate(rows: rows, chainNames: [])

        XCTAssertEqual(errors, [
            RuleEditorValidationError(rowIndex: 0, message: "all traffic must be last"),
        ])
    }

    func testValidationRejectsInvalidMatcherValues() {
        let rows = [
            RuleEditorRow(name: "bad-cidr", matcherKind: .cidr, value: "10.0.0.0/99", policyKind: .direct),
            RuleEditorRow(name: "bad-port", matcherKind: .port, value: "70000", policyKind: .direct),
            RuleEditorRow(name: "bad-network", matcherKind: .network, value: "icmp", policyKind: .direct),
        ]

        let errors = RuleEditor.validate(rows: rows, chainNames: [])

        XCTAssertEqual(errors.map(\.message), [
            "CIDR is invalid",
            "port must be 0 through 65535",
            "network must be TCP or UDP",
        ])
    }

    func testValidationRejectsMissingAndUnknownProxyChain() {
        let rows = [
            RuleEditorRow(name: "missing", matcherKind: .domain, value: "example.com", policyKind: .proxy),
            RuleEditorRow(name: "unknown", matcherKind: .domain, value: "example.org", policyKind: .proxy, chainName: "backup"),
        ]

        let errors = RuleEditor.validate(rows: rows, chainNames: ["proxy"])

        XCTAssertEqual(errors, [
            RuleEditorValidationError(rowIndex: 0, message: "proxy chain is required"),
            RuleEditorValidationError(rowIndex: 1, message: "proxy chain backup was not found"),
        ])
    }

    func testRulesThrowValidationFailure() {
        let rows = [
            RuleEditorRow(name: "", matcherKind: .port, value: "443", policyKind: .direct),
        ]

        do {
            _ = try RuleEditor.rules(from: rows, chainNames: [])
            XCTFail("rules(from:) succeeded, want validation failure")
        } catch let failure as RuleEditorValidationFailure {
            XCTAssertEqual(failure.errors, [
                RuleEditorValidationError(rowIndex: 0, message: "name is required"),
            ])
            XCTAssertEqual(failure.localizedDescription, "Rule 1: name is required")
        } catch {
            XCTFail("unexpected error: \(error)")
        }
    }
}
