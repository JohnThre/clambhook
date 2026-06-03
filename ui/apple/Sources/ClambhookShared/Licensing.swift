import Foundation

public let mobileLicenseTrialMonths = 2
public let mobileLicenseOfflineGraceDays = 7
public let mobileLicenseSnapshotDefaultsKey = "clambhook.apple.license.snapshot"

public enum MobileLicenseFeatureID: String, CaseIterable, Codable, Sendable {
    case tunnelRouting = "tunnel.routing"
    case profileManagement = "profile.management"
    case routingRules = "routing.rules"
    case activityInspection = "activity.inspection"
    case httpMetadata = "http.metadata"
    case widgets = "widgets"
}

public struct MobileLicenseFeature: Identifiable, Codable, Equatable, Sendable {
    public var id: MobileLicenseFeatureID
    public var displayName: String
    public var releaseDate: Date

    public init(id: MobileLicenseFeatureID, displayName: String, releaseDate: Date) {
        self.id = id
        self.displayName = displayName
        self.releaseDate = releaseDate
    }
}

public enum MobileLicenseFeatureCatalog {
    public static let v1ReleaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)

    public static let features: [MobileLicenseFeature] = [
        MobileLicenseFeature(id: .tunnelRouting, displayName: "Tunnel Routing", releaseDate: v1ReleaseDate),
        MobileLicenseFeature(id: .profileManagement, displayName: "Profile Management", releaseDate: v1ReleaseDate),
        MobileLicenseFeature(id: .routingRules, displayName: "Routing Rules", releaseDate: v1ReleaseDate),
        MobileLicenseFeature(id: .activityInspection, displayName: "Activity Inspection", releaseDate: v1ReleaseDate),
        MobileLicenseFeature(id: .httpMetadata, displayName: "HTTP Metadata", releaseDate: v1ReleaseDate),
        MobileLicenseFeature(id: .widgets, displayName: "Widgets", releaseDate: v1ReleaseDate),
    ]
}

public enum MobileLicenseProductKind: Equatable, Sendable {
    case lifetimeUnlock
    case paidUpdate(year: Int)
    case unknown
}

public struct MobileLicenseTransaction: Codable, Equatable, Sendable {
    public var productID: String
    public var purchaseDate: Date
    public var revocationDate: Date?

    public init(productID: String, purchaseDate: Date, revocationDate: Date? = nil) {
        self.productID = productID
        self.purchaseDate = purchaseDate
        self.revocationDate = revocationDate
    }

    public var productKind: MobileLicenseProductKind {
        MobilePurchaseCatalog.productKind(for: productID)
    }

    public var isActive: Bool {
        revocationDate == nil
    }
}

public struct MobileLicenseSnapshot: Codable, Equatable, Sendable {
    public var trialStartDate: Date?
    public var transactions: [MobileLicenseTransaction]
    public var lastVerifiedAt: Date?
    public var lastVerificationFailedAt: Date?
    public var cachedAt: Date

    public init(
        trialStartDate: Date? = nil,
        transactions: [MobileLicenseTransaction] = [],
        lastVerifiedAt: Date? = nil,
        lastVerificationFailedAt: Date? = nil,
        cachedAt: Date = Date()
    ) {
        self.trialStartDate = trialStartDate
        self.transactions = transactions
        self.lastVerifiedAt = lastVerifiedAt
        self.lastVerificationFailedAt = lastVerificationFailedAt
        self.cachedAt = cachedAt
    }
}

public enum MobileLicenseAccessReason: String, Codable, Equatable, Sendable {
    case trial
    case lifetime
    case offlineGrace
    case locked
}

public struct MobileLicenseDecision: Equatable, Sendable {
    public var reason: MobileLicenseAccessReason
    public var trialStartDate: Date?
    public var trialEndsAt: Date?
    public var trialDaysRemaining: Int
    public var hasLifetimeUnlock: Bool
    public var updateCutoffDate: Date?
    public var offlineGraceEndsAt: Date?
    public var unlockedFeatureIDs: Set<MobileLicenseFeatureID>

    public init(
        reason: MobileLicenseAccessReason,
        trialStartDate: Date?,
        trialEndsAt: Date?,
        trialDaysRemaining: Int,
        hasLifetimeUnlock: Bool,
        updateCutoffDate: Date?,
        offlineGraceEndsAt: Date?,
        unlockedFeatureIDs: Set<MobileLicenseFeatureID>
    ) {
        self.reason = reason
        self.trialStartDate = trialStartDate
        self.trialEndsAt = trialEndsAt
        self.trialDaysRemaining = trialDaysRemaining
        self.hasLifetimeUnlock = hasLifetimeUnlock
        self.updateCutoffDate = updateCutoffDate
        self.offlineGraceEndsAt = offlineGraceEndsAt
        self.unlockedFeatureIDs = unlockedFeatureIDs
    }

    public var canUseApp: Bool {
        reason != .locked
    }

    public var isTrialActive: Bool {
        reason == .trial
    }

    public var isOfflineGraceActive: Bool {
        reason == .offlineGrace
    }

    public func canUseFeature(_ featureID: MobileLicenseFeatureID) -> Bool {
        canUseApp && unlockedFeatureIDs.contains(featureID)
    }
}

public enum MobileLicenseEvaluator {
    public static func evaluate(
        snapshot: MobileLicenseSnapshot,
        features: [MobileLicenseFeature] = MobileLicenseFeatureCatalog.features,
        now: Date = Date(),
        calendar: Calendar = mobileLicenseCalendar
    ) -> MobileLicenseDecision {
        let trialEndsAt = snapshot.trialStartDate.flatMap {
            calendar.date(byAdding: .month, value: mobileLicenseTrialMonths, to: $0)
        }
        let trialActive = trialEndsAt.map { now < $0 } ?? false
        let trialDaysRemaining = trialEndsAt.map {
            max(0, calendar.dateComponents([.day], from: calendar.startOfDay(for: now), to: calendar.startOfDay(for: $0)).day ?? 0)
        } ?? 0

        let activeTransactions = snapshot.transactions.filter(\.isActive)
        let lifetime = activeTransactions
            .filter { $0.productKind == .lifetimeUnlock }
            .sorted { $0.purchaseDate < $1.purchaseDate }
            .first

        let cutoffDate = lifetime.flatMap {
            updateCutoffDate(lifetimePurchaseDate: $0.purchaseDate, transactions: activeTransactions, calendar: calendar)
        }
        let offlineGraceEndsAt = snapshot.lastVerifiedAt.flatMap {
            calendar.date(byAdding: .day, value: mobileLicenseOfflineGraceDays, to: $0)
        }
        let verifiedPaidAccess = lifetime != nil && offlineGraceEndsAt.map { now <= $0 } == true
        let failedAfterVerification = snapshot.lastVerificationFailedAt.map { failedAt in
            guard let verifiedAt = snapshot.lastVerifiedAt else {
                return false
            }
            return failedAt >= verifiedAt
        } ?? false

        let reason: MobileLicenseAccessReason
        if trialActive {
            reason = .trial
        } else if verifiedPaidAccess {
            reason = failedAfterVerification ? .offlineGrace : .lifetime
        } else {
            reason = .locked
        }

        let unlocked: Set<MobileLicenseFeatureID>
        switch reason {
        case .trial:
            unlocked = Set(features.map(\.id))
        case .lifetime, .offlineGrace:
            unlocked = Set(features.filter { feature in
                guard let cutoffDate else {
                    return false
                }
                return feature.releaseDate <= cutoffDate
            }.map(\.id))
        case .locked:
            unlocked = []
        }

        return MobileLicenseDecision(
            reason: reason,
            trialStartDate: snapshot.trialStartDate,
            trialEndsAt: trialEndsAt,
            trialDaysRemaining: trialDaysRemaining,
            hasLifetimeUnlock: lifetime != nil,
            updateCutoffDate: cutoffDate,
            offlineGraceEndsAt: offlineGraceEndsAt,
            unlockedFeatureIDs: unlocked
        )
    }

    public static func updateCutoffDate(
        lifetimePurchaseDate: Date,
        transactions: [MobileLicenseTransaction],
        calendar: Calendar = mobileLicenseCalendar
    ) -> Date? {
        guard var cutoff = calendar.date(byAdding: .year, value: 1, to: lifetimePurchaseDate) else {
            return nil
        }
        let paidUpdates = transactions
            .filter { transaction in
                if case .paidUpdate = transaction.productKind {
                    return transaction.isActive
                }
                return false
            }
            .sorted { $0.purchaseDate < $1.purchaseDate }

        for update in paidUpdates {
            let extensionStart = max(cutoff, update.purchaseDate)
            guard let nextCutoff = calendar.date(byAdding: .year, value: 1, to: extensionStart) else {
                return cutoff
            }
            cutoff = nextCutoff
        }
        return cutoff
    }
}

public enum MobileLicenseSnapshotStore {
    public static func load(
        defaults: UserDefaults = UserDefaults(suiteName: defaultAppGroupIdentifier) ?? .standard,
        key: String = mobileLicenseSnapshotDefaultsKey
    ) -> MobileLicenseSnapshot {
        guard
            let data = defaults.data(forKey: key),
            let snapshot = try? JSONDecoder().decode(MobileLicenseSnapshot.self, from: data)
        else {
            return MobileLicenseSnapshot()
        }
        return snapshot
    }

    public static func save(
        _ snapshot: MobileLicenseSnapshot,
        defaults: UserDefaults = UserDefaults(suiteName: defaultAppGroupIdentifier) ?? .standard,
        key: String = mobileLicenseSnapshotDefaultsKey
    ) {
        if let data = try? JSONEncoder().encode(snapshot) {
            defaults.set(data, forKey: key)
        }
    }
}

public enum MobileLicenseRuntimeError: Error, LocalizedError {
    case locked

    public var errorDescription: String? {
        switch self {
        case .locked:
            return "The trial has ended. Purchase or restore the lifetime unlock to keep using clambhook."
        }
    }
}

public enum MobileLicenseRuntimeGuard {
    public static func decision(
        groupIdentifier: String = defaultAppGroupIdentifier,
        now: Date = Date()
    ) -> MobileLicenseDecision {
        let defaults = UserDefaults(suiteName: groupIdentifier) ?? .standard
        return MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshotStore.load(defaults: defaults),
            now: now
        )
    }

    public static func requireAppAccess(
        groupIdentifier: String = defaultAppGroupIdentifier,
        now: Date = Date()
    ) throws {
        guard decision(groupIdentifier: groupIdentifier, now: now).canUseApp else {
            throw MobileLicenseRuntimeError.locked
        }
    }

    public static func requireFeatureAccess(
        _ featureID: MobileLicenseFeatureID,
        groupIdentifier: String = defaultAppGroupIdentifier,
        now: Date = Date()
    ) throws {
        guard decision(groupIdentifier: groupIdentifier, now: now).canUseFeature(featureID) else {
            throw MobileLicenseRuntimeError.locked
        }
    }
}

public let mobileLicenseCalendar: Calendar = {
    var calendar = Calendar(identifier: .gregorian)
    calendar.timeZone = TimeZone(secondsFromGMT: 0) ?? .gmt
    return calendar
}()

public func mobileLicenseUTCDate(year: Int, month: Int, day: Int) -> Date {
    DateComponents(calendar: mobileLicenseCalendar, timeZone: mobileLicenseCalendar.timeZone, year: year, month: month, day: day).date!
}
