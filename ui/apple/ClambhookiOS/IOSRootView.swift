import ClambhookShared
import SwiftUI

struct IOSRootView: View {
    @ObservedObject var model: AppleAppModel
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass
    @Environment(\.scenePhase) private var scenePhase
    @State private var selectedDestination: IOSAppDestination = .status
    @State private var showingOnboarding = false
    @AppStorage("org.jpfchang.clambhook.onboardingComplete") private var onboardingComplete = false
    @StateObject private var inspectionLock = InspectionLockState()

    var body: some View {
        Group {
            if horizontalSizeClass == .regular {
                splitView
            } else {
                tabView
            }
        }
        .fullScreenCover(isPresented: $showingOnboarding) {
            IOSOnboardingView(model: model) {
                onboardingComplete = true
                showingOnboarding = false
                model.refresh()
            }
        }
        .overlay {
            if shouldShowInspectionLock {
                IOSInspectionLockOverlay(state: inspectionLock) {
                    Task { await authenticateInspectionLock() }
                }
            }
        }
        .task {
            if !onboardingComplete || model.shouldShowOnboarding() {
                showingOnboarding = true
            }
            engageInspectionLockIfNeeded()
        }
        .onChange(of: scenePhase) { _, phase in
            switch phase {
            case .active:
                engageInspectionLockIfNeeded()
            case .background:
                inspectionLock.lockIfNeeded(enabled: model.settingsStore.settings.inspectionLockEnabled)
            default:
                break
            }
        }
        .onChange(of: model.settingsStore.settings.inspectionLockEnabled) { _, enabled in
            if enabled {
                engageInspectionLockIfNeeded()
            } else {
                inspectionLock.clearLock()
            }
        }
    }

    private var tabView: some View {
        TabView(selection: $selectedDestination) {
            ForEach(IOSAppDestination.allCases) { destination in
                NavigationStack {
                    destinationView(destination)
                        .navigationTitle(destination.title)
                }
                .tabItem {
                    Label(destination.title, systemImage: destination.systemImage)
                }
                .tag(destination)
            }
        }
    }

    private var splitView: some View {
        NavigationSplitView {
            List {
                Section {
                    ForEach(IOSAppDestination.allCases) { destination in
                        Button {
                            selectedDestination = destination
                        } label: {
                            HStack {
                                Label(destination.title, systemImage: destination.systemImage)
                                Spacer()
                                if destination == selectedDestination {
                                    Image(systemName: "checkmark")
                                        .foregroundStyle(.tint)
                                }
                            }
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
            .navigationTitle("clambhook")
        } detail: {
            NavigationStack {
                destinationView(selectedDestination)
                    .navigationTitle(selectedDestination.title)
            }
        }
    }

    @ViewBuilder
    private func destinationView(_ destination: IOSAppDestination) -> some View {
        switch destination {
        case .status:
            IOSStatusView(model: model)
        case .profiles:
            IOSProfilesView(model: model)
        case .activity:
            IOSActivityView(model: model)
        case .rules:
            IOSRulesView(model: model)
        case .settings:
            AppSettingsView(model: model)
        }
    }

    private var shouldShowInspectionLock: Bool {
        model.settingsStore.settings.inspectionLockEnabled && inspectionLock.isLocked
    }

    private func engageInspectionLockIfNeeded() {
        inspectionLock.lockIfNeeded(enabled: model.settingsStore.settings.inspectionLockEnabled)
        Task { await authenticateInspectionLock() }
    }

    private func authenticateInspectionLock() async {
        await inspectionLock.authenticateIfNeeded(enabled: model.settingsStore.settings.inspectionLockEnabled)
    }
}

private struct IOSInspectionLockOverlay: View {
    @ObservedObject var state: InspectionLockState
    var onUnlock: () -> Void

    var body: some View {
        ZStack {
            Color(.systemBackground)
                .ignoresSafeArea()
            VStack(spacing: 18) {
                Image(systemName: "lock.shield")
                    .font(.system(size: 52, weight: .semibold))
                    .foregroundStyle(.tint)
                VStack(spacing: 6) {
                    Text("Activity Locked")
                        .font(.title2.weight(.semibold))
                    Text("Use \(state.status.label) to view local inspection details.")
                        .font(.body)
                        .foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                }
                if !state.message.isEmpty {
                    Text(state.message)
                        .font(.footnote)
                        .foregroundStyle(.red)
                        .multilineTextAlignment(.center)
                }
                Button {
                    onUnlock()
                } label: {
                    if state.isAuthenticating {
                        ProgressView()
                    } else {
                        Label("Unlock", systemImage: "faceid")
                    }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.large)
                .disabled(state.isAuthenticating || !state.status.isAvailable)
            }
            .padding(28)
            .frame(maxWidth: 360)
        }
        .accessibilityIdentifier("inspection-lock")
    }
}

private enum IOSAppDestination: String, CaseIterable, Identifiable, Hashable {
    case status
    case profiles
    case activity
    case rules
    case settings

    var id: Self { self }

    var title: String {
        switch self {
        case .status:
            return "Status"
        case .profiles:
            return "Profiles"
        case .activity:
            return "Activity"
        case .rules:
            return "Rules"
        case .settings:
            return "Settings"
        }
    }

    var systemImage: String {
        switch self {
        case .status:
            return "shield.lefthalf.filled"
        case .profiles:
            return "person.crop.rectangle.stack"
        case .activity:
            return "waveform.path.ecg"
        case .rules:
            return "slider.horizontal.3"
        case .settings:
            return "gearshape"
        }
    }
}
