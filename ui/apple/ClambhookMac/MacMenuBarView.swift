import AppKit
import SwiftUI

struct MacMenuBarView: View {
    @ObservedObject var model: AppleAppModel
    @Environment(\.openSettings) private var openSettings

    var body: some View {
        VStack(spacing: 0) {
            DashboardContentView(model: model)
            Divider()
            HStack {
                Button {
                    model.launchDaemon()
                } label: {
                    Label("Launch Daemon", systemImage: "terminal")
                }
                Button {
                    model.stopDaemon()
                } label: {
                    Label("Stop Daemon", systemImage: "xmark.octagon")
                }
                Spacer()
                Button {
                    openSettings()
                } label: {
                    Label("Settings", systemImage: "gear")
                }
                Button {
                    model.stop()
                    NSApplication.shared.terminate(nil)
                } label: {
                    Label("Quit", systemImage: "power")
                }
            }
            .padding(12)
            if !model.daemonMessage.isEmpty {
                Text(model.daemonMessage)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, 12)
                    .padding(.bottom, 8)
            }
        }
    }
}
