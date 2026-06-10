import ClambhookShared
import SwiftUI

#if os(iOS)
import StoreKit

struct PremiumPurchasesSection: View {
    @ObservedObject var manager: StoreKitEntitlementManager

    var body: some View {
        Section("Purchases") {
            ProductStatePanel(decision: manager.decision)

            ForEach(manager.purchaseOfferProducts, id: \.id) { product in
                Button {
                    Task { await manager.purchase(product) }
                } label: {
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(product.displayName)
                                .font(.body.weight(.medium))
                            Text(product.description)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                                .lineLimit(2)
                        }
                        Spacer()
                        if manager.purchasingProductIDs.contains(product.id) {
                            ProgressView()
                        } else {
                            Text(product.displayPrice)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
                .disabled(manager.isLoading || manager.purchasingProductIDs.contains(product.id))
            }

            Button {
                Task { await manager.restorePurchases() }
            } label: {
                Label("Restore Purchases", systemImage: "arrow.clockwise")
            }
            .disabled(manager.isLoading)

            Button {
                Task { await manager.repairPurchaseHistory() }
            } label: {
                Label("Repair Purchase History", systemImage: "wrench.and.screwdriver")
            }
            .disabled(manager.isLoading)

            if manager.isLoading {
                ProgressView()
            }

            if !manager.statusMessage.isEmpty {
                Text(manager.statusMessage)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
        .task {
            if manager.products.isEmpty {
                await manager.refreshProducts()
            }
        }
    }
}

private struct ProductStatePanel: View {
    var decision: MobileLicenseDecision

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            ForEach(MobileLicenseProductStateBuilder.states(for: decision)) { state in
                ProductStateRow(state: state)
            }
        }
    }
}

private struct ProductStateRow: View {
    var state: MobileLicenseProductState

    var body: some View {
        Label {
            VStack(alignment: .leading, spacing: 2) {
                Text(state.title)
                    .font(.body.weight(.semibold))
                Text(state.detail)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        } icon: {
            Image(systemName: systemImage)
                .foregroundStyle(tint)
        }
    }

    private var systemImage: String {
        switch state.kind {
        case .trial:
            return "clock"
        case .lifetimeUnlocked:
            return "checkmark.seal.fill"
        case .paidUpdateWindow:
            return "calendar"
        case .newFeaturesLocked:
            return "lock.fill"
        }
    }

    private var tint: Color {
        if state.isActive {
            return state.kind == .newFeaturesLocked ? .orange : .green
        }
        switch state.kind {
        case .trial, .lifetimeUnlocked, .paidUpdateWindow:
            return .secondary
        case .newFeaturesLocked:
            return .red
        }
    }
}
#endif
