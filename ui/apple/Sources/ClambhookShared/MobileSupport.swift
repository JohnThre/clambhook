import Foundation

public struct MobilePurchaseProduct: Identifiable, Equatable {
    public let id: String
    public let displayName: String

    public init(id: String, displayName: String) {
        self.id = id
        self.displayName = displayName
    }
}

public enum MobilePurchaseCatalog {
    public static let lifetimeUnlockID = "org.jpfchang.clambhook.unlock.lifetime"
    public static let featureUpdate2027ID = "org.jpfchang.clambhook.feature_update.2027"
    public static let featureUpdatePrefix = "org.jpfchang.clambhook.feature_update."

    public static let products: [MobilePurchaseProduct] = [
        MobilePurchaseProduct(id: lifetimeUnlockID, displayName: "ClambHook Lifetime Unlock"),
        MobilePurchaseProduct(id: featureUpdate2027ID, displayName: "ClambHook 2027 Feature Update"),
    ]

    public static let productIDs = products.map(\.id)

    public static func orderedIDs<T: Sequence>(_ ids: T) -> [String] where T.Element == String {
        let knownOrder = Dictionary(uniqueKeysWithValues: productIDs.enumerated().map { ($1, $0) })
        return ids.sorted { lhs, rhs in
            (knownOrder[lhs] ?? Int.max, lhs) < (knownOrder[rhs] ?? Int.max, rhs)
        }
    }

    public static func productKind(for id: String) -> MobileLicenseProductKind {
        if id == lifetimeUnlockID {
            return .lifetimeUnlock
        }
        guard id.hasPrefix(featureUpdatePrefix) else {
            return .unknown
        }
        let suffix = String(id.dropFirst(featureUpdatePrefix.count))
        guard let year = Int(suffix), suffix.count == 4 else {
            return .unknown
        }
        return .paidUpdate(year: year)
    }
}
