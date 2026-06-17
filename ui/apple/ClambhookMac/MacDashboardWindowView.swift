import AppKit
import ClambhookShared
import SwiftUI

struct MacDashboardWindowView: View {
    @ObservedObject var model: AppleAppModel
    @Environment(\.openSettings) private var openSettings

    var body: some View {
        NavigationStack {
            DashboardContentView(model: model)
                .navigationTitle("clambhook")
                .toolbar {
                    ToolbarItem(placement: .primaryAction) {
                        Button {
                            model.refresh()
                        } label: {
                            Label("Refresh", systemImage: "arrow.clockwise")
                        }
                    }
                    ToolbarItem(placement: .primaryAction) {
                        Button {
                            model.connectOrDisconnect()
                        } label: {
                            Label(
                                model.dashboard.status.running ? "Disconnect" : "Connect",
                                systemImage: model.dashboard.status.running ? "stop.fill" : "play.fill"
                            )
                        }
                        .disabled(!model.dashboard.apiOnline && !model.dashboard.status.running)
                    }
                    ToolbarItem(placement: .secondaryAction) {
                        Button {
                            openSettings()
                        } label: {
                            Label("Settings", systemImage: "gear")
                        }
                    }
                }
        }
        .frame(minWidth: 640, minHeight: 480)
        .onAppear {
            NSApp.setActivationPolicy(.regular)
            NSApp.activate(ignoringOtherApps: true)
        }
        .onDisappear {
            NSApp.setActivationPolicy(.accessory)
        }
    }
}
