import SwiftUI

struct IOSRootView: View {
    @ObservedObject var model: AppleAppModel
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass
    @State private var selectedDestination: IOSDashboardDestination = .overview
    @State private var showingSettings = false
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
        .sheet(isPresented: $showingSettings) {
            NavigationStack {
                AppSettingsView(model: model)
                    .navigationTitle("Settings")
                    .toolbar {
                        ToolbarItem(placement: .topBarTrailing) {
                            Button("Done") {
                                showingSettings = false
                            }
                        }
                    }
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
            ForEach(IOSDashboardDestination.allCases) { destination in
                NavigationStack {
                    destinationView(destination)
                        .navigationTitle(destination.title)
                        .toolbar {
                            settingsToolbarItem
                        }
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
                Section("Monitoring") {
                    ForEach(IOSDashboardDestination.allCases) { destination in
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
                    .toolbar {
                        settingsToolbarItem
                    }
            }
        }
    }

    @ToolbarContentBuilder
    private var settingsToolbarItem: some ToolbarContent {
        ToolbarItem(placement: .topBarTrailing) {
            Button {
                showingSettings = true
            } label: {
                Image(systemName: "gearshape")
            }
            .accessibilityLabel("Settings")
        }
    }

    @ViewBuilder
    private func destinationView(_ destination: IOSDashboardDestination) -> some View {
        switch destination {
        case .overview:
            IOSOperationsOverviewView(model: model)
        case .traffic:
            IOSOperationsTrafficView(model: model)
        case .servers:
            IOSOperationsServersView(model: model)
        case .logs:
            IOSOperationsLogsView(model: model)
        }
    }
}

private enum IOSDashboardDestination: String, CaseIterable, Identifiable, Hashable {
    case overview
    case traffic
    case servers
    case logs

    var id: Self { self }

    var title: String {
        switch self {
        case .overview:
            return "Overview"
        case .traffic:
            return "Traffic"
        case .servers:
            return "Servers"
        case .logs:
            return "Logs"
        }
    }

    var systemImage: String {
        switch self {
        case .overview:
            return "gauge.with.dots.needle.67percent"
        case .traffic:
            return "point.3.connected.trianglepath.dotted"
        case .servers:
            return "server.rack"
        case .logs:
            return "doc.text.magnifyingglass"
        }
    }
}
