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

public enum MobileLicenseTransactionOwnership: String, Codable, Equatable, Sendable {
    case purchased
    case familyShared
}

public struct MobileLicenseTransaction: Codable, Equatable, Sendable {
    public var productID: String
    public var purchaseDate: Date
    public var revocationDate: Date?
    public var ownershipType: MobileLicenseTransactionOwnership

    public init(
        productID: String,
        purchaseDate: Date,
        revocationDate: Date? = nil,
        ownershipType: MobileLicenseTransactionOwnership = .purchased
    ) {
        self.productID = productID
        self.purchaseDate = purchaseDate
        self.revocationDate = revocationDate
        self.ownershipType = ownershipType
    }

    public var productKind: MobileLicenseProductKind {
        MobilePurchaseCatalog.productKind(for: productID)
    }

    public var isActive: Bool {
        revocationDate == nil
    }

    enum CodingKeys: String, CodingKey {
        case productID
        case purchaseDate
        case revocationDate
        case ownershipType
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.productID = try container.decode(String.self, forKey: .productID)
        self.purchaseDate = try container.decode(Date.self, forKey: .purchaseDate)
        self.revocationDate = try container.decodeIfPresent(Date.self, forKey: .revocationDate)
        self.ownershipType = try container.decodeIfPresent(MobileLicenseTransactionOwnership.self, forKey: .ownershipType) ?? .purchased
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
        let trialEndsAt = snapshot.trialStartDate.flatMap { mobileLicenseTrialEndDate(start: $0, calendar: calendar) }
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
        let activeOfflineGraceEndsAt = offlineGraceEndDate(snapshot: snapshot, calendar: calendar).flatMap { endsAt in
            now < endsAt ? endsAt : nil
        }
        let reason: MobileLicenseAccessReason
        if trialActive {
            reason = .trial
        } else if lifetime != nil, activeOfflineGraceEndsAt != nil {
            reason = .offlineGrace
        } else if lifetime != nil {
            reason = .lifetime
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
            offlineGraceEndsAt: reason == .offlineGrace ? activeOfflineGraceEndsAt : nil,
            unlockedFeatureIDs: unlocked
        )
    }

    private static func offlineGraceEndDate(
        snapshot: MobileLicenseSnapshot,
        calendar: Calendar
    ) -> Date? {
        guard let failedAt = snapshot.lastVerificationFailedAt else {
            return nil
        }
        if let verifiedAt = snapshot.lastVerifiedAt, failedAt < verifiedAt {
            return nil
        }
        return calendar.date(byAdding: .day, value: mobileLicenseOfflineGraceDays, to: failedAt)
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

public enum MobileLicenseTrialStore {
    public static let trialAccount = "trial-start-date"

    public static func resolvedSnapshot(
        snapshot: MobileLicenseSnapshot,
        credentialStore: CredentialStoring,
        now: Date = Date()
    ) -> MobileLicenseSnapshot {
        var next = snapshot
        if let stored = try? credentialStore.readToken(account: trialAccount),
           let date = dateFormatter.date(from: stored) {
            next.trialStartDate = date
        } else if let existing = snapshot.trialStartDate {
            try? credentialStore.saveToken(dateFormatter.string(from: existing), account: trialAccount)
            next.trialStartDate = existing
        } else {
            try? credentialStore.saveToken(dateFormatter.string(from: now), account: trialAccount)
            next.trialStartDate = now
        }
        next.cachedAt = now
        return next
    }

    public static func formattedTrialStartDate(_ date: Date) -> String {
        dateFormatter.string(from: date)
    }

    private static let dateFormatter: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()
}

public enum MobileLicenseCopy {
    public static func paidUpdatePolicy(cutoffDate: Date) -> String {
        "The macOS license includes feature updates through \(cutoffDate.formatted(date: .abbreviated, time: .omitted)). Paid updates unlock later feature releases. Bug fixes/security fixes remain included."
    }
}

public enum MobileLicenseProductStateKind: String, Codable, Equatable, Sendable {
    case trial
    case lifetimeUnlocked
    case paidUpdateWindow
    case newFeaturesLocked
}

public struct MobileLicenseProductState: Identifiable, Equatable, Sendable {
    public var kind: MobileLicenseProductStateKind
    public var title: String
    public var detail: String
    public var isActive: Bool

    public var id: MobileLicenseProductStateKind { kind }

    public init(kind: MobileLicenseProductStateKind, title: String, detail: String, isActive: Bool) {
        self.kind = kind
        self.title = title
        self.detail = detail
        self.isActive = isActive
    }
}

public enum MobileLicenseProductStateBuilder {
    public static func states(
        for decision: MobileLicenseDecision,
        features: [MobileLicenseFeature] = MobileLicenseFeatureCatalog.features
    ) -> [MobileLicenseProductState] {
        var states: [MobileLicenseProductState] = []

        if let trialEndsAt = decision.trialEndsAt {
            states.append(MobileLicenseProductState(
                kind: .trial,
                title: "Free access",
                detail: decision.isTrialActive
                    ? "Server-controlled free access ends \(trialEndsAt.formatted(date: .abbreviated, time: .omitted))."
                    : "Free access ended \(trialEndsAt.formatted(date: .abbreviated, time: .omitted)).",
                isActive: decision.isTrialActive
            ))
        } else {
            states.append(MobileLicenseProductState(
                kind: .trial,
                title: "Free access",
                detail: "Server-controlled free access starts the first time this app records an access date.",
                isActive: false
            ))
        }

        states.append(MobileLicenseProductState(
            kind: .lifetimeUnlocked,
            title: "macOS license",
            detail: decision.hasLifetimeUnlock
                ? "Included features remain usable after the paid update window ends."
                : "Buy or activate a ClambHook macOS license to keep using clambhook after free access.",
            isActive: decision.hasLifetimeUnlock
        ))

        if let cutoffDate = decision.updateCutoffDate {
            states.append(MobileLicenseProductState(
                kind: .paidUpdateWindow,
                title: "Paid-update window through \(cutoffDate.formatted(date: .abbreviated, time: .omitted))",
                detail: "Features released on or before this date are included.",
                isActive: decision.hasLifetimeUnlock
            ))
        } else {
            states.append(MobileLicenseProductState(
                kind: .paidUpdateWindow,
                title: "Paid-update window through DATE",
                detail: "A macOS license sets this date to the purchase date plus one year.",
                isActive: false
            ))
        }

        let lockedFeatures: [MobileLicenseFeature]
        if let cutoffDate = decision.updateCutoffDate {
            lockedFeatures = features.filter { $0.releaseDate > cutoffDate }
        } else {
            lockedFeatures = []
        }
        states.append(MobileLicenseProductState(
            kind: .newFeaturesLocked,
            title: "New features locked until update",
            detail: lockedFeatures.isEmpty
                ? "Feature releases after the paid-update window require a paid update. Bug fixes/security fixes remain included."
                : "Locked feature releases: \(lockedFeatures.map(\.displayName).joined(separator: ", ")).",
            isActive: !lockedFeatures.isEmpty
        ))

        return states
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
            return "Free access has ended. Buy or activate a ClambHook macOS license to keep using clambhook."
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

public func mobileLicenseTrialEndDate(start: Date, calendar: Calendar = mobileLicenseCalendar) -> Date? {
    calendar.date(byAdding: .month, value: mobileLicenseTrialMonths, to: start)
}

public func mobileLicenseUTCDate(year: Int, month: Int, day: Int) -> Date {
    DateComponents(calendar: mobileLicenseCalendar, timeZone: mobileLicenseCalendar.timeZone, year: year, month: month, day: day).date!
}
