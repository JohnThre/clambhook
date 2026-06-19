import SwiftUI

@main
struct ClambhookMacApp: App {
    @StateObject private var model = AppleAppModel(platform: .macOS)
    @Environment(\.openSettings) private var openSettings

    var body: some Scene {
        MenuBarExtra("clambhook", systemImage: model.dashboard.status.running ? "network" : "network.slash") {
            MacMenuBarView(model: model)
                .frame(width: 420, height: 640)
                .onDisappear { model.refresh() }
        }
        .menuBarExtraStyle(.window)

        Window("clambhook", id: "dashboard") {
            MacDashboardWindowView(model: model)
        }
        .defaultSize(width: 960, height: 680)
        .defaultPosition(.center)

        Settings {
            AppSettingsView(model: model)
                .frame(width: 620, height: 760)
        }
    }
}
