import ClambhookShared
import SwiftUI

struct IOSRulesView: View {
    @ObservedObject var model: AppleAppModel
    @State private var rows: [RuleEditorRow] = []
    @State private var validationErrors: [RuleEditorValidationError] = []
    @State private var message = ""
    @State private var loaded = false

    var body: some View {
        List {
            if model.dashboard.activeProfile.isEmpty {
                Section {
                    ContentUnavailableView(
                        "No active profile",
                        systemImage: "person.crop.rectangle.stack",
                        description: Text("Choose a profile before editing rules.")
                    )
                }
            } else {
                Section("Profile") {
                    Text(model.dashboard.activeProfile)
                        .font(.body.weight(.medium))
                }
            }

            Section("Rules") {
                if rows.isEmpty {
                    IOSInlineEmptyState(text: "No routing rules.", systemImage: "checklist")
                } else {
                    ForEach(Array(rows.enumerated()), id: \.element.id) { index, row in
                        NavigationLink {
                            IOSRuleFormView(
                                row: binding(for: row.id),
                                chainNames: chainNames,
                                rowNumber: index + 1
                            )
                        } label: {
                            IOSRuleDraftRow(
                                row: row,
                                order: index + 1,
                                error: firstError(for: index)
                            )
                        }
                    }
                    .onDelete { offsets in
                        rows.remove(atOffsets: offsets)
                        validationErrors = []
                    }
                    .onMove { offsets, destination in
                        rows.move(fromOffsets: offsets, toOffset: destination)
                        validationErrors = RuleEditor.validate(rows: rows, chainNames: chainNames)
                    }
                }
            }

            if !message.isEmpty {
                Section("Status") {
                    Text(message)
                        .font(.footnote)
                        .foregroundColor(validationErrors.isEmpty ? Color.secondary : Color.red)
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle("Rules")
        .toolbar {
            ToolbarItem(placement: .topBarLeading) {
                Button("Apply") {
                    saveRules()
                }
                .disabled(model.dashboard.activeProfile.isEmpty)
            }
            ToolbarItem(placement: .topBarTrailing) {
                Button {
                    addRule()
                } label: {
                    Image(systemName: "plus")
                }
                .disabled(model.dashboard.activeProfile.isEmpty)
            }
            ToolbarItem(placement: .topBarTrailing) {
                if !rows.isEmpty {
                    EditButton()
                }
            }
        }
        .onAppear {
            if !loaded {
                loadRowsFromDashboard()
                loaded = true
            }
        }
        .onChange(of: model.dashboard.activeProfile) { _, _ in
            loadRowsFromDashboard()
            loaded = true
            message = ""
        }
    }

    private var chainNames: [String] {
        model.dashboard.servers.chains.map(\.name)
    }

    private func binding(for id: RuleEditorRow.ID) -> Binding<RuleEditorRow> {
        Binding {
            rows.first(where: { $0.id == id }) ?? RuleEditorRow(name: "", matcherKind: .domainSuffix)
        } set: { next in
            if let index = rows.firstIndex(where: { $0.id == id }) {
                rows[index] = next
                validationErrors = []
            }
        }
    }

    private func firstError(for index: Int) -> RuleEditorValidationError? {
        validationErrors.first { $0.rowIndex == index }
    }

    private func loadRowsFromDashboard() {
        rows = RuleEditor.rows(from: model.dashboard.rules.rules)
        validationErrors = []
    }

    private func saveRules() {
        do {
            let nextRules = try RuleEditor.rules(from: rows, chainNames: chainNames)
            try model.replaceActiveProfileRules(nextRules)
            rows = RuleEditor.rows(from: nextRules)
            validationErrors = []
            message = "Applied rules."
        } catch let failure as RuleEditorValidationFailure {
            validationErrors = failure.errors
            message = failure.localizedDescription
        } catch {
            validationErrors = []
            message = error.localizedDescription
        }
    }

    private func addRule() {
        rows.append(
            RuleEditorRow(
                name: "new-rule",
                matcherKind: .domainSuffix,
                policyKind: .proxy,
                chainName: chainNames.first ?? ""
            )
        )
        validationErrors = []
    }
}

private struct IOSRuleDraftRow: View {
    var row: RuleEditorRow
    var order: Int
    var error: RuleEditorValidationError?

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Text("\(order)")
                .font(.caption.monospacedDigit().weight(.semibold))
                .foregroundStyle(.secondary)
                .frame(width: 24, alignment: .trailing)
                .padding(.top, 3)

            IOSActionChip(action: row.encodedAction)

            VStack(alignment: .leading, spacing: 4) {
                Text(emptyDash(row.name))
                    .font(.body.weight(.medium))
                    .lineLimit(1)
                Text(rowSubtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                if let error {
                    Label(error.message, systemImage: "exclamationmark.triangle.fill")
                        .font(.caption)
                        .foregroundStyle(.red)
                        .lineLimit(2)
                }
            }
        }
        .padding(.vertical, 2)
    }

    private var rowSubtitle: String {
        [row.matcherKind.displayName, row.matcherSummary, row.policySummary]
            .filter { !$0.isEmpty }
            .joined(separator: " / ")
    }
}

private struct IOSRuleFormView: View {
    @Binding var row: RuleEditorRow
    var chainNames: [String]
    var rowNumber: Int

    var body: some View {
        Form {
            Section("Rule") {
                LabeledContent("Order", value: "\(rowNumber)")
                TextField("Name", text: $row.name)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                Picker("Matcher", selection: matcherKindBinding) {
                    ForEach(RuleMatcherKind.editableCases) { kind in
                        Text(kind.displayName).tag(kind)
                    }
                    if row.matcherKind == .combined {
                        Text(RuleMatcherKind.combined.displayName).tag(RuleMatcherKind.combined)
                    }
                }
                .disabled(row.matcherKind == .combined)
                matcherValueControl
            }

            Section("Policy") {
                Picker("Action", selection: $row.policyKind) {
                    ForEach(RulePolicyKind.allCases) { policy in
                        Text(policy.displayName).tag(policy)
                    }
                }
                if row.policyKind == .proxy {
                    Picker("Chain", selection: $row.chainName) {
                        if chainNames.isEmpty {
                            Text("No chains").tag("")
                        }
                        ForEach(chainNames, id: \.self) { chain in
                            Text(chain).tag(chain)
                        }
                    }
                    .disabled(chainNames.isEmpty)
                }
            }
        }
        .navigationTitle(row.name.isEmpty ? "Rule" : row.name)
        .navigationBarTitleDisplayMode(.inline)
        .onChange(of: row.matcherKind) { _, next in
            switch next {
            case .allTraffic:
                row.value = ""
                row.compatibilityRule = nil
            case .network:
                if row.value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                    row.value = "tcp"
                }
                row.compatibilityRule = nil
            case .domain, .domainSuffix, .domainKeyword, .cidr, .port:
                row.compatibilityRule = nil
            case .combined:
                break
            }
        }
    }

    private var matcherKindBinding: Binding<RuleMatcherKind> {
        Binding {
            row.matcherKind
        } set: { next in
            row.matcherKind = next
        }
    }

    @ViewBuilder
    private var matcherValueControl: some View {
        switch row.matcherKind {
        case .allTraffic:
            LabeledContent("Value", value: "All traffic")
        case .combined:
            LabeledContent("Value", value: row.matcherSummary)
        case .network:
            Picker("Value", selection: networkValueBinding) {
                Text("TCP").tag("tcp")
                Text("UDP").tag("udp")
            }
        case .port:
            TextField(row.matcherKind.valueLabel, text: $row.value)
                .keyboardType(.numberPad)
        case .cidr:
            TextField(row.matcherKind.valueLabel, text: $row.value, prompt: Text(row.matcherKind.placeholder))
                .keyboardType(.numbersAndPunctuation)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
        case .domain, .domainSuffix, .domainKeyword:
            TextField(row.matcherKind.valueLabel, text: $row.value, prompt: Text(row.matcherKind.placeholder))
                .keyboardType(.URL)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
        }
    }

    private var networkValueBinding: Binding<String> {
        Binding {
            let value = row.value.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
            return value.isEmpty ? "tcp" : value
        } set: { next in
            row.value = next
        }
    }
}
