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

            Link(destination: URL(string: "https://jpfchang.org/clambhook/buy")!) {
                Label("Buy macOS license - USD \(MobileLicenseCommercialTerms.licensePriceUSD)", systemImage: "cart")
            }

            Link(destination: defaultLicensePortalURL) {
                Label("License Portal", systemImage: "safari")
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
