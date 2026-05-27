import ClambhookShared
import SwiftUI

#if os(iOS) || os(visionOS)
import StoreKit

struct SupportPurchasesSection: View {
    var body: some View {
        Section("Support") {
            StoreView(ids: MobileSupportCatalog.productIDs)
        }
    }
}
#endif
