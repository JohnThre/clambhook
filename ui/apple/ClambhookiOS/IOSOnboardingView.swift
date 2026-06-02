import ClambhookShared
import Foundation
import SwiftUI
import UniformTypeIdentifiers
import UIKit

struct IOSOnboardingView: View {
    @ObservedObject var model: AppleAppModel
    var onComplete: () -> Void
    @AppStorage("org.jpfchang.clambhook.vpnDisclosureAccepted") private var vpnDisclosureAccepted = false
    @State private var step: IOSOnboardingStep = .disclosure
    @State private var showingFileImporter = false
    @State private var message = ""
    @State private var canContinue = false
    @State private var profileDraft = TunnelProfileCreateDraft()
    @State private var stagedReviewItem: InboxImportItem?

    var body: some View {
        NavigationStack {
            stepContent
            .navigationTitle(step.navigationTitle)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                if step.showsBackButton {
                    ToolbarItem(placement: .topBarLeading) {
                        Button("Back") {
                            goBack()
                        }
                    }
                }
            }
            .fileImporter(
                isPresented: $showingFileImporter,
                allowedContentTypes: [.text, .plainText, .data],
                allowsMultipleSelection: false
            ) { result in
                importFromFile(result)
            }
            .task {
                refreshReadinessSilently()
            }
            .sheet(item: $stagedReviewItem, onDismiss: {
                refreshReadinessSilently()
            }) { item in
                NavigationStack {
                    IOSInboxItemDetailView(model: model, itemID: item.id)
                }
            }
        }
    }

    @ViewBuilder
    private var stepContent: some View {
        switch step {
        case .disclosure:
            disclosureStep
        case .setupChoice:
            setupChoiceStep
        case .importConfig:
            importStep
        case .scanQR:
            scanStep
        case .createProfile:
            createProfileStep
        case .ready:
            readyStep
        }
    }

    private var disclosureStep: some View {
        IOSOnboardingScrollStep(
            title: "Set Up clambhook",
            subtitle: "Review VPN data use before adding your first tunnel profile.",
            systemImage: "network.badge.shield.half.filled",
            message: ""
        ) {
            VStack(alignment: .leading, spacing: 14) {
                Text(vpnDataUseDisclosure)
                    .font(.body)
                    .foregroundStyle(.primary)
                    .fixedSize(horizontal: false, vertical: true)

                Link("Privacy Policy", destination: defaultPrivacyPolicyURL)
                    .font(.body.weight(.medium))
            }
        }
        .safeAreaInset(edge: .bottom) {
            IOSOnboardingFooter {
                Button {
                    acceptDisclosure()
                } label: {
                    Text("Continue")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
            }
        }
    }

    private var setupChoiceStep: some View {
        IOSOnboardingScrollStep(
            title: "Choose Setup Method",
            subtitle: "Add one tunnel profile now. More profiles can be managed later.",
            systemImage: "checklist.unchecked",
            message: message
        ) {
            VStack(spacing: 10) {
                IOSOnboardingActionRow(
                    title: "Import",
                    detail: "Open a config file or paste config text.",
                    systemImage: "folder",
                    tint: .blue
                ) {
                    showStep(.importConfig)
                }

                IOSOnboardingActionRow(
                    title: "Scan QR",
                    detail: "Use the camera to read a profile code.",
                    systemImage: "qrcode.viewfinder",
                    tint: .green
                ) {
                    showStep(.scanQR)
                }

                IOSOnboardingActionRow(
                    title: "Create Profile",
                    detail: "Enter server details manually.",
                    systemImage: "plus.circle",
                    tint: .orange
                ) {
                    showStep(.createProfile)
                }
            }
        }
    }

    private var importStep: some View {
        IOSOnboardingScrollStep(
            title: "Import Config",
            subtitle: "Bring in an existing TOML config or compatible profile text.",
            systemImage: "folder",
            message: message
        ) {
            VStack(spacing: 10) {
                IOSOnboardingActionRow(
                    title: "Import From Files",
                    detail: "Select a saved config from Files.",
                    systemImage: "doc.badge.plus",
                    tint: .blue
                ) {
                    showingFileImporter = true
                }

                IOSOnboardingActionRow(
                    title: "Import From Clipboard",
                    detail: "Paste copied config text.",
                    systemImage: "doc.on.clipboard",
                    tint: .purple
                ) {
                    importFromClipboard()
                }
            }
        }
        .safeAreaInset(edge: .bottom) {
            IOSOnboardingFooter {
                Button {
                    showStep(.setupChoice)
                } label: {
                    Text("Choose Another Method")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.bordered)
                .controlSize(.large)
            }
        }
    }

    private var scanStep: some View {
        VStack(spacing: 0) {
            IOSOnboardingHeader(
                title: "Scan QR",
                subtitle: "Point the camera at a clambhook config QR code.",
                systemImage: "qrcode.viewfinder"
            )
            .padding(.horizontal, 24)
            .padding(.top, 28)
            .padding(.bottom, 18)

            IOSQRCodeScannerView { value in
                stageImport(value, source: .qr, title: "QR import")
            }
            .frame(maxWidth: .infinity)
            .frame(height: 360)
            .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
            .padding(.horizontal, 20)

            if !message.isEmpty {
                IOSOnboardingStatus(message: message)
                    .padding(.horizontal, 24)
                    .padding(.top, 16)
            }

            Spacer(minLength: 0)
        }
        .background(Color(.systemGroupedBackground))
        .safeAreaInset(edge: .bottom) {
            IOSOnboardingFooter {
                Button {
                    showStep(.setupChoice)
                } label: {
                    Text("Choose Another Method")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.bordered)
                .controlSize(.large)
            }
        }
    }

    private var createProfileStep: some View {
        Form {
            IOSTunnelProfileTemplateForm(draft: $profileDraft)

            if !message.isEmpty {
                Section("Status") {
                    Text(message)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .safeAreaInset(edge: .bottom) {
            IOSOnboardingFooter {
                Button {
                    createProfile()
                } label: {
                    Text(profileDraft.template == .advanced ? "Save Config" : "Create Profile")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(!profileDraft.isInputComplete)
            }
        }
        .onAppear {
            seedAdvancedTOMLIfNeeded()
        }
    }

    private var readyStep: some View {
        IOSOnboardingScrollStep(
            title: "Profile Ready",
            subtitle: "Your first tunnel profile is in place.",
            systemImage: "checkmark.circle.fill",
            message: message
        ) {
            IOSOnboardingStatus(message: "Configuration validated.")
        }
        .safeAreaInset(edge: .bottom) {
            IOSOnboardingFooter {
                Button {
                    continueIfReady()
                } label: {
                    Text("Start Using clambhook")
                        .frame(maxWidth: .infinity)
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(!canContinue)
            }
        }
    }

    private func importFromClipboard() {
        guard let text = UIPasteboard.general.string else {
            message = "Clipboard does not contain text."
            return
        }
        stageImport(text, source: .clipboard, title: "Clipboard import")
    }

    private func importFromFile(_ result: Result<[URL], Error>) {
        do {
            guard let url = try result.get().first else {
                return
            }
            let scoped = url.startAccessingSecurityScopedResource()
            defer {
                if scoped {
                    url.stopAccessingSecurityScopedResource()
                }
            }
            stageImport(try String(contentsOf: url, encoding: .utf8), source: .file, title: url.lastPathComponent)
        } catch {
            message = error.localizedDescription
            canContinue = false
        }
    }

    @discardableResult
    private func stageImport(_ raw: String, source: InboxImportSource, title: String) -> Bool {
        do {
            let item = try model.attention.captureImport(rawValue: raw, source: source, title: title)
            stagedReviewItem = item
            message = "Review \(item.title) before it affects routing."
            canContinue = false
            return true
        } catch {
            message = error.localizedDescription
            canContinue = false
            return false
        }
    }

    private func createProfile() {
        do {
            if profileDraft.template == .advanced {
                try model.importTunnelConfigText(profileDraft.advancedTOML)
                refreshReadiness(successMessage: "Saved advanced config.")
            } else if let request = profileDraft.makeCreateRequest() {
                try model.createTunnelProfile(request)
                refreshReadiness(successMessage: "Created profile.")
            }
            if canContinue {
                step = .ready
            }
        } catch {
            message = error.localizedDescription
            canContinue = false
        }
    }

    private func seedAdvancedTOMLIfNeeded() {
        guard profileDraft.advancedTOML.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            return
        }
        profileDraft.advancedTOML = (try? TunnelConfigStore.loadOrCreateConfig(groupIdentifier: model.settingsStore.settings.appGroupIdentifier)) ?? defaultIOSTunnelConfig
    }

    private func continueIfReady() {
        refreshReadiness()
        guard canContinue else {
            return
        }
        onComplete()
    }

    private func acceptDisclosure() {
        vpnDisclosureAccepted = true
        refreshReadinessSilently()
        step = canContinue ? .ready : .setupChoice
    }

    private func showStep(_ nextStep: IOSOnboardingStep) {
        message = ""
        step = nextStep
    }

    private func goBack() {
        switch step {
        case .setupChoice:
            showStep(.disclosure)
        case .importConfig, .scanQR, .createProfile:
            showStep(.setupChoice)
        case .disclosure, .ready:
            break
        }
    }

    private func refreshReadinessSilently() {
        canContinue = model.tunnelOnboardingReadinessMessage() == nil
    }

    private func refreshReadiness(successMessage: String? = nil) {
        let wasReady = canContinue
        if let readinessMessage = model.tunnelOnboardingReadinessMessage() {
            canContinue = false
            message = readinessMessage
        } else {
            canContinue = true
            if let successMessage {
                message = successMessage
            } else if !wasReady {
                message = ""
            }
        }
    }
}

private enum IOSOnboardingStep {
    case disclosure
    case setupChoice
    case importConfig
    case scanQR
    case createProfile
    case ready

    var navigationTitle: String {
        switch self {
        case .disclosure:
            return "Welcome"
        case .setupChoice:
            return "Setup"
        case .importConfig:
            return "Import"
        case .scanQR:
            return "Scan QR"
        case .createProfile:
            return "Create Profile"
        case .ready:
            return "Ready"
        }
    }

    var showsBackButton: Bool {
        switch self {
        case .disclosure, .ready:
            return false
        case .setupChoice, .importConfig, .scanQR, .createProfile:
            return true
        }
    }
}

private struct IOSOnboardingScrollStep<Content: View>: View {
    var title: String
    var subtitle: String
    var systemImage: String
    var message: String
    private let content: Content

    init(
        title: String,
        subtitle: String,
        systemImage: String,
        message: String,
        @ViewBuilder content: () -> Content
    ) {
        self.title = title
        self.subtitle = subtitle
        self.systemImage = systemImage
        self.message = message
        self.content = content()
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                IOSOnboardingHeader(title: title, subtitle: subtitle, systemImage: systemImage)
                content
                if !message.isEmpty {
                    IOSOnboardingStatus(message: message)
                }
            }
            .padding(.horizontal, 24)
            .padding(.top, 32)
            .padding(.bottom, 110)
            .frame(maxWidth: 560, alignment: .leading)
            .frame(maxWidth: .infinity)
        }
        .background(Color(.systemGroupedBackground))
    }
}

private struct IOSOnboardingHeader: View {
    var title: String
    var subtitle: String
    var systemImage: String

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            ZStack {
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .fill(Color.accentColor.opacity(0.12))
                Image(systemName: systemImage)
                    .font(.title2.weight(.semibold))
                    .foregroundStyle(.tint)
            }
            .frame(width: 52, height: 52)

            VStack(alignment: .leading, spacing: 6) {
                Text(title)
                    .font(.title2.weight(.semibold))
                Text(subtitle)
                    .font(.body)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
    }
}

private struct IOSOnboardingActionRow: View {
    var title: String
    var detail: String
    var systemImage: String
    var tint: Color
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 14) {
                ZStack {
                    RoundedRectangle(cornerRadius: 8, style: .continuous)
                        .fill(tint.opacity(0.14))
                    Image(systemName: systemImage)
                        .font(.headline)
                        .foregroundStyle(tint)
                }
                .frame(width: 42, height: 42)

                VStack(alignment: .leading, spacing: 3) {
                    Text(title)
                        .font(.body.weight(.medium))
                        .foregroundStyle(.primary)
                    Text(detail)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .fixedSize(horizontal: false, vertical: true)
                }

                Spacer(minLength: 12)

                Image(systemName: "chevron.right")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.tertiary)
            }
            .padding(14)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            .overlay {
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .stroke(Color(.separator).opacity(0.35), lineWidth: 1)
            }
        }
        .buttonStyle(.plain)
    }
}

private struct IOSOnboardingStatus: View {
    var message: String

    var body: some View {
        Label {
            Text(message)
                .font(.footnote)
                .fixedSize(horizontal: false, vertical: true)
        } icon: {
            Image(systemName: "info.circle")
        }
        .foregroundStyle(.secondary)
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(.secondarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }
}

private struct IOSOnboardingFooter<Content: View>: View {
    private let content: Content

    init(@ViewBuilder content: () -> Content) {
        self.content = content()
    }

    var body: some View {
        VStack(spacing: 0) {
            Divider()
            content
                .padding(.horizontal, 24)
                .padding(.top, 12)
                .padding(.bottom, 12)
        }
        .background(.bar)
    }
}
