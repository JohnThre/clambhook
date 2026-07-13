import Foundation

public enum AppRecoveryStateKind: String, Codable, Equatable, Sendable {
    case missingProfile = "missing_profile"
    case invalidVPNEntitlementOrProfile = "invalid_vpn_entitlement_or_profile"
    case expiredTrial = "expired_trial"
    case licenseBackendUnavailable = "license_backend_unavailable"
    case licenseExpiredForUpdates = "license_expired_for_updates"
    case certificateNotTrusted = "certificate_not_trusted"
    case daemonFallbackUnavailable = "daemon_fallback_unavailable"
}

public enum AppRecoveryStateSeverity: String, Codable, Equatable, Sendable {
    case info
    case warning
    case error
}

public enum AppRecoveryStateAction: String, Codable, Equatable, Identifiable, Sendable {
    case createProfile = "create_profile"
    case importProfile = "import_profile"
    case openProfiles = "open_profiles"
    case retry
    case refresh
    case rebuildVPNProfile = "rebuild_vpn_profile"
    case openAppSettings = "open_app_settings"
    case buyLicense = "buy_license"
    case activateLicense = "activate_license"
    case openLicensePortal = "open_license_portal"
    case renewUpdates = "renew_updates"
    case openSystemSettings = "open_system_settings"
    case trustCertificate = "trust_certificate"
    case launchDaemon = "launch_daemon"
    case openSettings = "open_settings"
    case support
    case privacy

    public var id: String { rawValue }

    public var title: String {
        switch self {
        case .createProfile:
            return "Create Profile"
        case .importProfile:
            return "Import Profile"
        case .openProfiles:
            return "Profiles"
        case .retry:
            return "Retry"
        case .refresh:
            return "Refresh"
        case .rebuildVPNProfile:
            return "Refresh Profile"
        case .openAppSettings:
            return "Settings"
        case .buyLicense:
            return "Buy License"
        case .activateLicense:
            return "Activate License"
        case .openLicensePortal:
            return "License Portal"
        case .renewUpdates:
            return "Renew Updates"
        case .openSystemSettings:
            return "Open System Settings"
        case .trustCertificate:
            return "Trust Certificate"
        case .launchDaemon:
            return "Launch Daemon"
        case .openSettings:
            return "Settings"
        case .support:
            return "Support"
        case .privacy:
            return "Privacy Policy"
        }
    }

    public var systemImage: String {
        switch self {
        case .createProfile:
            return "plus.circle.fill"
        case .importProfile:
            return "tray.and.arrow.down.fill"
        case .openProfiles:
            return "person.crop.rectangle.stack"
        case .retry:
            return "play.fill"
        case .refresh:
            return "arrow.clockwise"
        case .rebuildVPNProfile:
            return "arrow.triangle.2.circlepath"
        case .openAppSettings:
            return "gearshape"
        case .buyLicense:
            return "checkmark.seal.fill"
        case .activateLicense:
            return "arrow.clockwise"
        case .openLicensePortal:
            return "wrench.and.screwdriver"
        case .renewUpdates:
            return "arrow.triangle.2.circlepath.circle.fill"
        case .openSystemSettings:
            return "gearshape.fill"
        case .trustCertificate:
            return "checkmark.shield.fill"
        case .launchDaemon:
            return "terminal"
        case .openSettings:
            return "gearshape"
        case .support:
            return "questionmark.circle"
        case .privacy:
            return "hand.raised"
        }
    }
}

public struct AppRecoveryState: Codable, Equatable, Identifiable, Sendable {
    public var id: AppRecoveryStateKind { kind }
    public var kind: AppRecoveryStateKind
    public var severity: AppRecoveryStateSeverity
    public var title: String
    public var message: String
    public var systemImage: String
    public var primaryAction: AppRecoveryStateAction
    public var secondaryActions: [AppRecoveryStateAction]
    public var diagnosticText: String

    public init(
        kind: AppRecoveryStateKind,
        severity: AppRecoveryStateSeverity,
        title: String,
        message: String,
        systemImage: String,
        primaryAction: AppRecoveryStateAction,
        secondaryActions: [AppRecoveryStateAction],
        diagnosticText: String = ""
    ) {
        self.kind = kind
        self.severity = severity
        self.title = title
        self.message = message
        self.systemImage = systemImage
        self.primaryAction = primaryAction
        self.secondaryActions = secondaryActions
        self.diagnosticText = diagnosticText
    }
}

public enum LicensePurchaseAvailabilityKind: String, Codable, Equatable, Sendable {
    case unknown
    case loading
    case available
    case unavailable
}

public struct LicensePurchaseAvailability: Codable, Equatable, Sendable {
    public var kind: LicensePurchaseAvailabilityKind
    public var message: String

    public init(kind: LicensePurchaseAvailabilityKind = .unknown, message: String = "") {
        self.kind = kind
        self.message = message
    }

    public static let unknown = LicensePurchaseAvailability(kind: .unknown)
    public static let loading = LicensePurchaseAvailability(kind: .loading)
    public static let available = LicensePurchaseAvailability(kind: .available)

    public static func unavailable(_ message: String) -> LicensePurchaseAvailability {
        LicensePurchaseAvailability(kind: .unavailable, message: message)
    }

    public var isUnavailable: Bool {
        kind == .unavailable
    }
}

public enum AppRecoveryStateBuilder {
    public static func noProfile(diagnosticText: String = "") -> AppRecoveryState {
        AppRecoveryState(
            kind: .missingProfile,
            severity: .info,
            title: "No profile yet",
            message: "Import or create a tunnel profile before connecting. Profile credentials stay on this Mac.",
            systemImage: "person.crop.rectangle.stack",
            primaryAction: .importProfile,
            secondaryActions: [.createProfile, .refresh],
            diagnosticText: diagnosticText.trimmingCharacters(in: .whitespacesAndNewlines)
        )
    }

    public static func missingProfile(readinessMessage: String) -> AppRecoveryState? {
        let trimmed = readinessMessage.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            return nil
        }
        return AppRecoveryState(
            kind: .missingProfile,
            severity: .info,
            title: "Add a tunnel profile",
            message: "Create or import a profile before connecting. clambhook keeps profile credentials on this device.",
            systemImage: "person.crop.rectangle.stack",
            primaryAction: .createProfile,
            secondaryActions: [.importProfile, .refresh],
            diagnosticText: trimmed
        )
    }

    public static func invalidVPNEntitlementOrProfile(issue: TunnelRecoveryIssue) -> AppRecoveryState? {
        guard issue.kind == .invalidEntitlementOrProfile else {
            return nil
        }
        return AppRecoveryState(
            kind: .invalidVPNEntitlementOrProfile,
            severity: .error,
            title: issue.title,
            message: issue.message,
            systemImage: "exclamationmark.shield.fill",
            primaryAction: .rebuildVPNProfile,
            secondaryActions: [.refresh, .openAppSettings],
            diagnosticText: issue.rawError
        )
    }

    public static func expiredTrial(
        decision: MobileLicenseDecision,
        purchaseAvailability: LicensePurchaseAvailability = .unknown
    ) -> AppRecoveryState? {
        guard !decision.canUseApp else {
            return nil
        }
        if purchaseAvailability.isUnavailable {
            return licenseBackendUnavailable(message: purchaseAvailability.message)
        }
        return AppRecoveryState(
            kind: .expiredTrial,
            severity: .warning,
            title: "Trial ended",
            message: expiredTrialMessage(decision: decision),
            systemImage: "lock.fill",
            primaryAction: .buyLicense,
            secondaryActions: [.activateLicense, .openLicensePortal, .support],
            diagnosticText: ""
        )
    }

    public static func licenseBackendUnavailable(message: String) -> AppRecoveryState {
        let trimmed = message.trimmingCharacters(in: .whitespacesAndNewlines)
        return AppRecoveryState(
            kind: .licenseBackendUnavailable,
            severity: .error,
            title: "License service unavailable",
            message: "The store.swiphtgroup.com license service is not reachable right now. Activate with an existing key or try again after the service is reachable.",
            systemImage: "cart.badge.exclamationmark",
            primaryAction: .activateLicense,
            secondaryActions: [.openLicensePortal, .refresh, .support],
            diagnosticText: trimmed
        )
    }

    public static func licenseExpiredForUpdates(
        decision: MobileLicenseDecision,
        manifestPublishedAt: Date? = nil,
        now: Date = Date()
    ) -> AppRecoveryState? {
        guard decision.hasLifetimeUnlock, let cutoffDate = decision.updateCutoffDate else {
            return nil
        }
        let releaseDate = manifestPublishedAt ?? now
        guard releaseDate > cutoffDate else {
            return nil
        }
        let cutoff = cutoffDate.formatted(date: .abbreviated, time: .omitted)
        let release = manifestPublishedAt.map { $0.formatted(date: .abbreviated, time: .omitted) }
        let message: String
        if let release {
            message = "This update was published \(release), after your included update window ended \(cutoff). Your installed version keeps working; renew updates for USD 9.99 to install releases after the cutoff, including critical, bug, and security updates."
        } else {
            message = "Your included update window ended \(cutoff). Your installed version keeps working; renew updates for USD 9.99 to install releases after the cutoff, including critical, bug, and security updates."
        }
        return AppRecoveryState(
            kind: .licenseExpiredForUpdates,
            severity: .warning,
            title: "Update window ended",
            message: message,
            systemImage: "calendar.badge.exclamationmark",
            primaryAction: .renewUpdates,
            secondaryActions: [.openLicensePortal, .activateLicense, .support],
            diagnosticText: MobileLicenseCopy.paidUpdatePolicy(cutoffDate: cutoffDate)
        )
    }

    public static func certificateNotTrusted(fingerprint: String) -> AppRecoveryState? {
        let trimmed = fingerprint.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            return nil
        }
        return AppRecoveryState(
            kind: .certificateNotTrusted,
            severity: .warning,
            title: "Certificate not trusted",
            message: "HTTPS capture needs the local ClambHook CA trusted in your login keychain before test traffic will accept inspected connections.",
            systemImage: "xmark.shield.fill",
            primaryAction: .trustCertificate,
            secondaryActions: [.refresh, .support],
            diagnosticText: "SHA-256 \(trimmed)"
        )
    }

    public static func daemonFallbackUnavailable(message: String = "") -> AppRecoveryState {
        let trimmed = message.trimmingCharacters(in: .whitespacesAndNewlines)
        return AppRecoveryState(
            kind: .daemonFallbackUnavailable,
            severity: .error,
            title: "Daemon fallback unavailable",
            message: "System proxy fallback needs the ClambHook daemon API. Launch the daemon or fix the daemon helper settings before using proxy routing.",
            systemImage: "terminal.fill",
            primaryAction: .launchDaemon,
            secondaryActions: [.openSettings, .refresh, .support],
            diagnosticText: trimmed
        )
    }

    private static func expiredTrialMessage(decision: MobileLicenseDecision) -> String {
        if let trialEndsAt = decision.trialEndsAt {
            return "The one-calendar-month trial ended \(trialEndsAt.formatted(date: .abbreviated, time: .omitted)). Buy or activate a USD 99.99 one-time ClambHook license to continue."
        }
        return "Buy or activate a ClambHook license to continue."
    }
}
