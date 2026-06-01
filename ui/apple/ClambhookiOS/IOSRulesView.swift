import ClambhookShared
import SwiftUI

struct IOSRuleEditorView: View {
    @ObservedObject var model: AppleAppModel
    @Environment(\.dismiss) private var dismiss
    @State private var rules: [RulePayload] = []
    @State private var message = ""
    @State private var loaded = false

    var body: some View {
        List {
            Section("Rules") {
                if rules.isEmpty {
                    IOSInlineEmptyState(text: "No routing rules.", systemImage: "checklist")
                } else {
                    ForEach(rules.indices, id: \.self) { index in
                        NavigationLink {
                            IOSRuleFormView(rule: $rules[index], chainNames: chainNames)
                        } label: {
                            IOSRuleDraftRow(rule: rules[index])
                        }
                    }
                    .onDelete { rules.remove(atOffsets: $0) }
                    .onMove { rules.move(fromOffsets: $0, toOffset: $1) }
                }
            }

            Section {
                Button {
                    rules.append(RulePayload(name: "new-rule", action: "block"))
                } label: {
                    Label("Add Rule", systemImage: "plus.circle")
                }

                Button {
                    saveRules()
                } label: {
                    Label("Save Rules", systemImage: "checkmark.circle")
                }
                .fontWeight(.semibold)
            }

            if !message.isEmpty {
                Section("Status") {
                    Text(message)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Rule Editor")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                EditButton()
            }
        }
        .onAppear {
            if !loaded {
                rules = model.dashboard.rules.rules
                loaded = true
            }
        }
    }

    private var chainNames: [String] {
        model.dashboard.servers.chains.map(\.name)
    }

    private func saveRules() {
        do {
            try model.replaceActiveProfileRules(rules)
            message = "Saved rules."
            dismiss()
        } catch {
            message = error.localizedDescription
        }
    }
}

private struct IOSRuleDraftRow: View {
    var rule: RulePayload

    var body: some View {
        HStack(spacing: 12) {
            IOSActionChip(action: rule.action)
            VStack(alignment: .leading, spacing: 3) {
                Text(emptyDash(rule.name))
                    .font(.body.weight(.medium))
                    .lineLimit(1)
                Text(ruleSummary)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
        }
    }

    private var ruleSummary: String {
        var parts: [String] = []
        parts.append(rule.action)
        parts.append(contentsOf: rule.domains.prefix(2))
        parts.append(contentsOf: rule.domainSuffixes.prefix(2).map { "*.\($0)" })
        parts.append(contentsOf: rule.cidrs.prefix(2))
        if !rule.ports.isEmpty {
            parts.append(rule.ports.map(String.init).joined(separator: ","))
        }
        return parts.filter { !$0.isEmpty }.joined(separator: " / ")
    }
}

private struct IOSRuleFormView: View {
    @Binding var rule: RulePayload
    var chainNames: [String]

    var body: some View {
        Form {
            Section("Rule") {
                TextField("Name", text: $rule.name)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                Picker("Action", selection: $rule.action) {
                    Text("Block").tag("block")
                    Text("Reject").tag("reject")
                    Text("Direct").tag("direct")
                    ForEach(chainNames, id: \.self) { chain in
                        Text("Proxy: \(chain)").tag("chain:\(chain)")
                    }
                }
            }

            Section("Matchers") {
                IOSCSVField(title: "Domains", values: $rule.domains)
                IOSCSVField(title: "Suffixes", values: $rule.domainSuffixes)
                IOSCSVField(title: "Keywords", values: $rule.domainKeywords)
                IOSCSVField(title: "CIDRs", values: $rule.cidrs)
                IOSPortsField(ports: $rule.ports)
                IOSCSVField(title: "Networks", values: $rule.networks)
            }
        }
        .navigationTitle(rule.name.isEmpty ? "Rule" : rule.name)
        .navigationBarTitleDisplayMode(.inline)
    }
}

private struct IOSCSVField: View {
    var title: String
    @Binding var values: [String]

    var body: some View {
        TextField(title, text: Binding(
            get: { values.joined(separator: ", ") },
            set: { raw in
                values = raw.split(separator: ",")
                    .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
                    .filter { !$0.isEmpty }
            }
        ))
        .textInputAutocapitalization(.never)
        .autocorrectionDisabled()
    }
}

private struct IOSPortsField: View {
    @Binding var ports: [Int]

    var body: some View {
        TextField("Ports", text: Binding(
            get: { ports.map(String.init).joined(separator: ", ") },
            set: { raw in
                ports = raw.split(separator: ",")
                    .compactMap { Int($0.trimmingCharacters(in: .whitespacesAndNewlines)) }
            }
        ))
        .keyboardType(.numbersAndPunctuation)
    }
}
