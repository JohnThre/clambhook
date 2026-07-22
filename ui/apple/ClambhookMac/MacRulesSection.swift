import AppKit
import ClambhookShared
import SwiftUI

// MARK: - Rules

struct MacRulesSection: View {
    @ObservedObject var model: AppleAppModel

    // Editor state
    @State private var isEditing = false
    @State private var draftRows: [RuleEditorRow] = []
    @State private var saveError = ""
    @State private var showAddSheet = false

    // Route tester / explain state
    @State private var routeTestNetwork = "tcp"
    @State private var routeTestTarget = "example.com:443"
    @State private var routeTestSource = ""
    @State private var testResult: RuleTestResponse?
    @State private var explainResult: RuleTestResponse?
    @State private var testerError = ""

    var body: some View {
        HSplitView {
            rulesPanel
                .frame(minWidth: 300)
            testerPanel
                .frame(minWidth: 240)
        }
        .sheet(isPresented: $showAddSheet) {
            MacRuleAddSheet(
                chainNames: model.dashboard.servers.chains.map { $0.name },
                policyGroupNames: model.dashboard.policyGroups.groups.map { $0.name }
            ) { newRow in
                draftRows.append(newRow)
            }
        }
        .onChange(of: model.dashboard.rules.rules) {
            if !isEditing { rebuildDraftRows() }
        }
        .onAppear { rebuildDraftRows() }
    }

    // MARK: - Left panel: ordered rule list / editor

    private var rulesPanel: some View {
        VStack(alignment: .leading, spacing: 0) {
            rulesPanelHeader
            Divider()
            if !saveError.isEmpty {
                Text(saveError)
                    .font(.caption)
                    .foregroundStyle(.red)
                    .padding(.horizontal, 16)
                    .padding(.top, 8)
            }
            rulesList
            if !model.dashboard.rules.ruleSets.isEmpty {
                Divider()
                ruleSetsSection
            }
        }
    }

    private var rulesPanelHeader: some View {
        HStack(spacing: 8) {
            Text("Rules")
                .font(.headline)
            Spacer()
            if isEditing {
                Button {
                    showAddSheet = true
                } label: {
                    Image(systemName: "plus")
                }
                .buttonStyle(.borderless)
                Button("Cancel") {
                    isEditing = false
                    saveError = ""
                    rebuildDraftRows()
                }
                .buttonStyle(.borderless)
                Button("Save") {
                    saveError = ""
                    do {
                        let chainNames = model.dashboard.servers.chains.map { $0.name }
                        let policyGroupNames = model.dashboard.policyGroups.groups.map { $0.name }
                        let defaultChainName = model.dashboard.servers.chains.first?.name ?? ""
                        _ = try RuleEditor.rules(
                            from: draftRows,
                            chainNames: chainNames,
                            policyGroupNames: policyGroupNames,
                            defaultChainName: defaultChainName
                        )
                        model.saveRules(draftRows)
                        isEditing = false
                    } catch let err as RuleEditorValidationFailure {
                        saveError = err.localizedDescription
                    } catch {
                        saveError = error.localizedDescription
                    }
                }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
            } else {
                Button {
                    isEditing = true
                    saveError = ""
                    rebuildDraftRows()
                } label: {
                    Label("Edit", systemImage: "pencil")
                }
                .buttonStyle(.borderless)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
    }

    private var rulesList: some View {
        List {
            if isEditing {
                ForEach($draftRows) { $row in
                    RuleEditorRowView(
                        row: $row,
                        chainNames: model.dashboard.servers.chains.map { $0.name },
                        policyGroupNames: model.dashboard.policyGroups.groups.map { $0.name }
                    )
                    .listRowSeparator(.visible)
                }
                .onMove { from, to in draftRows.move(fromOffsets: from, toOffset: to) }
                .onDelete { offsets in draftRows.remove(atOffsets: offsets) }
            } else {
                if draftRows.isEmpty {
                    Text("No routing rules")
                        .foregroundStyle(.secondary)
                        .listRowSeparator(.hidden)
                } else {
                    ForEach(Array(draftRows.enumerated()), id: \.element.id) { index, row in
                        RuleReadOnlyRowView(index: index, row: row)
                            .listRowSeparator(.visible)
                    }
                }
            }
        }
        .listStyle(.plain)
    }

    private var ruleSetsSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("Rule Sets")
                    .font(.subheadline.weight(.semibold))
                Spacer()
                Button {
                    model.refreshActiveProfileRuleSets()
                } label: {
                    Image(systemName: "arrow.clockwise")
                }
                .buttonStyle(.borderless)
            }
            .padding(.horizontal, 16)
            .padding(.top, 10)
            ForEach(model.dashboard.rules.ruleSets) { rs in
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(rs.name).fontWeight(.medium).font(.caption)
                        Text(rs.url).font(.caption2).foregroundStyle(.secondary).lineLimit(1)
                    }
                    Spacer()
                    VStack(alignment: .trailing, spacing: 2) {
                        Text(rs.cached ? "Cached" : "Not cached")
                            .font(.caption2)
                            .foregroundStyle(rs.cached ? .green : .secondary)
                        if rs.domainCount + rs.cidrCount > 0 {
                            Text("\(rs.domainCount)d / \(rs.cidrCount)c")
                                .font(.caption2).foregroundStyle(.secondary)
                        }
                    }
                }
                .padding(.horizontal, 16)
            }
            Spacer(minLength: 12)
        }
    }

    // MARK: - Right panel: route tester + explain

    private var testerPanel: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                Text("Route Tester")
                    .font(.headline)
                testerControls
                if !testerError.isEmpty {
                    Text(testerError)
                        .font(.caption)
                        .foregroundStyle(.red)
                }
                if let result = testResult {
                    RouteResultCard(title: "Test Result", result: result, showHops: false)
                }
                if let result = explainResult {
                    RouteResultCard(title: "Explain Result", result: result, showHops: true)
                }
            }
            .padding(16)
        }
    }

    private var testerControls: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Picker("Network", selection: $routeTestNetwork) {
                    Text("TCP").tag("tcp")
                    Text("UDP").tag("udp")
                }
                .labelsHidden()
                .pickerStyle(.segmented)
                .frame(width: 110)
                TextField("host:port", text: $routeTestTarget)
                    .textFieldStyle(.roundedBorder)
            }
            TextField("Source IP (optional)", text: $routeTestSource)
                .textFieldStyle(.roundedBorder)
                .font(.caption)
            HStack(spacing: 8) {
                Button {
                    testerError = ""
                    testResult = nil
                    Task {
                        do {
                            testResult = try await model.testRule(
                                network: routeTestNetwork,
                                target: routeTestTarget
                            )
                        } catch {
                            testerError = error.localizedDescription
                        }
                    }
                } label: {
                    Label("Test", systemImage: "checkmark.circle")
                }
                Button {
                    testerError = ""
                    explainResult = nil
                    Task {
                        do {
                            explainResult = try await model.explainRoute(
                                network: routeTestNetwork,
                                target: routeTestTarget,
                                source: routeTestSource
                            )
                        } catch {
                            testerError = error.localizedDescription
                        }
                    }
                } label: {
                    Label("Explain", systemImage: "questionmark.circle")
                }
            }
        }
    }

    // MARK: - Helpers

    private func rebuildDraftRows() {
        let defaultChain = model.dashboard.servers.chains.first?.name ?? ""
        draftRows = RuleEditor.rows(
            from: model.dashboard.rules.rules,
            defaultChainName: defaultChain,
            includeVirtualFinal: true
        )
    }
}

// MARK: Rule read-only row

private struct RuleReadOnlyRowView: View {
    var index: Int
    var row: RuleEditorRow

    var body: some View {
        HStack(spacing: 10) {
            Text("\(index + 1)")
                .font(.caption2.monospacedDigit())
                .foregroundStyle(.secondary)
                .frame(width: 22, alignment: .trailing)
            MatcherChip(kind: row.matcherKind, value: row.matcherSummary)
            Text("→")
                .foregroundStyle(.secondary)
                .font(.caption)
            PolicyBadge(row: row)
            Spacer()
            Text(row.name)
                .font(.caption2)
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .padding(.vertical, 2)
    }
}

// MARK: Rule editor row (edit mode)

private struct RuleEditorRowView: View {
    @Binding var row: RuleEditorRow
    var chainNames: [String]
    var policyGroupNames: [String]

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Picker("Type", selection: $row.matcherKind) {
                    ForEach(RuleMatcherKind.editableCases) { kind in
                        Text(kind.displayName).tag(kind)
                    }
                }
                .labelsHidden()
                .frame(width: 150)
                if row.matcherKind != .allTraffic {
                    TextField(row.matcherKind.placeholder, text: $row.value)
                        .textFieldStyle(.roundedBorder)
                        .font(.caption)
                }
            }
            HStack(spacing: 8) {
                Picker("Policy", selection: $row.policyKind) {
                    ForEach(RulePolicyKind.allCases) { kind in
                        Text(kind.displayName).tag(kind)
                    }
                }
                .labelsHidden()
                .frame(width: 90)
                if row.policyKind == .proxy {
                    Picker("Chain", selection: $row.chainName) {
                        ForEach(chainNames, id: \.self) { name in
                            Text(name).tag(name)
                        }
                    }
                    .labelsHidden()
                    .frame(width: 120)
                } else if row.policyKind == .group {
                    Picker("Group", selection: $row.chainName) {
                        ForEach(policyGroupNames, id: \.self) { name in
                            Text(name).tag(name)
                        }
                    }
                    .labelsHidden()
                    .frame(width: 120)
                }
                Spacer()
                TextField("Name", text: $row.name)
                    .textFieldStyle(.roundedBorder)
                    .font(.caption)
                    .frame(width: 120)
            }
        }
        .padding(.vertical, 2)
        .opacity(row.isGenerated ? 0.5 : 1)
        .disabled(row.isGenerated)
    }
}

// MARK: Matcher chip

private struct MatcherChip: View {
    var kind: RuleMatcherKind
    var value: String

    var body: some View {
        Text(value)
            .font(.caption.weight(.medium))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(chipColor.opacity(0.15))
            .foregroundStyle(chipColor)
            .clipShape(RoundedRectangle(cornerRadius: 4))
            .lineLimit(1)
    }

    private var chipColor: Color {
        switch kind {
        case .domain, .domainSuffix, .domainKeyword: return .blue
        case .cidr: return .orange
        case .port: return .purple
        case .network: return .teal
        case .allTraffic: return .gray
        case .combined: return .indigo
        }
    }
}

// MARK: Policy badge

private struct PolicyBadge: View {
    var row: RuleEditorRow

    var body: some View {
        Text(row.policySummary)
            .font(.caption.weight(.medium))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(badgeColor.opacity(0.15))
            .foregroundStyle(badgeColor)
            .clipShape(RoundedRectangle(cornerRadius: 4))
            .lineLimit(1)
    }

    private var badgeColor: Color {
        switch row.policyKind {
        case .direct: return .green
        case .block, .reject: return .red
        case .proxy: return .blue
        case .group: return .purple
        }
    }
}

// MARK: Route result card

private struct RouteResultCard: View {
    var title: String
    var result: RuleTestResponse
    var showHops: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title)
                .font(.subheadline.weight(.semibold))
            Divider()
            HStack(spacing: 8) {
                actionBadge
                VStack(alignment: .leading, spacing: 2) {
                    let ruleName = result.decision.ruleName.isEmpty ? "Default" : result.decision.ruleName
                    Text(ruleName)
                        .font(.caption.weight(.medium))
                    if result.decision.isDefault {
                        Text("No rule matched")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    } else {
                        Text("Rule #\(result.decision.ruleNumber)")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            if !result.decision.chainName.isEmpty {
                LabeledContent("Chain") {
                    Text(result.decision.chainName).font(.caption)
                }
                .font(.caption)
            }
            if !result.decision.groupName.isEmpty {
                LabeledContent("Group") {
                    Text(result.decision.groupName).font(.caption)
                }
                .font(.caption)
            }
            if result.decision.elapsedNs > 0 {
                LabeledContent("Elapsed") {
                    Text("\(result.decision.elapsedNs / 1_000) µs").font(.caption)
                }
                .font(.caption)
            }
            if showHops, !result.hops.isEmpty {
                Divider()
                Text("Hops")
                    .font(.caption.weight(.semibold))
                ForEach(result.hops) { hop in
                    HStack(spacing: 6) {
                        Text(hop.protocol.uppercased())
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .frame(width: 40, alignment: .leading)
                        Text(hop.name)
                            .font(.caption)
                        Spacer()
                        Text(hop.address)
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                }
            }
        }
        .padding(10)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
    }

    private var actionBadge: some View {
        let action = result.decision.action
        let color: Color = {
            switch action {
            case "direct": return .green
            case "block", "reject": return .red
            default: return action.hasPrefix("group:") ? .purple : .blue
            }
        }()
        return Text(action)
            .font(.caption.weight(.bold))
            .padding(.horizontal, 7)
            .padding(.vertical, 3)
            .background(color.opacity(0.15))
            .foregroundStyle(color)
            .clipShape(RoundedRectangle(cornerRadius: 5))
    }
}

// MARK: - Add Rule Sheet

struct MacRuleAddSheet: View {
    var chainNames: [String]
    var policyGroupNames: [String]
    var onAdd: (RuleEditorRow) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var matcherKind = RuleMatcherKind.domainSuffix
    @State private var value = ""
    @State private var policyKind = RulePolicyKind.direct
    @State private var chainName = ""
    @State private var validationError = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Add Rule")
                .font(.headline)

            TextField("Rule name", text: $name)
                .textFieldStyle(.roundedBorder)

            Picker("Match type", selection: $matcherKind) {
                ForEach(RuleMatcherKind.editableCases) { kind in
                    Text(kind.displayName).tag(kind)
                }
            }
            .pickerStyle(.menu)

            if matcherKind != .allTraffic {
                TextField(matcherKind.placeholder, text: $value)
                    .textFieldStyle(.roundedBorder)
            }

            Picker("Action", selection: $policyKind) {
                ForEach(RulePolicyKind.allCases) { kind in
                    Text(kind.displayName).tag(kind)
                }
            }
            .pickerStyle(.menu)
            .onChange(of: policyKind) { chainName = "" }

            if policyKind == .proxy {
                Picker("Chain", selection: $chainName) {
                    Text("(select chain)").tag("")
                    ForEach(chainNames, id: \.self) { n in Text(n).tag(n) }
                }
                .pickerStyle(.menu)
            } else if policyKind == .group {
                Picker("Group", selection: $chainName) {
                    Text("(select group)").tag("")
                    ForEach(policyGroupNames, id: \.self) { n in Text(n).tag(n) }
                }
                .pickerStyle(.menu)
            }

            if !validationError.isEmpty {
                Text(validationError)
                    .font(.caption)
                    .foregroundStyle(.red)
            }

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                Button("Add") { addRule() }
                    .buttonStyle(.borderedProminent)
                    .disabled(name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding(20)
        .frame(width: 340)
        .onAppear {
            chainName = chainNames.first ?? ""
        }
    }

    private func addRule() {
        validationError = ""
        let trimmedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let row = RuleEditorRow(
            name: trimmedName,
            matcherKind: matcherKind,
            value: matcherKind == .allTraffic ? "" : value.trimmingCharacters(in: .whitespacesAndNewlines),
            policyKind: policyKind,
            chainName: chainName
        )
        let errors = RuleEditor.validate(
            rows: [row],
            chainNames: chainNames,
            policyGroupNames: policyGroupNames
        )
        if let first = errors.first {
            validationError = first.message
            return
        }
        onAdd(row)
        dismiss()
    }
}
