// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

import AppKit
import ClambhookShared
import SwiftUI

// MARK: - Settings

struct MacSettingsSection: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        AppSettingsView(model: model)
    }
}

// MARK: - License

struct MacLicenseSectionInline: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                SupporterThankYouCard(decision: model.licenseManager.decision)
                ProductStatePanel(decision: model.licenseManager.decision)
                Divider()
                MacLicenseControls(manager: model.licenseManager)
                Divider()
                DonationLinksPanel()
                Divider()
                LegalFooter()
            }
            .padding(20)
        }
    }
}

private struct MacLicenseControls: View {
    @ObservedObject var manager: MacLicenseManager
    @State private var licenseKey = ""
    @State private var email = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Label(deviceSummary, systemImage: "desktopcomputer")
                Spacer()
                Text("\(manager.deviceState.activeDeviceCount)/\(manager.deviceState.maxActiveDevices) active")
                    .foregroundStyle(.secondary)
            }

            SecureField("License key", text: $licenseKey)
                .textFieldStyle(.roundedBorder)
            TextField("Email", text: $email)
                .textFieldStyle(.roundedBorder)

            HStack(spacing: 10) {
                Button {
                    Task { await manager.activate(licenseKey: licenseKey, email: email) }
                } label: {
                    Label("Activate", systemImage: "checkmark.seal")
                }
                .disabled(manager.isLoading || licenseKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)

                Button(role: .destructive) {
                    Task { await manager.deactivateCurrentDevice() }
                } label: {
                    Label("Deactivate", systemImage: "minus.circle")
                }
                .disabled(manager.isLoading || !manager.deviceState.isCurrentDeviceActive)
            }

            HStack(spacing: 10) {
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
            return device.status == .active ? "\(device.displayName) is active" : "\(device.displayName) is deactivated"
        }
        return "This Mac is not activated"
    }
}
