import SwiftUI

struct IOSRootView: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        NavigationStack {
            DashboardContentView(model: model)
                .navigationTitle("clambhook")
                .toolbar {
                    ToolbarItem(placement: .topBarTrailing) {
                        NavigationLink {
                            AppSettingsView(model: model)
                                .navigationTitle("Settings")
                        } label: {
                            Image(systemName: "gear")
                        }
                        .accessibilityLabel("Settings")
                    }
                }
        }
    }
}
