import Foundation

public struct MobileSupportProduct: Identifiable, Equatable {
    public let id: String
    public let displayName: String

    public init(id: String, displayName: String) {
        self.id = id
        self.displayName = displayName
    }
}

public enum MobileSupportCatalog {
    public static let products: [MobileSupportProduct] = [
        MobileSupportProduct(id: "org.jpfchang.clambhook.support.small", displayName: "Small Support"),
        MobileSupportProduct(id: "org.jpfchang.clambhook.support.medium", displayName: "Medium Support"),
        MobileSupportProduct(id: "org.jpfchang.clambhook.support.large", displayName: "Large Support"),
    ]

    public static let productIDs = products.map(\.id)

    public static func orderedIDs<T: Sequence>(_ ids: T) -> [String] where T.Element == String {
        let knownOrder = Dictionary(uniqueKeysWithValues: productIDs.enumerated().map { ($1, $0) })
        return ids.sorted { lhs, rhs in
            (knownOrder[lhs] ?? Int.max, lhs) < (knownOrder[rhs] ?? Int.max, rhs)
        }
    }
}
