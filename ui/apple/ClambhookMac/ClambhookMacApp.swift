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
                .sheet(isPresented: Binding(
                    get: { !model.onboardingManager.isComplete },
                    set: { if !$0 { model.onboardingManager.complete() } }
                )) {
                    OnboardingView(model: model, manager: model.onboardingManager)
                }
        }
        .menuBarExtraStyle(.window)

        Window("clambhook", id: "dashboard") {
            MacDashboardWindowView(model: model)
        }
        .defaultSize(width: 1060, height: 700)
        .defaultPosition(.center)

        Settings {
            AppSettingsView(model: model)
                .frame(width: 620, height: 760)
        }
    }
}
