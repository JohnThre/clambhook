import AppKit
import ClambhookShared
import SwiftUI

// MARK: - Root

struct OnboardingView: View {
    @ObservedObject var model: AppleAppModel
    @ObservedObject var manager: OnboardingManager

    var body: some View {
        VStack(spacing: 0) {
            stepContent
            Divider()
            navigationBar
        }
        .frame(width: 560, height: 480)
    }

    @ViewBuilder
    private var stepContent: some View {
        switch manager.currentStep {
        case .welcome:
            OnboardingWelcomeStep(model: model)
        case .routingMode:
            OnboardingRoutingModeStep(model: model)
        case .profileImport:
            OnboardingProfileImportStep(model: model)
        case .httpsCA:
            OnboardingHTTPSCAStep(model: model)
        case .done:
            OnboardingDoneStep(model: model)
        }
    }

    private var navigationBar: some View {
        HStack {
            if manager.currentStep != .welcome {
                Button("Back") { manager.back() }
                    .keyboardShortcut(.cancelAction)
            }
            Spacer()
            stepIndicator
            Spacer()
            if manager.currentStep == .done {
                Button("Finish") { manager.complete() }
                    .keyboardShortcut(.defaultAction)
                    .buttonStyle(.borderedProminent)
            } else {
                skipButton
                Button("Continue") { manager.advance() }
                    .keyboardShortcut(.defaultAction)
                    .buttonStyle(.borderedProminent)
            }
        }
        .padding(.horizontal, 20)
        .padding(.vertical, 14)
    }

    private var stepIndicator: some View {
        HStack(spacing: 6) {
            ForEach(OnboardingStep.allCases, id: \.self) { step in
                Circle()
                    .fill(step == manager.currentStep ? Color.accentColor : Color.secondary.opacity(0.3))
                    .frame(width: 6, height: 6)
            }
        }
    }

    @ViewBuilder
    private var skipButton: some View {
        switch manager.currentStep {
        case .profileImport, .httpsCA:
            Button("Skip") { manager.advance() }
                .foregroundStyle(.secondary)
        default:
            EmptyView()
        }
    }
}

// MARK: - Step container

private struct OnboardingStepContainer<Content: View>: View {
    var systemImage: String
    var title: String
    var subtitle: String
    @ViewBuilder var content: () -> Content

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 14) {
                Image(systemName: systemImage)
                    .font(.system(size: 36))
                    .foregroundStyle(.tint)
                    .frame(width: 48, height: 48)
                VStack(alignment: .leading, spacing: 4) {
                    Text(title)
                        .font(.title2.bold())
                    Text(subtitle)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .padding(.bottom, 20)
            content()
        }
        .padding(28)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }
}

// MARK: - Welcome step

private struct OnboardingWelcomeStep: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        OnboardingStepContainer(
            systemImage: "network",
            title: "Welcome to ClambHook",
            subtitle: "Route, inspect, and control your Mac's network traffic."
        ) {
            VStack(alignment: .leading, spacing: 14) {
                trialStatusRow
                licenseActivationArea
            }
        }
    }

    @ViewBuilder
    private var trialStatusRow: some View {
        let decision = model.licenseManager.decision
        switch decision.reason {
        case .trial:
            OnboardingInfoRow(
                systemImage: "clock.fill",
                tint: .orange,
                title: "Trial active",
                detail: trialDetail(decision)
            )
        case .lifetime:
            OnboardingInfoRow(
                systemImage: "checkmark.seal.fill",
                tint: .green,
                title: "License active",
                detail: "Full access unlocked on this Mac."
            )
        case .offlineGrace:
            OnboardingInfoRow(
                systemImage: "wifi.slash",
                tint: .orange,
                title: "Offline grace period",
                detail: offlineGraceDetail(decision)
            )
        case .locked:
            OnboardingInfoRow(
                systemImage: "lock.fill",
                tint: .red,
                title: "Trial ended",
                detail: "Activate a license key below to continue using ClambHook."
            )
        }
    }

    private func trialDetail(_ d: MobileLicenseDecision) -> String {
        if d.trialDaysRemaining > 0 {
            return "\(d.trialDaysRemaining) day\(d.trialDaysRemaining == 1 ? "" : "s") remaining in your free trial."
        }
        return "Your trial ends today."
    }

    private func offlineGraceDetail(_ d: MobileLicenseDecision) -> String {
        if let ends = d.offlineGraceEndsAt {
            let formatter = DateFormatter()
            formatter.dateStyle = .medium
            formatter.timeStyle = .none
            return "License grace period active until \(formatter.string(from: ends))."
        }
        return "License grace period active."
    }

    @ViewBuilder
    private var licenseActivationArea: some View {
        if model.licenseManager.decision.reason != .lifetime {
            OnboardingLicenseActivationInline(manager: model.licenseManager)
        }
    }
}

// MARK: - Inline license activation

private struct OnboardingLicenseActivationInline: View {
    @ObservedObject var manager: MacLicenseManager
    @State private var licenseKey = ""
    @State private var email = ""
    @State private var expanded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Button {
                expanded.toggle()
            } label: {
                Label(
                    expanded ? "Hide license activation" : "Activate a license key",
                    systemImage: expanded ? "chevron.up" : "checkmark.seal"
                )
                .font(.subheadline)
            }
            .buttonStyle(.borderless)

            if expanded {
                SecureField("License key", text: $licenseKey)
                    .textFieldStyle(.roundedBorder)
                TextField("Email (optional)", text: $email)
                    .textFieldStyle(.roundedBorder)
                HStack(spacing: 8) {
                    Button("Activate") {
                        Task { await manager.activate(licenseKey: licenseKey, email: email) }
                    }
                    .buttonStyle(.bordered)
                    .disabled(manager.isLoading || licenseKey.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    if manager.isLoading {
                        ProgressView().controlSize(.small)
                    }
                    Link("Buy license", destination: defaultLicensePurchaseURL)
                        .font(.subheadline)
                }
                if !manager.statusMessage.isEmpty {
                    Text(manager.statusMessage)
                        .font(.caption)
                        .foregroundStyle(manager.decision.reason == .lifetime ? .green : .secondary)
                }
            }
        }
        .onAppear {
            licenseKey = manager.savedLicenseKey()
            email = manager.savedEmail()
        }
    }
}

// MARK: - Routing mode step

private struct OnboardingRoutingModeStep: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        OnboardingStepContainer(
            systemImage: "arrow.triangle.branch",
            title: "Choose Routing Mode",
            subtitle: "Start with System Proxy or use Enhanced Mode for device-wide TUN routing."
        ) {
            VStack(alignment: .leading, spacing: 14) {
                Picker("Routing mode", selection: $model.settingsStore.settings.routingMode) {
                    Text("System Proxy").tag(AppRoutingMode.systemProxy)
                    Text("Enhanced Mode").tag(AppRoutingMode.enhancedTUN)
                }
                .pickerStyle(.segmented)

                modeDescription
                helperStatus
            }
        }
        .onChange(of: model.settingsStore.settings.routingMode) { _, mode in
            if mode.requiresPrivilegedHelper {
                model.settingsStore.settings.usePrivilegedHelper = true
            }
            model.applySettings()
        }
    }

    @ViewBuilder
    private var modeDescription: some View {
        if model.settingsStore.settings.routingMode == .enhancedTUN {
            OnboardingInfoRow(
                systemImage: "network.badge.shield.half.filled",
                tint: .blue,
                title: "Enhanced Mode",
                detail: "Runs the privileged daemon with a utun interface for device-wide routing. This is the macOS equivalent of Surge's Enhanced Mode."
            )
        } else {
            OnboardingInfoRow(
                systemImage: "globe",
                tint: .green,
                title: "System Proxy",
                detail: "Applies macOS HTTP, HTTPS, and SOCKS proxy settings. Apps that ignore system proxy settings will not be routed."
            )
        }
    }

    @ViewBuilder
    private var helperStatus: some View {
        if model.settingsStore.settings.routingMode == .enhancedTUN {
            Divider()
            VStack(alignment: .leading, spacing: 8) {
                Label(
                    model.privilegedHelperManager.serviceStatus.label,
                    systemImage: helperStatusImage
                )
                .foregroundStyle(helperStatusColor)

                HStack {
                    Button {
                        Task { await model.privilegedHelperManager.registerHelper() }
                    } label: {
                        Label("Install Helper", systemImage: "lock.shield")
                    }
                    .buttonStyle(.bordered)

                    Button {
                        model.privilegedHelperManager.openSystemSettings()
                    } label: {
                        Label("Open System Settings", systemImage: "gear")
                    }
                    .buttonStyle(.bordered)
                }
                Text("Enhanced Mode requires this helper so the daemon can create utun routes and restore DNS settings.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }

    private var helperStatusImage: String {
        switch model.privilegedHelperManager.serviceStatus {
        case .enabled:
            return "checkmark.circle.fill"
        case .requiresApproval:
            return "exclamationmark.triangle.fill"
        case .notFound:
            return "questionmark.circle"
        case .notRegistered:
            return "lock.shield"
        case .unknown:
            return "questionmark.circle"
        }
    }

    private var helperStatusColor: Color {
        switch model.privilegedHelperManager.serviceStatus {
        case .enabled:
            return .green
        case .requiresApproval:
            return .orange
        case .notFound, .unknown:
            return .red
        case .notRegistered:
            return .secondary
        }
    }
}

// MARK: - Profile import step

private struct OnboardingProfileImportStep: View {
    @ObservedObject var model: AppleAppModel
    @State private var importedProfiles: [String] = []
    @State private var importError = ""
    @State private var importSuccess = false
    @State private var pendingText = ""

    var body: some View {
        OnboardingStepContainer(
            systemImage: "tray.and.arrow.down.fill",
            title: "Import a Profile",
            subtitle: "Import a ClambHook TOML configuration file containing your proxy profiles and rules. You can skip this and import later."
        ) {
            VStack(alignment: .leading, spacing: 14) {
                if importSuccess {
                    OnboardingInfoRow(
                        systemImage: "checkmark.circle.fill",
                        tint: .green,
                        title: "Config imported",
                        detail: importedProfiles.isEmpty
                            ? "Configuration staged for import."
                            : "Profiles queued: \(importedProfiles.joined(separator: ", "))."
                    )
                } else {
                    OnboardingInfoRow(
                        systemImage: "doc.badge.plus",
                        tint: .accentColor,
                        title: "No config imported",
                        detail: "Click below to select a .toml configuration file."
                    )
                }

                HStack(spacing: 10) {
                    Button {
                        pickFile()
                    } label: {
                        Label("Choose Config File", systemImage: "folder")
                    }
                    .buttonStyle(.bordered)

                    if importSuccess {
                        Button(role: .destructive) {
                            importedProfiles = []
                            pendingText = ""
                            importError = ""
                            importSuccess = false
                        } label: {
                            Label("Clear", systemImage: "xmark.circle")
                        }
                        .buttonStyle(.borderless)
                    }
                }

                if !importError.isEmpty {
                    Text(importError)
                        .font(.caption)
                        .foregroundStyle(.red)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
        }
        .onDisappear {
            applyPendingImport()
        }
    }

    private func pickFile() {
        let panel = NSOpenPanel()
        panel.title = "Import clambhook config"
        panel.allowedContentTypes = [.init(filenameExtension: "toml") ?? .data]
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = false
        guard panel.runModal() == .OK, let url = panel.url else { return }
        do {
            let text = try String(contentsOf: url, encoding: .utf8)
            _ = try TunnelImportDecoder.decode(text)
            pendingText = text
            importedProfiles = profileNames(in: text)
            importError = ""
            importSuccess = true
        } catch {
            importError = error.localizedDescription
            importSuccess = false
        }
    }

    private func applyPendingImport() {
        guard !pendingText.isEmpty else { return }
        let configPath = model.settingsStore.settings.daemonConfigPath
        if !configPath.isEmpty, (try? model.writeConfigFile(pendingText)) != nil {
            model.reloadDaemon()
        } else {
            _ = try? model.attention.captureImport(rawValue: pendingText, source: .file)
        }
    }

    private func profileNames(in toml: String) -> [String] {
        var names: [String] = []
        for line in toml.components(separatedBy: .newlines) {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            if trimmed.hasPrefix("name") {
                let parts = trimmed.components(separatedBy: "=")
                if parts.count >= 2 {
                    let name = parts[1]
                        .trimmingCharacters(in: .whitespaces)
                        .trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
                    if !name.isEmpty { names.append(name) }
                }
            }
        }
        return names
    }
}

// MARK: - HTTPS CA step

private struct OnboardingHTTPSCAStep: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        OnboardingStepContainer(
            systemImage: "lock.shield",
            title: "HTTPS Certificate",
            subtitle: "To inspect HTTPS traffic, ClambHook uses a local Certificate Authority. Install it to trust HTTPS captures."
        ) {
            VStack(alignment: .leading, spacing: 14) {
                if model.developerCAPEMText.isEmpty {
                    OnboardingInfoRow(
                        systemImage: "info.circle",
                        tint: .secondary,
                        title: "CA not available yet",
                        detail: "Start the daemon and enable HTTPS capture in Settings to generate the CA certificate. You can install it later from Settings \u{203A} Developer."
                    )
                } else {
                    caAvailableContent
                }
            }
        }
    }

    @ViewBuilder
    private var caAvailableContent: some View {
        let cert = model.certificateManager
        if !cert.fingerprint.isEmpty {
            VStack(alignment: .leading, spacing: 6) {
                Text("Certificate fingerprint (SHA-256)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Text(cert.fingerprint)
                    .font(.system(.caption, design: .monospaced))
                    .textSelection(.enabled)
                    .lineLimit(4)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }

        Text("Installing the CA certificate trusts it only in your login keychain for SSL connections.")
            .font(.caption)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)

        HStack(spacing: 10) {
            Button {
                model.certificateManager.install(pem: model.developerCAPEMText)
            } label: {
                Label("Trust Certificate", systemImage: "lock.shield.fill")
            }
            .buttonStyle(.bordered)
            .disabled(cert.isWorking || model.developerCAPEMText.isEmpty)

            if cert.isWorking {
                ProgressView().controlSize(.small)
            }
        }

        if !cert.statusMessage.isEmpty {
            Text(cert.statusMessage)
                .font(.caption)
                .foregroundStyle(.secondary)
        }

        warningBox
    }

    private var warningBox: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "exclamationmark.triangle")
                .foregroundStyle(.orange)
                .font(.subheadline)
            Text("Only install this certificate if you intentionally use HTTPS inspection. Remove it from Keychain Access when you no longer need it.")
                .font(.caption)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(10)
        .background(Color.orange.opacity(0.08))
        .clipShape(RoundedRectangle(cornerRadius: 8))
    }
}

// MARK: - Done step

private struct OnboardingDoneStep: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        OnboardingStepContainer(
            systemImage: "checkmark.circle.fill",
            title: "You're ready",
            subtitle: "ClambHook is set up. Connect to start routing traffic."
        ) {
            VStack(alignment: .leading, spacing: 12) {
                summaryRow(
                    systemImage: routingModeImage,
                    tint: .blue,
                    title: routingModeTitle,
                    detail: routingModeDetail
                )
                summaryRow(
                    systemImage: licenseImage,
                    tint: licenseColor,
                    title: licenseTitle,
                    detail: licenseDetail
                )
                Spacer()
                Text("You can adjust all settings at any time from the Settings window.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var routingModeImage: String {
        model.settingsStore.settings.routingMode == .enhancedTUN ? "network.badge.shield.half.filled" : "globe"
    }

    private var routingModeTitle: String {
        model.settingsStore.settings.routingMode == .enhancedTUN ? "Enhanced Mode routing" : "System Proxy routing"
    }

    private var routingModeDetail: String {
        model.settingsStore.settings.routingMode == .enhancedTUN
            ? "Device-wide routing through the privileged daemon and utun."
            : "System proxy mode. Apps that ignore proxy settings will not be routed."
    }

    private var licenseImage: String {
        switch model.licenseManager.decision.reason {
        case .lifetime: return "checkmark.seal.fill"
        case .trial: return "clock"
        case .offlineGrace: return "wifi.slash"
        case .locked: return "lock.fill"
        }
    }

    private var licenseColor: Color {
        switch model.licenseManager.decision.reason {
        case .lifetime: return .green
        case .trial: return .orange
        case .offlineGrace: return .orange
        case .locked: return .red
        }
    }

    private var licenseTitle: String {
        switch model.licenseManager.decision.reason {
        case .lifetime: return "License active"
        case .trial: return "Trial active"
        case .offlineGrace: return "Offline grace period"
        case .locked: return "Trial ended"
        }
    }

    private var licenseDetail: String {
        let d = model.licenseManager.decision
        switch d.reason {
        case .lifetime:
            return "Full access unlocked."
        case .trial:
            return "\(d.trialDaysRemaining) day\(d.trialDaysRemaining == 1 ? "" : "s") remaining."
        case .offlineGrace:
            return "Verify your license when back online."
        case .locked:
            return "Activate a license key in Settings \u{203A} License."
        }
    }

    private func summaryRow(systemImage: String, tint: Color, title: String, detail: String) -> some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: systemImage)
                .foregroundStyle(tint)
                .frame(width: 20)
            VStack(alignment: .leading, spacing: 2) {
                Text(title).font(.subheadline.weight(.semibold))
                Text(detail).font(.caption).foregroundStyle(.secondary)
            }
        }
    }
}

// MARK: - Shared row component

private struct OnboardingInfoRow: View {
    var systemImage: String
    var tint: Color
    var title: String
    var detail: String

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: systemImage)
                .foregroundStyle(tint)
                .frame(width: 20, height: 20)
            VStack(alignment: .leading, spacing: 3) {
                Text(title)
                    .font(.subheadline.weight(.semibold))
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .padding(10)
        .background(Color(nsColor: .quaternarySystemFill))
        .clipShape(RoundedRectangle(cornerRadius: 8))
    }
}
