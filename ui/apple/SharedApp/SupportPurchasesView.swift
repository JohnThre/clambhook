// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

import ClambhookShared
import SwiftUI

struct ProductStatePanel: View {
    var decision: MobileLicenseDecision

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            ForEach(MobileLicenseProductStateBuilder.states(for: decision)) { state in
                ProductStateRow(state: state)
            }
        }
    }
}

struct SupporterBadge: View {
    var decision: MobileLicenseDecision

    var body: some View {
        if decision.supporterTier != .none {
            Label(
                decision.supporterActive
                    ? "\(decision.supporterTier.displayName) · Active"
                    : decision.supporterTier.displayName,
                systemImage: decision.supporterActive ? "star.circle.fill" : "star.circle"
            )
            .font(.caption.weight(.semibold))
            .padding(.horizontal, 9)
            .padding(.vertical, 5)
            .foregroundStyle(decision.supporterActive ? Color.white : supporterColor)
            .background(
                decision.supporterActive ? supporterColor : supporterColor.opacity(0.14),
                in: Capsule()
            )
            .accessibilityLabel("\(decision.supporterTier.displayName), \(decision.supporterActive ? "subscription active" : "perpetual fallback")")
        }
    }

    private var supporterColor: Color {
        switch decision.supporterTier {
        case .copper: return .brown
        case .silver: return .gray
        case .gold: return .orange
        case .none: return .secondary
        }
    }
}

struct SupporterThankYouCard: View {
    var decision: MobileLicenseDecision

    var body: some View {
        if decision.supporterTier != .none {
            VStack(alignment: .leading, spacing: 8) {
                SupporterBadge(decision: decision)
                Text("Thank you for supporting independent ClambHook development")
                    .font(.headline)
                Text(decision.supporterActive
                    ? "Your paid-through period is current. You receive every compatible release published during this term."
                    : "Your supporter badge and compatible fallback remain yours. Resubscribe whenever you want access to later releases.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            .padding(14)
            .background(.quaternary, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
    }
}

struct DonationLinksPanel: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Support ClambHook")
                .font(.headline)
            Text("Donations never create a license, extend a subscription, change a supporter badge, or affect support priority.")
                .font(.footnote)
                .foregroundStyle(.secondary)
            ViewThatFits {
                HStack { links }
                VStack(alignment: .leading) { links }
            }
        }
    }

    @ViewBuilder private var links: some View {
        Link("Ko-fi", destination: clambHookKoFiURL)
        Link("Liberapay", destination: clambHookLiberapayURL)
        Link("IssueHunt", destination: clambHookIssueHuntURL)
        Link("Donate crypto", destination: clambHookNowPaymentsDonationURL)
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

#if os(macOS)
struct MacLicenseSection: View {
    @ObservedObject var manager: MacLicenseManager
    @State private var licenseKey = ""
    @State private var email = ""

    var body: some View {
        Section("License") {
            SupporterThankYouCard(decision: manager.decision)
            ProductStatePanel(decision: manager.decision)

            HStack {
                Label(deviceSummary, systemImage: "desktopcomputer")
                Spacer()
                Text("\(manager.deviceState.activeDeviceCount)/\(manager.deviceState.maxActiveDevices) active")
                    .foregroundStyle(.secondary)
            }

            SecureField("License key", text: $licenseKey)
            TextField("Email", text: $email)

            HStack {
                Button {
                    Task { await manager.activate(licenseKey: licenseKey, email: email) }
                } label: {
                    Label("Activate", systemImage: "checkmark.seal")
                }
                .disabled(manager.isLoading || licenseKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)

                Button {
                    Task { await manager.deactivateCurrentDevice() }
                } label: {
                    Label("Deactivate", systemImage: "minus.circle")
                }
                .disabled(manager.isLoading || !manager.deviceState.isCurrentDeviceActive)
            }

            HStack {
                Button {
                    Task { await manager.reactivateCurrentDevice() }
                } label: {
                    Label("Reactivate", systemImage: "arrow.clockwise.circle")
                }
                .disabled(manager.isLoading || !manager.deviceState.canReactivateCurrentDevice)

                Button {
                    Task { await manager.transferCurrentDevice() }
                } label: {
                    Label("Transfer", systemImage: "arrow.right.arrow.left")
                }
                .disabled(manager.isLoading || !manager.deviceState.canTransferCurrentDevice)
            }

            Link(destination: defaultLicensePurchaseURL) {
                Label("Buy subscription - USD \(MobileLicenseCommercialTerms.annualSubscriptionPriceUSD)/year", systemImage: "cart")
            }

            Link(destination: defaultLicensePortalURL) {
                Label("Manage Subscription", systemImage: "safari")
            }

            DonationLinksPanel()

            if manager.isLoading {
                ProgressView()
            }

            if !manager.statusMessage.isEmpty {
                Text(manager.statusMessage)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
        .onAppear {
            licenseKey = manager.savedLicenseKey()
            email = manager.savedEmail()
        }
    }

    private var deviceSummary: String {
        if let device = manager.deviceState.currentDevice {
            switch device.status {
            case .active:
                return "\(device.displayName) is active"
            case .deactivated:
                return "\(device.displayName) is deactivated"
            }
        }
        return "This Mac is not activated"
    }
}
#endif
