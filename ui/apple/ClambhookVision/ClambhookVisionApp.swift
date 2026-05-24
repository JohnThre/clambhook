import SwiftUI

let visionImmersiveSpaceID = "clambhook-network-map"

@main
struct ClambhookVisionApp: App {
    @StateObject private var model = AppleAppModel(platform: .visionOS)
    @State private var immersionStyle: ImmersionStyle = .mixed

    var body: some Scene {
        WindowGroup {
            VisionRootView(model: model)
                .onAppear { model.start() }
                .onDisappear { model.stop() }
        }
        .defaultSize(width: 1040, height: 720)

        ImmersiveSpace(id: visionImmersiveSpaceID) {
            VisionNetworkMapImmersiveView(model: model)
        }
        .immersionStyle(selection: $immersionStyle, in: .mixed)
    }
}
