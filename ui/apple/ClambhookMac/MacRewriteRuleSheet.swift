import ClambhookShared
import SwiftUI

// MARK: - Rewrite rule editor sheet

struct MacRewriteRuleSheet: View {
    @Environment(\.dismiss) private var dismiss
    let rule: DeveloperRewriteRulePayload
    let onSave: (DeveloperRewriteRulePayload) -> Void

    @State private var name: String
    @State private var stage: String
    @State private var host: String
    @State private var pathPrefix: String
    @State private var ops: [DeveloperRewriteOpPayload]

    init(rule: DeveloperRewriteRulePayload, onSave: @escaping (DeveloperRewriteRulePayload) -> Void) {
        self.rule = rule
        self.onSave = onSave
        _name = State(initialValue: rule.name)
        _stage = State(initialValue: rule.stage.isEmpty ? "both" : rule.stage)
        _host = State(initialValue: rule.match.host)
        _pathPrefix = State(initialValue: rule.match.pathPrefix)
        _ops = State(initialValue: rule.ops.isEmpty ? [DeveloperRewriteOpPayload()] : rule.ops)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Rewrite Rule")
                    .font(.headline)
                Spacer()
                Button("Cancel") { dismiss() }
                Button {
                    onSave(buildRule())
                    dismiss()
                } label: {
                    Label("Save", systemImage: "checkmark")
                }
                .keyboardShortcut(.return, modifiers: .command)
            }
            .padding(16)
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    HStack {
                        TextField("Name", text: $name)
                            .textFieldStyle(.roundedBorder)
                        Picker("Stage", selection: $stage) {
                            Text("Request").tag("request")
                            Text("Response").tag("response")
                            Text("Both").tag("both")
                        }
                        .pickerStyle(.segmented)
                    }
                    GroupBox("Match") {
                        VStack(alignment: .leading, spacing: 8) {
                            HStack {
                                Text("Host").font(.caption).foregroundStyle(.secondary)
                                TextField("api.example.com", text: $host)
                                    .textFieldStyle(.roundedBorder)
                            }
                            HStack {
                                Text("Path prefix").font(.caption).foregroundStyle(.secondary)
                                TextField("/v1/", text: $pathPrefix)
                                    .textFieldStyle(.roundedBorder)
                            }
                        }
                        .padding(8)
                    }
                    HStack {
                        Text("Operations")
                            .font(.subheadline.weight(.semibold))
                        Spacer()
                        Button {
                            ops.append(DeveloperRewriteOpPayload())
                        } label: {
                            Label("Add Op", systemImage: "plus")
                        }
                    }
                    ForEach($ops) { $op in
                        rewriteOpRow(op: $op)
                    }
                    Text("Header: add/set/remove · Body: set/replace · Status: set (response/both only)")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                .padding(16)
            }
        }
    }

    private func rewriteOpRow(op: Binding<DeveloperRewriteOpPayload>) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Picker("Target", selection: op.target) {
                    Text("Header").tag("header")
                    Text("Body").tag("body")
                    Text("Status").tag("status")
                }
                .pickerStyle(.segmented)
                Picker("Action", selection: op.action) {
                    Text(opActionLabel(op.target.wrappedValue, "add")).tag("add")
                    Text(opActionLabel(op.target.wrappedValue, "set")).tag("set")
                    Text(opActionLabel(op.target.wrappedValue, "remove")).tag("remove")
                    Text(opActionLabel(op.target.wrappedValue, "replace")).tag("replace")
                }
                .pickerStyle(.segmented)
                Spacer()
                Button(role: .destructive) {
                    ops.removeAll { $0.id == op.id }
                } label: {
                    Image(systemName: "minus.circle")
                }
                .buttonStyle(.borderless)
            }
            if op.target.wrappedValue == "header" {
                HStack {
                    TextField("Header name", text: op.field)
                        .textFieldStyle(.roundedBorder)
                    TextField("Value", text: op.value)
                        .textFieldStyle(.roundedBorder)
                }
            } else if op.target.wrappedValue == "body" {
                if op.action.wrappedValue == "replace" {
                    TextField("Find", text: op.value)
                        .textFieldStyle(.roundedBorder)
                    TextField("Replace with", text: op.replace)
                        .textFieldStyle(.roundedBorder)
                } else {
                    TextEditor(text: op.value)
                        .font(.system(.caption, design: .monospaced))
                        .frame(minHeight: 80)
                        .overlay(RoundedRectangle(cornerRadius: 6).stroke(.quaternary))
                }
            } else {
                TextField("Status code", text: op.value)
                    .textFieldStyle(.roundedBorder)
            }
        }
        .padding(8)
        .background(Color.secondary.opacity(0.06), in: RoundedRectangle(cornerRadius: 8))
    }

    private func opActionLabel(_ target: String, _ action: String) -> String {
        switch target {
        case "header":
            return action.capitalized
        case "body":
            return action.capitalized
        default:
            return action.capitalized
        }
    }

    private func buildRule() -> DeveloperRewriteRulePayload {
        var updated = rule
        updated.name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        updated.stage = stage
        updated.match = DeveloperMatchPayload(
            methods: rule.match.methods,
            host: host.trimmingCharacters(in: .whitespacesAndNewlines),
            pathPrefix: pathPrefix.trimmingCharacters(in: .whitespacesAndNewlines),
            urlContains: rule.match.urlContains
        )
        updated.ops = ops
        updated.enabled = true
        return updated
    }
}