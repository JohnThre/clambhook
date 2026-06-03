import ClambhookShared
import SwiftUI

#if os(iOS)
import StoreKit

struct PremiumPurchasesSection: View {
    @ObservedObject var manager: StoreKitEntitlementManager

    var body: some View {
        Section("Purchases") {
            LicenseStatusView(decision: manager.decision)

            ForEach(manager.products, id: \.id) { product in
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

private struct LicenseStatusView: View {
    var decision: MobileLicenseDecision

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Label(title, systemImage: systemImage)
                .font(.body.weight(.semibold))
                .foregroundStyle(tint)
            Text(detail)
                .font(.footnote)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    private var title: String {
        switch decision.reason {
        case .trial:
            return "Trial active"
        case .lifetime:
            return "Lifetime unlock active"
        case .offlineGrace:
            return "Offline grace active"
        case .locked:
            return "Purchase required"
        }
    }

    private var detail: String {
        switch decision.reason {
        case .trial:
            if let trialEndsAt = decision.trialEndsAt {
                return "Free use ends \(trialEndsAt.formatted(date: .abbreviated, time: .omitted))."
            }
            return "Free use is active."
        case .lifetime:
            if let cutoff = decision.updateCutoffDate {
                return "Features released through \(cutoff.formatted(date: .abbreviated, time: .omitted)) are unlocked."
            }
            return "Purchased features are unlocked."
        case .offlineGrace:
            if let grace = decision.offlineGraceEndsAt {
                return "Cached purchase access is available until \(grace.formatted(date: .abbreviated, time: .omitted))."
            }
            return "Cached purchase access is available while offline."
        case .locked:
            return "The trial has ended. Purchase or restore the lifetime unlock to keep using clambhook."
        }
    }

    private var systemImage: String {
        switch decision.reason {
        case .trial:
            return "clock"
        case .lifetime:
            return "checkmark.seal.fill"
        case .offlineGrace:
            return "wifi.slash"
        case .locked:
            return "lock.fill"
        }
    }

    private var tint: Color {
        switch decision.reason {
        case .trial, .lifetime:
            return .green
        case .offlineGrace:
            return .orange
        case .locked:
            return .red
        }
    }
}
#endif
