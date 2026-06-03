import SwiftUI

@main
struct ClambhookTVApp: App {
    @StateObject private var model = AppleAppModel(platform: .tvOS)

    var body: some Scene {
        WindowGroup {
            TVRootView(model: model)
                .onAppear { model.start() }
                .onDisappear { model.stop() }
        }
    }
}
