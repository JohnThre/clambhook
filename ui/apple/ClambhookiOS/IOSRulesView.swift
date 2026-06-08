import ClambhookShared
import SwiftUI

struct IOSRulesView: View {
    @ObservedObject var model: AppleAppModel
    @State private var rows: [RuleEditorRow] = []
    @State private var validationErrors: [RuleEditorValidationError] = []
    @State private var message = ""
    @State private var loaded = false
    @State private var routeTestNetwork = "tcp"
    @State private var routeTestTarget = "example.com:443"
    @State private var routeTestResult: RuleTestResponse?
    @State private var routeTestError = ""

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 12) {
            if model.dashboard.activeProfile.isEmpty {
                IOSConsoleSection("Profile") {
                    ContentUnavailableView(
                        "No active profile",
                        systemImage: "person.crop.rectangle.stack",
                        description: Text("Choose a profile before editing rules.")
                    )
                }
            } else {
                    IOSConsoleSection("Profile", detail: model.dashboard.activeProfile) {
                        IOSConsoleMetricStrip(metrics: [
                            IOSConsoleMetric(title: "Manual", value: "\(model.dashboard.rules.rules.count)"),
                            IOSConsoleMetric(title: "Effective", value: "\(model.dashboard.rules.routeTestRules.count)"),
                            IOSConsoleMetric(title: "Rule sets", value: "\(ruleSetRows.count)"),
                            IOSConsoleMetric(title: "Chains", value: "\(chainNames.count)"),
                        ])
                    }
                }

                IOSConsoleSection("Rules", detail: "\(editableRows.count) editable") {
                if editableRows.isEmpty {
                    IOSInlineEmptyState(text: "No manual routing rules.", systemImage: "checklist")
                } else {
                        VStack(spacing: 8) {
                            ForEach(Array(editableRows.enumerated()), id: \.element.id) { index, row in
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
                                        error: firstError(for: row.id),
                                        canMoveUp: index > 0,
                                        canMoveDown: index < editableRows.count - 1,
                                        onMoveUp: { moveEditableRow(at: index, by: -1) },
                                        onMoveDown: { moveEditableRow(at: index, by: 1) },
                                        onDelete: { deleteEditableRows(at: IndexSet(integer: index)) }
                                    )
                                }
                                .buttonStyle(.plain)
                            }
                        }
                    }
                }

            if !ruleSetRows.isEmpty || !generatedRows.isEmpty {
                    IOSConsoleSection("Rule Sets", detail: "\(ruleSetRows.count) subscriptions") {
                    if ruleSetRows.isEmpty {
                        IOSInlineEmptyState(text: "No rule-set status.", systemImage: "tray")
                    } else {
                            VStack(spacing: 8) {
                                ForEach(ruleSetRows) { subscription in
                                    IOSRuleSetRow(subscription: subscription)
                                }
                            }
                        }
                    }
                }

            if !generatedRows.isEmpty {
                    IOSConsoleSection("Rule Set Rules", detail: "\(generatedRows.count) generated") {
                        VStack(spacing: 8) {
                            ForEach(Array(generatedRows.enumerated()), id: \.offset) { index, row in
                                IOSRuleDraftRow(
                                    row: row,
                                    order: model.dashboard.rules.rules.count + index + 1,
                                    error: nil
                                )
                            }
                        }
                    }
                }

            if let virtualFinalRow {
                    IOSConsoleSection("Final", detail: "fallback") {
                    NavigationLink {
                        IOSRuleFormView(
                            row: binding(for: virtualFinalRow.id),
                            chainNames: chainNames,
                            rowNumber: model.dashboard.rules.routeTestRules.count + 1
                        )
                    } label: {
                        IOSRuleDraftRow(
                            row: virtualFinalRow,
                            order: model.dashboard.rules.routeTestRules.count + 1,
                            error: firstError(for: virtualFinalRow.id)
                        )
                    }
                        .buttonStyle(.plain)
                }
            }

                IOSConsoleSection("Test Route", detail: "first match wins") {
                    VStack(alignment: .leading, spacing: 8) {
                        Picker("Network", selection: $routeTestNetwork) {
                            Text("TCP").tag("tcp")
                            Text("UDP").tag("udp")
                        }
                        .pickerStyle(.segmented)
                        HStack(spacing: 8) {
                            TextField("host:port", text: $routeTestTarget)
                                .textInputAutocapitalization(.never)
                                .autocorrectionDisabled()
                                .textFieldStyle(.roundedBorder)
                            Button {
                                runRouteTest()
                            } label: {
                                Image(systemName: "checkmark.circle")
                                    .frame(width: 30, height: 30)
                            }
                            .buttonStyle(.borderedProminent)
                            .accessibilityLabel("Test Route")
                        }
                if !routeTestError.isEmpty {
                    Text(routeTestError)
                        .font(.footnote)
                        .foregroundStyle(.red)
                } else if let routeTestResult {
                    IOSRouteTestResultView(
                        response: routeTestResult,
                        manualRuleCount: model.dashboard.rules.rules.count,
                        effectiveRuleCount: model.dashboard.rules.routeTestRules.count
                    )
                }
            }
                }

            if !message.isEmpty {
                    IOSConsoleSection("Status") {
                    Text(message)
                        .font(.footnote)
                        .foregroundColor(validationErrors.isEmpty ? Color.secondary : Color.red)
                }
            }
        }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
        }
        .background(Color(.systemGroupedBackground))
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

    private var defaultChainName: String {
        chainNames.first ?? ""
    }

    private var generatedRows: [RuleEditorRow] {
        RuleEditor.rows(from: model.dashboard.rules.generatedRules, source: .generated)
    }

    private var ruleSetRows: [RuleSubscriptionPayload] {
        model.dashboard.ruleSubscriptions.subscriptions
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

    private var editableRows: [RuleEditorRow] {
        rows.filter { !$0.isVirtualFinal }
    }

    private var virtualFinalRow: RuleEditorRow? {
        rows.first { $0.isVirtualFinal }
    }

    private func firstError(for id: RuleEditorRow.ID) -> RuleEditorValidationError? {
        guard let index = rows.firstIndex(where: { $0.id == id }) else {
            return nil
        }
        return validationErrors.first { $0.rowIndex == index }
    }

    private func loadRowsFromDashboard() {
        rows = RuleEditor.rows(
            from: model.dashboard.rules.rules,
            defaultChainName: defaultChainName,
            includeVirtualFinal: true
        )
        validationErrors = []
    }

    private func saveRules() {
        do {
            let nextRules = try RuleEditor.rules(from: rows, chainNames: chainNames, defaultChainName: defaultChainName)
            try model.replaceActiveProfileRules(nextRules)
            rows = RuleEditor.rows(from: nextRules, defaultChainName: defaultChainName, includeVirtualFinal: true)
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
        let row = RuleEditorRow(
            name: "new-rule",
            matcherKind: .domainSuffix,
            policyKind: .proxy,
            chainName: defaultChainName
        )
        if let finalIndex = rows.firstIndex(where: { $0.matcherKind == .allTraffic }) {
            rows.insert(row, at: finalIndex)
        } else {
            rows.append(row)
        }
        validationErrors = []
    }

    private func deleteEditableRows(at offsets: IndexSet) {
        let editable = editableRows
        let ids = offsets.compactMap { index -> RuleEditorRow.ID? in
            guard editable.indices.contains(index) else {
                return nil
            }
            return editable[index].id
        }
        rows.removeAll { ids.contains($0.id) }
        appendVirtualFinalIfNeeded()
    }

    private func moveEditableRows(from offsets: IndexSet, to destination: Int) {
        var editable = editableRows
        editable.move(fromOffsets: offsets, toOffset: destination)
        if let virtualFinalRow {
            rows = editable + [virtualFinalRow]
        } else {
            rows = editable
        }
    }

    private func moveEditableRow(at index: Int, by delta: Int) {
        let target = index + delta
        guard editableRows.indices.contains(index), editableRows.indices.contains(target) else {
            return
        }
        var editable = editableRows
        editable.swapAt(index, target)
        if let virtualFinalRow {
            rows = editable + [virtualFinalRow]
        } else {
            rows = editable
        }
        validationErrors = RuleEditor.validate(rows: rows, chainNames: chainNames)
    }

    private func appendVirtualFinalIfNeeded() {
        guard !rows.contains(where: { $0.matcherKind == .allTraffic }) else {
            return
        }
        rows.append(contentsOf: RuleEditor.rows(from: [], defaultChainName: defaultChainName, includeVirtualFinal: true))
    }

    private func runRouteTest() {
        routeTestError = ""
        Task {
            do {
                routeTestResult = try await model.testRule(network: routeTestNetwork, target: routeTestTarget)
            } catch {
                routeTestResult = nil
                routeTestError = error.localizedDescription
            }
        }
    }
}

private struct IOSRuleDraftRow: View {
    var row: RuleEditorRow
    var order: Int
    var error: RuleEditorValidationError?
    var canMoveUp = false
    var canMoveDown = false
    var onMoveUp: (() -> Void)?
    var onMoveDown: (() -> Void)?
    var onDelete: (() -> Void)?

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Text("\(order)")
                .font(.caption.monospacedDigit().weight(.semibold))
                .foregroundStyle(.secondary)
                .frame(width: 24, alignment: .trailing)
                .padding(.top, 3)

            IOSActionChip(action: row.encodedAction)

            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text(emptyDash(row.name))
                        .font(.body.weight(.medium))
                        .lineLimit(1)
                    if row.isGenerated {
                        Text("Rule set")
                            .font(.caption2.weight(.semibold))
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(Color.secondary.opacity(0.14), in: Capsule())
                            .foregroundStyle(.secondary)
                    } else if row.isVirtualFinal {
                        Text("Virtual")
                            .font(.caption2.weight(.semibold))
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(Color.secondary.opacity(0.14), in: Capsule())
                            .foregroundStyle(.secondary)
                    }
                }
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
            Spacer(minLength: 8)
            if onMoveUp != nil || onMoveDown != nil || onDelete != nil {
                VStack(spacing: 6) {
                    if let onMoveUp {
                        IOSConsoleIconButton("chevron.up", title: "Move rule up", action: onMoveUp)
                            .disabled(!canMoveUp)
                    }
                    if let onMoveDown {
                        IOSConsoleIconButton("chevron.down", title: "Move rule down", action: onMoveDown)
                            .disabled(!canMoveDown)
                    }
                    if let onDelete {
                        IOSConsoleIconButton("trash", title: "Delete rule", role: .destructive, action: onDelete)
                    }
                }
            }
        }
        .padding(10)
        .background(Color(.tertiarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 7, style: .continuous))
    }

    private var rowSubtitle: String {
        [row.matcherKind.displayName, row.matcherSummary, row.policySummary]
            .filter { !$0.isEmpty }
            .joined(separator: " / ")
    }
}

private struct IOSRuleSetRow: View {
    var subscription: RuleSubscriptionPayload

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack {
                Text(emptyDash(subscription.name))
                    .font(.body.weight(.medium))
                Spacer()
                Text(statusText)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(statusColor)
            }
            Text(emptyDash(subscription.url))
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            Text(countText)
                .font(.caption)
                .foregroundStyle(.secondary)
            if !subscription.generatedRules.isEmpty {
                Text(subscription.generatedRules.joined(separator: ", "))
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
            if !subscription.cacheError.isEmpty || !subscription.lastError.isEmpty {
                Text([subscription.cacheError, subscription.lastError].filter { !$0.isEmpty }.joined(separator: " / "))
                    .font(.caption)
                    .foregroundStyle(.red)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 2)
    }

    private var statusText: String {
        if subscription.disabled {
            return "Disabled"
        }
        if !subscription.cacheError.isEmpty || !subscription.lastError.isEmpty {
            return "Error"
        }
        return subscription.cached ? "Cached" : "Not cached"
    }

    private var statusColor: Color {
        if subscription.disabled {
            return .secondary
        }
        if !subscription.cacheError.isEmpty || !subscription.lastError.isEmpty {
            return .red
        }
        return subscription.cached ? .green : .secondary
    }

    private var countText: String {
        var parts = [
            "\(subscription.domainCount) DOMAIN-SUFFIX",
            "\(subscription.cidrCount) IP-CIDR",
        ]
        if !subscription.networks.isEmpty {
            parts.append(subscription.networks.map { $0.uppercased() }.joined(separator: ", "))
        }
        return parts.joined(separator: " / ")
    }
}

private struct IOSRouteTestResultView: View {
    var response: RuleTestResponse
    var manualRuleCount: Int
    var effectiveRuleCount: Int

    var body: some View {
        let decision = response.decision
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                IOSActionChip(action: decision.action)
                Text(sourceText)
                    .font(.body.weight(.medium))
                Spacer()
            }
            Text(routeTestSummary(response))
                .font(.footnote)
                .foregroundStyle(.secondary)
            LabeledContent("Target", value: emptyDash(decision.target))
            LabeledContent("Network", value: emptyDash(decision.network).uppercased())
            if !decision.chainName.isEmpty {
                LabeledContent("Chain", value: decision.chainName)
            }
            if let chain = response.chain {
                LabeledContent("Hops", value: "\(chain.hopCount)")
                LabeledContent("UDP", value: udpSupportText(chain.capabilities))
            }
        }
        .font(.footnote)
    }

    private var sourceText: String {
        let decision = response.decision
        if decision.isDefault {
            return "FINAL default"
        }
        if decision.ruleNumber > 0 && decision.ruleNumber <= manualRuleCount {
            return "Manual rule #\(decision.ruleNumber)"
        }
        if decision.ruleNumber > manualRuleCount && decision.ruleNumber <= effectiveRuleCount {
            return "Rule set rule #\(decision.ruleNumber)"
        }
        if decision.ruleNumber > 0 {
            return "Rule #\(decision.ruleNumber)"
        }
        return "Matched rule"
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
                LabeledContent("Source", value: sourceLabel)
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
                .disabled(row.matcherKind == .combined || row.isVirtualFinal)
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

    private var sourceLabel: String {
        switch row.source {
        case .manual:
            return "Manual"
        case .generated:
            return "Rule set"
        case .virtualFinal:
            return "Virtual FINAL"
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
