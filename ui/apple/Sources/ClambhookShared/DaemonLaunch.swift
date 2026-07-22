import Foundation

/// Builds the argument vector and environment used to spawn the `clambhook`
/// daemon.
///
/// The API bearer token is deliberately kept out of the argument vector: process
/// arguments are world-readable via `ps`/`sysctl`, so the token is passed through
/// the child environment (`CLAMBHOOK_API_TOKEN`) instead. The daemon already
/// reads that variable as the default value for its `-api-token` flag, so the
/// resulting auth token is identical to the old `-api-token <token>` form.
public enum DaemonLaunchPlanner {
    public static let apiTokenEnvironmentKey = "CLAMBHOOK_API_TOKEN"

    /// Daemon CLI arguments. The token is intentionally omitted here.
    public static func arguments(apiHostPort: String, configPath: String?, licensePath: String? = nil) -> [String] {
        var args: [String] = []
        let trimmedAPI = apiHostPort.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmedAPI.isEmpty {
            args += ["-api", trimmedAPI]
        }
        if let configPath {
            let trimmedConfig = configPath.trimmingCharacters(in: .whitespacesAndNewlines)
            if !trimmedConfig.isEmpty {
                args += ["-config", trimmedConfig]
            }
        }
        if let licensePath {
            let trimmedLicense = licensePath.trimmingCharacters(in: .whitespacesAndNewlines)
            if !trimmedLicense.isEmpty {
                args += ["-license", trimmedLicense]
            }
        }
        return args
    }

    /// Child-process environment carrying the API token.
    ///
    /// When the token is empty the key is *removed* from the inherited
    /// environment so a stale `CLAMBHOOK_API_TOKEN` cannot silently leak into the
    /// daemon.
    public static func environment(base: [String: String], token: String) -> [String: String] {
        var env = base
        let trimmed = token.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty {
            env.removeValue(forKey: apiTokenEnvironmentKey)
        } else {
            env[apiTokenEnvironmentKey] = trimmed
        }
        return env
    }
}