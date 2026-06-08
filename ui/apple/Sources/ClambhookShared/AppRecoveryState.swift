import Foundation

public enum AppRecoveryStateKind: String, Codable, Equatable, Sendable {
    case missingProfile = "missing_profile"
    case invalidVPNEntitlementOrProfile = "invalid_vpn_entitlement_or_profile"
    case expiredTrial = "expired_trial"
    case storeKitUnavailable = "storekit_unavailable"
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
    case purchaseLifetime = "purchase_lifetime"
    case restorePurchases = "restore_purchases"
    case repairPurchaseHistory = "repair_purchase_history"
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
            return "Rebuild VPN Profile"
        case .openAppSettings:
            return "Settings"
        case .purchaseLifetime:
            return "Unlock Lifetime"
        case .restorePurchases:
            return "Restore Purchases"
        case .repairPurchaseHistory:
            return "Repair Purchase History"
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
        case .purchaseLifetime:
            return "checkmark.seal.fill"
        case .restorePurchases:
            return "arrow.clockwise"
        case .repairPurchaseHistory:
            return "wrench.and.screwdriver"
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

public enum StoreKitAvailabilityKind: String, Codable, Equatable, Sendable {
    case unknown
    case loading
    case available
    case unavailable
}

public struct StoreKitAvailability: Codable, Equatable, Sendable {
    public var kind: StoreKitAvailabilityKind
    public var message: String

    public init(kind: StoreKitAvailabilityKind = .unknown, message: String = "") {
        self.kind = kind
        self.message = message
    }

    public static let unknown = StoreKitAvailability(kind: .unknown)
    public static let loading = StoreKitAvailability(kind: .loading)
    public static let available = StoreKitAvailability(kind: .available)

    public static func unavailable(_ message: String) -> StoreKitAvailability {
        StoreKitAvailability(kind: .unavailable, message: message)
    }

    public var isUnavailable: Bool {
        kind == .unavailable
    }
}

public enum AppRecoveryStateBuilder {
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
        storeKitAvailability: StoreKitAvailability = .unknown
    ) -> AppRecoveryState? {
        guard !decision.canUseApp else {
            return nil
        }
        if storeKitAvailability.isUnavailable {
            return storeKitUnavailable(message: storeKitAvailability.message)
        }
        return AppRecoveryState(
            kind: .expiredTrial,
            severity: .warning,
            title: "Trial ended",
            message: expiredTrialMessage(decision: decision),
            systemImage: "lock.fill",
            primaryAction: .purchaseLifetime,
            secondaryActions: [.restorePurchases, .repairPurchaseHistory, .support],
            diagnosticText: ""
        )
    }

    public static func storeKitUnavailable(message: String) -> AppRecoveryState {
        let trimmed = message.trimmingCharacters(in: .whitespacesAndNewlines)
        return AppRecoveryState(
            kind: .storeKitUnavailable,
            severity: .error,
            title: "Purchases unavailable",
            message: "The App Store purchase catalog is not available right now. Restore or repair purchase history, or try again after the store is reachable.",
            systemImage: "cart.badge.exclamationmark",
            primaryAction: .restorePurchases,
            secondaryActions: [.repairPurchaseHistory, .refresh, .support],
            diagnosticText: trimmed
        )
    }

    private static func expiredTrialMessage(decision: MobileLicenseDecision) -> String {
        if let trialEndsAt = decision.trialEndsAt {
            return "The free trial ended \(trialEndsAt.formatted(date: .abbreviated, time: .omitted)). Purchase or restore the lifetime unlock to continue."
        }
        return "Purchase or restore the lifetime unlock to continue."
    }
}
