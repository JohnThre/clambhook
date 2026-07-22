import AppKit
import ClambhookShared
import SwiftUI

// MARK: - Compose request sheet

private struct ComposeHeaderRow: Identifiable {
    let id = UUID()
    var name: String
    var value: String
}

struct MacComposeRequestSheet: View {
    @Environment(\.dismiss) private var dismiss
    let entry: DeveloperEntryPayload
    let onSend: (DeveloperRepeatRequestPayload) -> Void

    @State private var method: String
    @State private var url: String
    @State private var headers: [ComposeHeaderRow]
    @State private var bodyText: String

    init(entry: DeveloperEntryPayload, onSend: @escaping (DeveloperRepeatRequestPayload) -> Void) {
        self.entry = entry
        self.onSend = onSend
        _method = State(initialValue: entry.method.isEmpty ? "GET" : entry.method)
        _url = State(initialValue: entry.url)
        _headers = State(initialValue: entry.request.headers
            .filter { !$0.redacted && !$0.truncated }
            .map { ComposeHeaderRow(name: $0.name, value: $0.value) })
        _bodyText = State(initialValue: entry.request.body.preview)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Edit & Send Request")
                    .font(.headline)
                Spacer()
                Button("Cancel") { dismiss() }
                Button {
                    onSend(makeRequest())
                    dismiss()
                } label: {
                    Label("Send", systemImage: "paperplane")
                }
                .keyboardShortcut(.return, modifiers: .command)
                .disabled(url.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            .padding(16)
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    HStack {
                        TextField("Method", text: $method)
                            .frame(width: 90)
                        TextField("URL", text: $url)
                    }
                    .textFieldStyle(.roundedBorder)
                    HStack {
                        Text("Headers")
                            .font(.subheadline.weight(.semibold))
                        Spacer()
                        Button {
                            headers.append(ComposeHeaderRow(name: "", value: ""))
                        } label: {
                            Label("Add", systemImage: "plus")
                        }
                    }
                    ForEach($headers) { $header in
                        HStack {
                            TextField("Name", text: $header.name)
                                .frame(width: 180)
                            TextField("Value", text: $header.value)
                            Button(role: .destructive) {
                                headers.removeAll { $0.id == header.id }
                            } label: {
                                Image(systemName: "minus.circle")
                            }
                            .buttonStyle(.borderless)
                        }
                        .textFieldStyle(.roundedBorder)
                    }
                    if entry.request.body.truncated {
                        Label("Captured body was truncated; provide the full body to send.", systemImage: "exclamationmark.triangle")
                            .font(.caption)
                            .foregroundStyle(.orange)
                    }
                    Text("Body")
                        .font(.subheadline.weight(.semibold))
                    TextEditor(text: $bodyText)
                        .font(.system(.caption, design: .monospaced))
                        .frame(minHeight: 140)
                        .overlay(RoundedRectangle(cornerRadius: 6).stroke(.quaternary))
                }
                .padding(16)
            }
        }
    }

    private func makeRequest() -> DeveloperRepeatRequestPayload {
        DeveloperRepeatRequestPayload(
            entryID: entry.id,
            method: method.trimmingCharacters(in: .whitespaces),
            url: url.trimmingCharacters(in: .whitespaces),
            headers: headers
                .filter { !$0.name.trimmingCharacters(in: .whitespaces).isEmpty }
                .map { DeveloperHeaderPayload(name: $0.name, value: $0.value) },
            body: bodyText
        )
    }
}
