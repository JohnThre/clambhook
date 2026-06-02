import Foundation

public enum TunnelRecoveryKind: String, Codable, Equatable, Sendable {
    case vpnPermissionDenied = "vpn_permission_denied"
    case invalidEntitlementOrProfile = "invalid_entitlement_or_profile"
    case badServerCredentials = "bad_server_credentials"
    case noUDPSupport = "no_udp_support"
    case demoProfileExpired = "demo_profile_expired"
    case generic = "generic"
}

public enum TunnelRecoveryAction: String, Codable, Equatable, Identifiable, Sendable {
    case retry
    case refresh
    case openAppSettings = "open_app_settings"
    case rebuildVPNProfile = "rebuild_vpn_profile"
    case openProfiles = "open_profiles"
    case importProfile = "import_profile"

    public var id: String { rawValue }

    public var title: String {
        switch self {
        case .retry:
            return "Retry"
        case .refresh:
            return "Refresh"
        case .openAppSettings:
            return "Settings"
        case .rebuildVPNProfile:
            return "Rebuild"
        case .openProfiles:
            return "Profiles"
        case .importProfile:
            return "Import"
        }
    }
}

public struct TunnelRecoveryIssue: Codable, Equatable, Identifiable, Sendable {
    public var id: TunnelRecoveryKind { kind }
    public var kind: TunnelRecoveryKind
    public var title: String
    public var message: String
    public var actions: [TunnelRecoveryAction]
    public var rawError: String

    public init(
        kind: TunnelRecoveryKind,
        title: String,
        message: String,
        actions: [TunnelRecoveryAction],
        rawError: String = ""
    ) {
        self.kind = kind
        self.title = title
        self.message = message
        self.actions = actions
        self.rawError = rawError
    }
}

public enum TunnelRecoveryClassifier {
    public static func issue(for error: Error) -> TunnelRecoveryIssue {
        if let recoveryError = error as? TunnelRecoveryError {
            return recoveryError.issue
        }
        return issue(forRawError: error.localizedDescription)
    }

    public static func issue(forRawError rawError: String) -> TunnelRecoveryIssue {
        let trimmed = rawError.trimmingCharacters(in: .whitespacesAndNewlines)
        let lower = trimmed.lowercased()

        if lower.contains("permission denied") ||
            lower.contains("not authorized") ||
            lower.contains("user denied") ||
            lower.contains("cancelled") ||
            lower.contains("canceled") {
            return vpnPermissionDenied(rawError: trimmed)
        }

        if lower.contains("entitlement") ||
            lower.contains("provision") ||
            lower.contains("profile") && lower.contains("invalid") ||
            lower.contains("configuration invalid") ||
            lower.contains("configuration is disabled") ||
            lower.contains("configuration disabled") ||
            lower.contains("packet tunnel session is unavailable") ||
            lower.contains("invalid session") {
            return invalidEntitlementOrProfile(rawError: trimmed)
        }

        if lower.contains("does not support udp") ||
            lower.contains("no udp support") ||
            lower.contains("udp over a tunneled stream is not supported") ||
            lower.contains("cannot tunnel wireguard") ||
            lower.contains("cannot tunnel openvpn") ||
            lower.contains("route does not support udp") {
            return noUDPSupport(rawError: trimmed)
        }

        if lower.contains("server rejected auth") ||
            lower.contains("auth failed") ||
            lower.contains("authentication") ||
            lower.contains("userpass auth failed") ||
            lower.contains("decrypt length") ||
            lower.contains("decrypt payload") ||
            lower.contains("decrypt failed") ||
            lower.contains("bad record mac") ||
            lower.contains("certificate") && lower.contains("unknown") ||
            lower.contains("tls handshake") && lower.contains("certificate") {
            return badServerCredentials(rawError: trimmed)
        }

        return generic(rawError: trimmed)
    }

    public static func expiredDemoProfile(profile: String, expiresAt: Date) -> TunnelRecoveryIssue {
        let date = DateFormatter.localizedString(from: expiresAt, dateStyle: .medium, timeStyle: .none)
        return TunnelRecoveryIssue(
            kind: .demoProfileExpired,
            title: "Demo profile expired",
            message: "\(profile) expired on \(date). Import a replacement profile before connecting.",
            actions: [.importProfile, .openProfiles],
            rawError: ""
        )
    }

    private static func vpnPermissionDenied(rawError: String) -> TunnelRecoveryIssue {
        TunnelRecoveryIssue(
            kind: .vpnPermissionDenied,
            title: "VPN permission was denied",
            message: "Allow clambhook to add a VPN configuration, then connect again.",
            actions: [.retry, .openAppSettings],
            rawError: rawError
        )
    }

    private static func invalidEntitlementOrProfile(rawError: String) -> TunnelRecoveryIssue {
        TunnelRecoveryIssue(
            kind: .invalidEntitlementOrProfile,
            title: "VPN profile is not usable",
            message: "The installed VPN profile or app entitlement is invalid. Rebuild the local VPN profile and refresh.",
            actions: [.rebuildVPNProfile, .refresh],
            rawError: rawError
        )
    }

    private static func badServerCredentials(rawError: String) -> TunnelRecoveryIssue {
        TunnelRecoveryIssue(
            kind: .badServerCredentials,
            title: "Server credentials failed",
            message: "The active server rejected the profile credentials or certificate. Check the profile and try again.",
            actions: [.openProfiles, .retry],
            rawError: rawError
        )
    }

    private static func noUDPSupport(rawError: String) -> TunnelRecoveryIssue {
        TunnelRecoveryIssue(
            kind: .noUDPSupport,
            title: "Active route cannot carry UDP",
            message: "Device VPN mode needs UDP support. Choose a profile whose final hop supports UDP.",
            actions: [.openProfiles],
            rawError: rawError
        )
    }

    private static func generic(rawError: String) -> TunnelRecoveryIssue {
        TunnelRecoveryIssue(
            kind: .generic,
            title: "Connection failed",
            message: rawError.isEmpty ? "The tunnel could not connect. Refresh and try again." : rawError,
            actions: [.refresh, .retry],
            rawError: rawError
        )
    }
}

public struct TunnelProviderErrorEnvelope: Codable, Equatable, Sendable {
    public var error: TunnelRecoveryIssue

    public init(error: TunnelRecoveryIssue) {
        self.error = error
    }
}

public struct TunnelRecoveryError: Error, LocalizedError, Equatable, Sendable {
    public var issue: TunnelRecoveryIssue

    public init(_ issue: TunnelRecoveryIssue) {
        self.issue = issue
    }

    public var errorDescription: String? {
        issue.message
    }
}
