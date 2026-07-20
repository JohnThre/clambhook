import XCTest
@testable import ClambhookShared

final class DaemonLaunchTests: XCTestCase {
    func testArgumentsNeverContainAPIToken() {
        let args = DaemonLaunchPlanner.arguments(apiHostPort: "127.0.0.1:9090", configPath: "/tmp/c.toml")
        XCTAssertEqual(args, ["-api", "127.0.0.1:9090", "-config", "/tmp/c.toml"])
        XCTAssertFalse(args.contains("-api-token"))
    }

    func testArgumentsOmitEmptyAPIAndConfig() {
        XCTAssertEqual(DaemonLaunchPlanner.arguments(apiHostPort: "  ", configPath: "   "), [])
        XCTAssertEqual(DaemonLaunchPlanner.arguments(apiHostPort: "127.0.0.1:9090", configPath: nil), ["-api", "127.0.0.1:9090"])
    }

    func testEnvironmentCarriesTrimmedToken() {
        let env = DaemonLaunchPlanner.environment(base: ["PATH": "/usr/bin"], token: "  secret-token  ")
        XCTAssertEqual(env[DaemonLaunchPlanner.apiTokenEnvironmentKey], "secret-token")
        XCTAssertEqual(env["PATH"], "/usr/bin")
    }

    func testEmptyTokenRemovesInheritedTokenToPreventLeak() {
        let base = [DaemonLaunchPlanner.apiTokenEnvironmentKey: "stale", "PATH": "/usr/bin"]
        let env = DaemonLaunchPlanner.environment(base: base, token: "   ")
        XCTAssertNil(env[DaemonLaunchPlanner.apiTokenEnvironmentKey])
        XCTAssertEqual(env["PATH"], "/usr/bin")
    }

    func testEnvironmentKeyMatchesDaemonFlagDefault() {
        // cmd/clambhook/main.go reads os.Getenv("CLAMBHOOK_API_TOKEN") as the
        // default for -api-token; the key must match exactly.
        XCTAssertEqual(DaemonLaunchPlanner.apiTokenEnvironmentKey, "CLAMBHOOK_API_TOKEN")
    }
}
