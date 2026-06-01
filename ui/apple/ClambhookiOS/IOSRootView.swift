import SwiftUI

struct IOSRootView: View {
    @ObservedObject var model: AppleAppModel
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass
    @State private var selectedDestination: IOSAppDestination = .status
    @State private var showingOnboarding = false
    @AppStorage("org.jpfchang.clambhook.onboardingComplete") private var onboardingComplete = false

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
        .task {
            if !onboardingComplete || model.shouldShowOnboarding() {
                showingOnboarding = true
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
