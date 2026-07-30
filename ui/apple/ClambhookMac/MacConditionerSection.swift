import AppKit
import ClambhookShared
import SwiftUI

// MARK: - Network Conditioner

struct MacConditionerSection: View {
    @ObservedObject var model: AppleAppModel
    @State private var enabled = false
    @State private var downloadKbps = ""
    @State private var uploadKbps = ""
    @State private var latency = ""
    @State private var jitter = ""
    @State private var lossPercent = ""
    @State private var loaded = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                overview
                Divider()
                shaperForm
            }
            .padding(20)
        }
        .task {
            await model.refreshConditionerNow()
            syncFromModel()
        }
        .onChange(of: model.conditioner) { _, _ in
            syncFromModel()
        }
    }

    private var overview: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("Network Conditioner")
                    .font(.headline)
                Spacer()
                Toggle("Enabled", isOn: $enabled)
                    .toggleStyle(.switch)
                    .controlSize(.small)
                    .labelsHidden()
            }
            HStack(spacing: 16) {
                Label(enabled ? "Active" : "Off", systemImage: enabled ? "checkmark.circle.fill" : "xmark.circle")
                    .foregroundStyle(enabled ? .green : .secondary)
                if !model.conditioner.profile.isEmpty {
                    Label("Profile: \(model.conditioner.profile)", systemImage: "person.crop.circle")
                        .foregroundStyle(.secondary)
                }
            }
            .font(.subheadline)
            if !model.daemonMessage.isEmpty {
                Text(model.daemonMessage)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var shaperForm: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Shaping")
                .font(.headline)
            Form {
                TextField("Download (kbps)", text: $downloadKbps)
                    .disabled(!enabled)
                TextField("Upload (kbps)", text: $uploadKbps)
                    .disabled(!enabled)
                TextField("Latency (e.g. 40ms)", text: $latency)
                    .disabled(!enabled)
                TextField("Jitter (e.g. 10ms)", text: $jitter)
                    .disabled(!enabled)
                TextField("Loss (%)", text: $lossPercent)
                    .disabled(!enabled)
            }
            HStack {
                Spacer()
                Button("Save") {
                    save()
                }
                .buttonStyle(.borderedProminent)
            }
        }
    }

    private func syncFromModel() {
        let payload = model.conditioner
        enabled = payload.enabled
        downloadKbps = payload.downloadKbps == 0 ? "" : "\(payload.downloadKbps)"
        uploadKbps = payload.uploadKbps == 0 ? "" : "\(payload.uploadKbps)"
        latency = payload.latency
        jitter = payload.jitter
        lossPercent = payload.lossPercent == 0 ? "" : "\(payload.lossPercent)"
        loaded = true
    }

    private func save() {
        let request = ConditionerUpdateRequest(
            profile: model.conditioner.profile.isEmpty ? nil : model.conditioner.profile,
            enabled: enabled,
            downloadKbps: Int(downloadKbps.trimmingCharacters(in: .whitespaces)) ?? 0,
            uploadKbps: Int(uploadKbps.trimmingCharacters(in: .whitespaces)) ?? 0,
            latency: latency.trimmingCharacters(in: .whitespaces),
            jitter: jitter.trimmingCharacters(in: .whitespaces),
            lossPercent: Double(lossPercent.trimmingCharacters(in: .whitespaces)) ?? 0
        )
        model.saveConditioner(request)
    }
}
