import ClambhookShared
import Foundation
import SwiftUI
import UniformTypeIdentifiers

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
    @State private var draggedRuleID: RuleEditorRow.ID?

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 12) {
            if model.dashboard.activeProfile.isEmpty {
                IOSSurfaceSection("Profile") {
                    ContentUnavailableView(
                        "No active profile",
                        systemImage: "person.crop.rectangle.stack",
                        description: Text("Choose a profile before editing rules.")
                    )
                }
            } else {
                    IOSSurfaceSection("Profile", detail: model.dashboard.activeProfile) {
                        IOSConsoleMetricStrip(metrics: [
                            IOSConsoleMetric(title: "Manual", value: "\(model.dashboard.rules.rules.count)"),
                            IOSConsoleMetric(title: "Effective", value: "\(model.dashboard.rules.routeTestRules.count)"),
                            IOSConsoleMetric(title: "Rule sets", value: "\(ruleSetStatusCount)"),
                            IOSConsoleMetric(title: "Chains", value: "\(chainNames.count)"),
                        ])
                    }
                }

                IOSSurfaceSection("Test Route", detail: "first match wins") {
                    routeTestControls
                }

                IOSSurfaceSection("Rules", detail: "\(editableRows.count) editable") {
                if editableRows.isEmpty {
                    IOSInlineEmptyState(text: "No manual routing rules.", systemImage: "checklist")
                } else {
                        VStack(spacing: 8) {
                            ForEach(Array(editableRows.enumerated()), id: \.element.id) { index, row in
                                NavigationLink {
                                    IOSRuleFormView(
                                        row: binding(for: row.id),
                                        chainNames: chainNames,
                                        policyGroupNames: policyGroupNames,
                                        rowNumber: index + 1
                                    )
                                } label: {
                                    IOSRuleDraftRow(
                                        row: row,
                                        order: index + 1,
                                        error: firstError(for: row.id),
                                        routeTestResult: routeTestMatchForManualRow(order: index + 1),
                                        manualRuleCount: model.dashboard.rules.rules.count,
                                        effectiveRuleCount: model.dashboard.rules.routeTestRules.count,
                                        isDraggable: true,
                                        onDelete: { deleteEditableRows(at: IndexSet(integer: index)) }
                                    )
                                }
                                .buttonStyle(.plain)
                                .onDrag {
                                    draggedRuleID = row.id
                                    return NSItemProvider(object: row.id.uuidString as NSString)
                                }
                                .onDrop(
                                    of: [UTType.text],
                                    delegate: IOSRuleDropDelegate(
                                        rowID: row.id,
                                        draggedRuleID: $draggedRuleID,
                                        rows: $rows,
                                        validationErrors: $validationErrors,
                                        routeTestResult: $routeTestResult,
                                        chainNames: chainNames,
                                        policyGroupNames: policyGroupNames
                                    )
                                )
                            }
                        }
                    }
                }

            if ruleSetStatusCount > 0 || !generatedRows.isEmpty {
                    IOSSurfaceSection("Rule Sets", detail: ruleSetStatusDetail) {
                    if staticRuleSetRows.isEmpty && subscriptionRuleSetRows.isEmpty {
                        IOSInlineEmptyState(text: "No rule-set status.", systemImage: "tray")
                    } else {
                            VStack(spacing: 8) {
                                HStack(spacing: 8) {
                                    if !staticRuleSetRows.isEmpty {
                                        Button {
                                            model.refreshActiveProfileRuleSets()
                                        } label: {
                                            Label("Refresh Sets", systemImage: "arrow.clockwise")
                                        }
                                        .buttonStyle(.bordered)
                                        .controlSize(.small)
                                    }
                                    if !subscriptionRuleSetRows.isEmpty {
                                        Button {
                                            model.refreshActiveProfileRuleSubscriptions()
                                        } label: {
                                            Label("Refresh Subscriptions", systemImage: "arrow.clockwise.circle")
                                        }
                                        .buttonStyle(.bordered)
                                        .controlSize(.small)
                                    }
                                }
                                ForEach(staticRuleSetRows) { ruleSet in
                                    IOSImportedRuleSetRow(ruleSet: ruleSet)
                                }
                                ForEach(subscriptionRuleSetRows) { subscription in
                                    IOSRuleSubscriptionRow(subscription: subscription)
                                }
                            }
                        }
                    }
                }

            if !generatedRows.isEmpty {
                    IOSSurfaceSection("Rule Set Rules", detail: "\(generatedRows.count) generated") {
                        VStack(spacing: 8) {
                            ForEach(Array(generatedRows.enumerated()), id: \.offset) { index, row in
                                IOSRuleDraftRow(
                                    row: row,
                                    order: model.dashboard.rules.rules.count + index + 1,
                                    error: nil,
                                    routeTestResult: routeTestMatchForGeneratedRow(index: index),
                                    manualRuleCount: model.dashboard.rules.rules.count,
                                    effectiveRuleCount: model.dashboard.rules.routeTestRules.count
                                )
                            }
                        }
                    }
                }

            if let virtualFinalRow {
                    IOSSurfaceSection("Final", detail: "fallback") {
                    NavigationLink {
                        IOSRuleFormView(
                            row: binding(for: virtualFinalRow.id),
                            chainNames: chainNames,
                            policyGroupNames: policyGroupNames,
                            rowNumber: model.dashboard.rules.routeTestRules.count + 1
                        )
                    } label: {
                        IOSRuleDraftRow(
                            row: virtualFinalRow,
                            order: model.dashboard.rules.routeTestRules.count + 1,
                            error: firstError(for: virtualFinalRow.id),
                            routeTestResult: routeTestMatchForFinalRow(),
                            manualRuleCount: model.dashboard.rules.rules.count,
                            effectiveRuleCount: model.dashboard.rules.routeTestRules.count
                        )
                    }
                        .buttonStyle(.plain)
                }
            }

            if !message.isEmpty {
                    IOSSurfaceSection("Status") {
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

    private var routeTestControls: some View {
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
                .disabled(model.dashboard.activeProfile.isEmpty)
            }
            if !routeTestError.isEmpty {
                Text(routeTestError)
                    .font(.footnote)
                    .foregroundStyle(.red)
            } else if let routeTestResult, !routeTestHasInlineMatch {
                IOSInlineRouteTestResultView(
                    response: routeTestResult,
                    manualRuleCount: model.dashboard.rules.rules.count,
                    effectiveRuleCount: model.dashboard.rules.routeTestRules.count
                )
            }
        }
    }

    private var chainNames: [String] {
        model.dashboard.servers.chains.map(\.name)
    }

    private var defaultChainName: String {
        chainNames.first ?? ""
    }

    private var policyGroupNames: [String] {
        model.dashboard.policyGroups.groups.map(\.name)
    }

    private var generatedRows: [RuleEditorRow] {
        RuleEditor.rows(from: model.dashboard.rules.generatedRules, source: .generated)
    }

    private var staticRuleSetRows: [RuleSetStatusPayload] {
        model.dashboard.ruleSets.statuses
    }

    private var subscriptionRuleSetRows: [RuleSubscriptionPayload] {
        model.dashboard.ruleSubscriptions.subscriptions
    }

    private var ruleSetStatusCount: Int {
        staticRuleSetRows.count + subscriptionRuleSetRows.count
    }

    private var ruleSetStatusDetail: String {
        var parts: [String] = []
        if !staticRuleSetRows.isEmpty {
            parts.append("\(staticRuleSetRows.count) imported")
        }
        if !subscriptionRuleSetRows.isEmpty {
            parts.append("\(subscriptionRuleSetRows.count) subscriptions")
        }
        return parts.isEmpty ? "none" : parts.joined(separator: " / ")
    }

    private var routeTestHasInlineMatch: Bool {
        guard let decision = routeTestResult?.decision else {
            return false
        }
        if decision.isDefault {
            return virtualFinalRow != nil
        }
        if decision.ruleNumber > 0 && decision.ruleNumber <= editableRows.count {
            return true
        }
        let firstGenerated = model.dashboard.rules.rules.count + 1
        let lastGenerated = model.dashboard.rules.rules.count + generatedRows.count
        return decision.ruleNumber >= firstGenerated && decision.ruleNumber <= lastGenerated
    }

    private func binding(for id: RuleEditorRow.ID) -> Binding<RuleEditorRow> {
        Binding {
            rows.first(where: { $0.id == id }) ?? RuleEditorRow(name: "", matcherKind: .domainSuffix)
        } set: { next in
            if let index = rows.firstIndex(where: { $0.id == id }) {
                rows[index] = next
                validationErrors = []
                routeTestResult = nil
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

    private func routeTestMatchForManualRow(order: Int) -> RuleTestResponse? {
        guard let routeTestResult,
              !routeTestResult.decision.isDefault,
              routeTestResult.decision.ruleNumber == order else {
            return nil
        }
        return routeTestResult
    }

    private func routeTestMatchForGeneratedRow(index: Int) -> RuleTestResponse? {
        let order = model.dashboard.rules.rules.count + index + 1
        guard let routeTestResult,
              !routeTestResult.decision.isDefault,
              routeTestResult.decision.ruleNumber == order else {
            return nil
        }
        return routeTestResult
    }

    private func routeTestMatchForFinalRow() -> RuleTestResponse? {
        guard let routeTestResult, routeTestResult.decision.isDefault else {
            return nil
        }
        return routeTestResult
    }

    private func loadRowsFromDashboard() {
        rows = RuleEditor.rows(
            from: model.dashboard.rules.rules,
            defaultChainName: defaultChainName,
            includeVirtualFinal: true
        )
        validationErrors = []
        routeTestResult = nil
    }

    private func saveRules() {
        do {
            let nextRules = try RuleEditor.rules(from: rows, chainNames: chainNames, policyGroupNames: policyGroupNames, defaultChainName: defaultChainName)
            try model.replaceActiveProfileRules(nextRules)
            rows = RuleEditor.rows(from: nextRules, defaultChainName: defaultChainName, includeVirtualFinal: true)
            validationErrors = []
            routeTestResult = nil
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
        routeTestResult = nil
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
        routeTestResult = nil
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

private struct IOSRuleDropDelegate: DropDelegate {
    var rowID: RuleEditorRow.ID
    @Binding var draggedRuleID: RuleEditorRow.ID?
    @Binding var rows: [RuleEditorRow]
    @Binding var validationErrors: [RuleEditorValidationError]
    @Binding var routeTestResult: RuleTestResponse?
    var chainNames: [String]
    var policyGroupNames: [String]

    func dropEntered(info: DropInfo) {
        guard let draggedRuleID,
              draggedRuleID != rowID else {
            return
        }
        var editable = rows.filter { !$0.isVirtualFinal }
        guard let from = editable.firstIndex(where: { $0.id == draggedRuleID }),
              let to = editable.firstIndex(where: { $0.id == rowID }) else {
            return
        }
        withAnimation(.snappy) {
            editable.move(fromOffsets: IndexSet(integer: from), toOffset: to > from ? to + 1 : to)
            if let final = rows.first(where: { $0.isVirtualFinal }) {
                rows = editable + [final]
            } else {
                rows = editable
            }
            validationErrors = RuleEditor.validate(rows: rows, chainNames: chainNames, policyGroupNames: policyGroupNames)
            routeTestResult = nil
        }
    }

    func performDrop(info: DropInfo) -> Bool {
        draggedRuleID = nil
        return true
    }
}

private struct IOSRuleDraftRow: View {
    var row: RuleEditorRow
    var order: Int
    var error: RuleEditorValidationError?
    var routeTestResult: RuleTestResponse?
    var manualRuleCount = 0
    var effectiveRuleCount = 0
    var isDraggable = false
    var onDelete: (() -> Void)?

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .top, spacing: 12) {
                VStack(spacing: 6) {
                    Text("\(order)")
                        .font(.caption.monospacedDigit().weight(.semibold))
                        .foregroundStyle(.secondary)
                        .frame(width: 24, alignment: .trailing)
                    if isDraggable {
                        Image(systemName: "line.3.horizontal")
                            .font(.caption.weight(.semibold))
                            .foregroundStyle(.secondary)
                            .accessibilityHidden(true)
                    }
                }

                IOSActionChip(action: row.encodedAction)

                VStack(alignment: .leading, spacing: 6) {
                    HStack(spacing: 6) {
                        Text(rowTitle)
                            .font(.body.weight(.medium))
                            .lineLimit(1)
                        IOSRuleSourceBadge(row: row)
                    }
                    IOSRuleMatcherChips(row: row)
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
                if let onDelete {
                    IOSConsoleIconButton("trash", title: "Delete rule", role: .destructive, action: onDelete)
                }
            }
            if let routeTestResult {
                IOSInlineRouteTestResultView(
                    response: routeTestResult,
                    manualRuleCount: manualRuleCount,
                    effectiveRuleCount: effectiveRuleCount
                )
            }
        }
        .padding(10)
        .background(rowBackground, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
        .overlay {
            if routeTestResult != nil {
                RoundedRectangle(cornerRadius: 7, style: .continuous)
                    .stroke(Color.accentColor.opacity(0.45), lineWidth: 1)
            }
        }
        .accessibilityHint(isDraggable ? "Drag to reorder manual rules." : "")
    }

    private var rowSubtitle: String {
        [row.policySummary, row.isVirtualFinal ? "Fallback when no earlier rule matches" : ""]
            .filter { !$0.isEmpty }
            .joined(separator: " / ")
    }

    private var rowTitle: String {
        row.isVirtualFinal ? "FINAL" : emptyDash(row.name)
    }

    private var rowBackground: Color {
        if routeTestResult != nil {
            return Color.accentColor.opacity(0.10)
        }
        if row.isVirtualFinal {
            return Color.orange.opacity(0.10)
        }
        return Color(.tertiarySystemGroupedBackground)
    }
}

private struct IOSRuleSourceBadge: View {
    var row: RuleEditorRow

    var body: some View {
        if !title.isEmpty {
            Text(title)
                .font(.caption2.weight(.semibold))
                .padding(.horizontal, 6)
                .padding(.vertical, 2)
                .background(Color.secondary.opacity(0.14), in: Capsule())
                .foregroundStyle(.secondary)
        }
    }

    private var title: String {
        if row.isGenerated {
            return "Rule set"
        }
        if row.isVirtualFinal {
            return "Fallback"
        }
        return ""
    }
}

private struct IOSRuleMatcherChips: View {
    var row: RuleEditorRow

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 5) {
                ForEach(chips) { chip in
                    IOSRuleMatcherChip(chip: chip)
                }
            }
        }
        .accessibilityElement(children: .combine)
    }

    private var chips: [IOSRuleMatcherChipData] {
        [
            IOSRuleMatcherChipData(
                kind: row.matcherKind.displayName,
                value: row.matcherSummary
            )
        ]
    }
}

private struct IOSRuleMatcherChipData: Identifiable {
    var kind: String
    var value: String

    var id: String { "\(kind)-\(value)" }
}

private struct IOSRuleMatcherChip: View {
    var chip: IOSRuleMatcherChipData

    var body: some View {
        Text("\(chip.kind) \(chip.value)")
            .font(.caption2.weight(.semibold))
            .lineLimit(1)
            .foregroundStyle(.secondary)
            .padding(.horizontal, 7)
            .padding(.vertical, 3)
            .background(Color.secondary.opacity(0.11), in: Capsule())
    }
}

private struct IOSRuleSubscriptionRow: View {
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
            Text("Subscription / \(formatText) / \(countText)")
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

    private var formatText: String {
        subscription.format.isEmpty ? "default" : subscription.format
    }
}

private struct IOSImportedRuleSetRow: View {
    var ruleSet: RuleSetStatusPayload

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack {
                Text(emptyDash(ruleSet.name))
                    .font(.body.weight(.medium))
                Spacer()
                Text(statusText)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(statusColor)
            }
            if !ruleSet.url.isEmpty {
                Text(ruleSet.url)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Text("Imported / \(formatText) / \(countText)")
                .font(.caption)
                .foregroundStyle(.secondary)
            if !ruleSet.cacheError.isEmpty || !ruleSet.lastError.isEmpty {
                Text([ruleSet.cacheError, ruleSet.lastError].filter { !$0.isEmpty }.joined(separator: " / "))
                    .font(.caption)
                    .foregroundStyle(.red)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 2)
    }

    private var statusText: String {
        if ruleSet.disabled {
            return "Disabled"
        }
        if !ruleSet.cacheError.isEmpty || !ruleSet.lastError.isEmpty {
            return "Error"
        }
        return ruleSet.cached ? "Cached" : "Inline"
    }

    private var statusColor: Color {
        if ruleSet.disabled {
            return .secondary
        }
        if !ruleSet.cacheError.isEmpty || !ruleSet.lastError.isEmpty {
            return .red
        }
        return ruleSet.cached ? .green : .secondary
    }

    private var countText: String {
        [
            "\(ruleSet.domainCount) DOMAIN-SUFFIX",
            "\(ruleSet.cidrCount) IP-CIDR",
            "\(ruleSet.inlineDomainCount) inline domains",
            "\(ruleSet.inlineCIDRCount) inline CIDRs",
            ruleSet.skipped > 0 ? "\(ruleSet.skipped) skipped" : "",
        ]
        .filter { !$0.isEmpty }
        .joined(separator: " / ")
    }

    private var formatText: String {
        ruleSet.format.isEmpty ? "default" : ruleSet.format
    }
}

private struct IOSInlineRouteTestResultView: View {
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
            if !decision.groupName.isEmpty {
                LabeledContent("Group", value: decision.groupName)
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
    var policyGroupNames: [String]
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
                if row.policyKind == .group {
                    Picker("Policy Group", selection: $row.chainName) {
                        if policyGroupNames.isEmpty {
                            Text("No groups").tag("")
                        }
                        ForEach(policyGroupNames, id: \.self) { group in
                            Text(group).tag(group)
                        }
                    }
                    .disabled(policyGroupNames.isEmpty)
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
