import ClambhookShared
import SwiftUI

struct TVRootView: View {
    @ObservedObject var model: AppleAppModel
    @State private var selectedDestination: TVDestination = .dashboard

    var body: some View {
        NavigationSplitView {
            List(TVDestination.allCases, selection: $selectedDestination) { destination in
                Label(destination.title, systemImage: destination.systemImage)
            }
            .navigationTitle("clambhook")
        } detail: {
            NavigationStack {
                destinationView(selectedDestination)
                    .navigationTitle(selectedDestination.title)
                    .toolbar {
                        Button {
                            model.refresh()
                        } label: {
                            Image(systemName: "arrow.clockwise")
                        }
                        .accessibilityLabel("Refresh")
                    }
            }
        }
    }

    @ViewBuilder
    private func destinationView(_ destination: TVDestination) -> some View {
        switch destination {
        case .dashboard:
            DashboardContentView(model: model)
        case .settings:
            AppSettingsView(model: model)
        }
    }
}

private enum TVDestination: String, CaseIterable, Identifiable {
    case dashboard
    case settings

    var id: String { rawValue }

    var title: String {
        switch self {
        case .dashboard:
            return "Dashboard"
        case .settings:
            return "Settings"
        }
    }

    var systemImage: String {
        switch self {
        case .dashboard:
            return "chart.line.uptrend.xyaxis"
        case .settings:
            return "gearshape"
        }
    }
}
