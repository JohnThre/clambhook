import SwiftUI

@main
struct ClambhookIOSApp: App {
    @StateObject private var model = AppleAppModel(platform: .iOS)

    var body: some Scene {
        WindowGroup {
            IOSRootView(model: model)
                .onAppear { model.start() }
                .onDisappear { model.stop() }
        }
    }
}
