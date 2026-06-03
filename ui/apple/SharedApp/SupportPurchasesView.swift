import ClambhookShared
import SwiftUI

#if os(iOS) || os(visionOS)
import StoreKit

struct PremiumPurchasesSection: View {
    var body: some View {
        Section("Purchases") {
            StoreView(ids: MobilePurchaseCatalog.productIDs)
        }
    }
}
#endif
