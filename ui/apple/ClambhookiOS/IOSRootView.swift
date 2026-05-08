import SwiftUI

struct IOSRootView: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        NavigationStack {
            IOSStandaloneView(model: model)
        }
    }
}
