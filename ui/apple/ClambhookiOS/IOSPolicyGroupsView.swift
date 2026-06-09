import ClambhookShared
import SwiftUI

struct IOSPolicyGroupsView: View {
    @ObservedObject var model: AppleAppModel
    @State private var draftGroups: [PolicyGroupPayload] = []
    @State private var loaded = false
    @State private var message = ""

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 12) {
                IOSSurfaceSection("Route Mix", detail: activeProfileText) {
                    IOSMetricsGrid(metrics: [
                        IOSMetric(title: "Proxy", value: "\(summary.proxyCount)", systemImage: "shield.lefthalf.filled"),
                        IOSMetric(title: "Direct", value: "\(summary.directCount)", systemImage: "arrow.up.right"),
                        IOSMetric(title: "Block", value: "\(summary.blockCount)", systemImage: "hand.raised.fill"),
                        IOSMetric(title: "Groups", value: "\(model.dashboard.policyGroups.groups.count)", systemImage: "point.3.connected.trianglepath.dotted"),
                    ])
                }

                IOSSurfaceSection("Policy Groups", detail: policyGroupDetail) {
                    if draftGroups.filter({ !$0.hidden }).isEmpty {
                        ContentUnavailableView(
                            "No policy groups",
                            systemImage: "point.3.connected.trianglepath.dotted",
                            description: Text("Static profile routes are shown below.")
                        )
                    } else {
                        VStack(spacing: 10) {
                            ForEach(draftGroups.filter { !$0.hidden }) { group in
                                IOSPolicyGroupCard(group: group) { chain in
                                    model.selectPolicyGroup(group: group.name, chain: chain)
                                } onDelete: {
                                    deleteGroup(group)
                                } destination: {
                                    IOSPolicyGroupFormView(group: binding(for: group.id), chainNames: chainNames)
                                }
                            }
                        }
                    }
                }

                let hiddenGroups = draftGroups.filter(\.hidden)
                if !hiddenGroups.isEmpty {
                    IOSSurfaceSection("Hidden Groups", detail: "\(hiddenGroups.count)") {
                        VStack(spacing: 10) {
                            ForEach(hiddenGroups) { group in
                                IOSPolicyGroupCard(group: group) { chain in
                                    model.selectPolicyGroup(group: group.name, chain: chain)
                                } onDelete: {
                                    deleteGroup(group)
                                } destination: {
                                    IOSPolicyGroupFormView(group: binding(for: group.id), chainNames: chainNames)
                                }
                            }
                        }
                    }
                }

                IOSSurfaceSection("Current Routes", detail: "\(summary.routes.count)") {
                    if summary.routes.isEmpty {
                        IOSInlineEmptyState(text: "No route summary is available.", systemImage: "arrow.triangle.branch")
                    } else {
                        VStack(spacing: 8) {
                            ForEach(Array(summary.routes.prefix(8))) { route in
                                IOSPolicyRouteRow(route: route)
                            }
                        }
                    }
                }

            if !summary.topRuleHits.isEmpty {
                    IOSSurfaceSection("Rule Hits", detail: "\(summary.topRuleHits.count)") {
                        VStack(spacing: 8) {
                            ForEach(summary.topRuleHits) { hit in
                                HStack(spacing: 10) {
                                    IOSActionChip(action: hit.action)
                                    Text(hit.ruleName.isEmpty ? "Default route" : hit.ruleName)
                                        .font(.subheadline.weight(.medium))
                                        .lineLimit(1)
                                    Spacer(minLength: 8)
                                    Text("\(hit.count)")
                                        .font(.subheadline.weight(.semibold))
                                        .monospacedDigit()
                                        .foregroundStyle(.secondary)
                                }
                            }
                        }
                    }
            }

                if !message.isEmpty {
                    IOSSurfaceSection("Status") {
                        Text(message)
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                }
        }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
        }
        .background(Color(.systemGroupedBackground))
        .navigationTitle("Policy Groups")
        .toolbar {
            ToolbarItem(placement: .topBarLeading) {
                Button("Apply") {
                    applyGroups()
                }
                .disabled(model.dashboard.activeProfile.isEmpty)
            }
            ToolbarItem(placement: .topBarTrailing) {
                Button {
                    addGroup()
                } label: {
                    Image(systemName: "plus")
                }
                .disabled(model.dashboard.activeProfile.isEmpty || chainNames.isEmpty)
            }
        }
        .refreshable {
            await model.refreshNow()
            loadDraftGroups()
        }
        .onAppear {
            if !loaded {
                loadDraftGroups()
                loaded = true
            }
        }
        .onChange(of: model.dashboard.policyGroups.groups) { _, _ in
            loadDraftGroups()
        }
    }

    private var summary: PolicySelectorSummary {
        model.dashboard.policySelectorSummary
    }

    private var activeProfileText: String {
        model.dashboard.activeProfile.isEmpty ? "No active profile" : model.dashboard.activeProfile
    }

    private var policyGroupDetail: String {
        let count = draftGroups.count
        return count == 1 ? "1 group" : "\(count) groups"
    }

    private var chainNames: [String] {
        model.dashboard.servers.chains.map(\.name)
    }

    private func loadDraftGroups() {
        draftGroups = model.dashboard.policyGroups.groups
    }

    private func binding(for id: PolicyGroupPayload.ID) -> Binding<PolicyGroupPayload> {
        Binding {
            draftGroups.first(where: { $0.id == id }) ?? PolicyGroupPayload()
        } set: { next in
            if let index = draftGroups.firstIndex(where: { $0.id == id }) {
                draftGroups[index] = next
                message = ""
            }
        }
    }

    private func addGroup() {
        let base = "group"
        var name = base
        var index = 1
        let existing = Set(draftGroups.map(\.name))
        while existing.contains(name) {
            index += 1
            name = "\(base)-\(index)"
        }
        draftGroups.append(PolicyGroupPayload(
            name: name,
            type: "select",
            chains: Array(chainNames.prefix(1)),
            selectedChain: chainNames.first ?? "",
            selected: chainNames.first ?? "",
            selectionMode: "manual",
            selectionReason: "manual"
        ))
    }

    private func deleteGroup(_ group: PolicyGroupPayload) {
        draftGroups.removeAll { $0.id == group.id }
        message = ""
    }

    private func applyGroups() {
        do {
            try model.replaceActiveProfilePolicyGroups(draftGroups)
            message = "Applied policy groups."
        } catch {
            message = error.localizedDescription
        }
    }
}

private struct IOSPolicyGroupCard: View {
    var group: PolicyGroupPayload
    var onSelect: (String) -> Void
    var onDelete: () -> Void
    var destination: () -> IOSPolicyGroupFormView

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .top, spacing: 10) {
                Image(systemName: "point.3.connected.trianglepath.dotted")
                    .foregroundStyle(.secondary)
                    .frame(width: 24)

                VStack(alignment: .leading, spacing: 4) {
                    Text(group.name.isEmpty ? "Policy group" : group.name)
                        .font(.subheadline.weight(.semibold))
                        .lineLimit(1)
                    Text([modeText, selectedText].filter { !$0.isEmpty }.joined(separator: " / "))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                }

                Spacer(minLength: 8)

                IOSStatusBadge(text: healthText, systemImage: healthIcon, tint: healthTint)
                NavigationLink {
                    destination()
                } label: {
                    Image(systemName: "slider.horizontal.3")
                        .frame(width: 28, height: 28)
                }
                .buttonStyle(.plain)
            }

            if group.chains.isEmpty {
                IOSInlineEmptyState(text: "No member chains.", systemImage: "tray")
            } else {
                VStack(spacing: 6) {
                    ForEach(group.chains, id: \.self) { chain in
                        Button {
                            if isManual {
                                onSelect(chain)
                            }
                        } label: {
                            IOSPolicyMemberRow(
                                chain: chain,
                                result: result(for: chain),
                                isSelected: chain == selectedChain,
                                isManual: isManual
                            )
                        }
                        .buttonStyle(.plain)
                        .disabled(!isManual || chain == selectedChain)
                    }
                }
            }

            HStack {
                Text(group.selectionReason.isEmpty ? modeText : group.selectionReason.replacingOccurrences(of: "_", with: " "))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Spacer()
                Button(role: .destructive, action: onDelete) {
                    Image(systemName: "trash")
                }
                .buttonStyle(.borderless)
                .accessibilityLabel("Delete policy group")
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(.tertiarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }

    private var selectedChain: String {
        if !group.selectedChain.isEmpty {
            return group.selectedChain
        }
        if !group.selected.isEmpty {
            return group.selected
        }
        return group.chains.first ?? ""
    }

    private var selectedText: String {
        selectedChain.isEmpty ? "No chain selected" : "Selected \(selectedChain)"
    }

    private var modeText: String {
        let value = group.selectionMode.isEmpty ? group.type : group.selectionMode
        return value.isEmpty ? "static" : value.replacingOccurrences(of: "-", with: " ")
    }

    private var isManual: Bool {
        group.type.caseInsensitiveCompare("select") == .orderedSame ||
            group.selectionMode.caseInsensitiveCompare("manual") == .orderedSame
    }

    private var healthText: String {
        guard !group.results.isEmpty else {
            return isManual ? "Manual" : "Pending"
        }
        let healthy = group.results.filter(\.healthy).count
        return fallbackSelected ? "Fallback \(healthy)/\(group.results.count)" : "Healthy \(healthy)/\(group.results.count)"
    }

    private var healthIcon: String {
        if group.results.isEmpty {
            return isManual ? "hand.tap" : "clock"
        }
        return fallbackSelected ? "exclamationmark.triangle.fill" : "checkmark.circle.fill"
    }

    private var healthTint: Color {
        if group.results.isEmpty {
            return .secondary
        }
        return fallbackSelected ? .orange : .green
    }

    private var fallbackSelected: Bool {
        guard !group.results.isEmpty else {
            return false
        }
        return group.results.first(where: { $0.chainName == selectedChain })?.healthy != true
    }

    private func result(for chain: String) -> PolicyProbeResultPayload? {
        group.results.first { $0.chainName == chain }
    }
}

private struct IOSPolicyMemberRow: View {
    var chain: String
    var result: PolicyProbeResultPayload?
    var isSelected: Bool
    var isManual: Bool

    var body: some View {
        HStack(spacing: 10) {
            Image(systemName: icon)
                .foregroundStyle(tint)
                .frame(width: 22)
            VStack(alignment: .leading, spacing: 2) {
                Text(emptyDash(chain))
                    .font(.subheadline.weight(.medium))
                    .lineLimit(1)
                Text(detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
            Spacer(minLength: 8)
            if isManual {
                Image(systemName: "chevron.right")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.tertiary)
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .background(isSelected ? Color.accentColor.opacity(0.12) : Color.secondary.opacity(0.06), in: RoundedRectangle(cornerRadius: 7, style: .continuous))
    }

    private var icon: String {
        if isSelected {
            return "checkmark.circle.fill"
        }
        guard let result else {
            return "clock"
        }
        return result.healthy ? "checkmark.circle" : "exclamationmark.triangle"
    }

    private var tint: Color {
        if isSelected {
            return .accentColor
        }
        guard let result else {
            return .secondary
        }
        return result.healthy ? .green : .orange
    }

    private var detail: String {
        guard let result else {
            return isManual ? "Tap a member to select it when health arrives." : "Waiting for health probe."
        }
        if result.healthy {
            return result.latencyNs > 0 ? formatDurationNs(result.latencyNs) : "Healthy"
        }
        return result.error.isEmpty ? "Unhealthy" : result.error
    }
}

private struct IOSPolicyRouteRow: View {
    var route: PolicySelectorRouteSummary

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "arrow.triangle.branch")
                .foregroundStyle(tint)
                .frame(width: 24)
            VStack(alignment: .leading, spacing: 3) {
                Text(route.groupName.isEmpty ? "Route" : route.groupName)
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                Text([route.selectedChain, route.healthText].filter { !$0.isEmpty }.joined(separator: " / "))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
            Spacer(minLength: 8)
            Image(systemName: icon)
                .foregroundStyle(tint)
        }
        .padding(.vertical, 2)
    }

    private var icon: String {
        switch route.healthState {
        case .staticRoute:
            return "arrow.triangle.branch"
        case .pending:
            return "clock"
        case .healthy:
            return "checkmark.circle.fill"
        case .fallback:
            return "exclamationmark.triangle.fill"
        }
    }

    private var tint: Color {
        switch route.healthState {
        case .staticRoute, .pending:
            return .secondary
        case .healthy:
            return .green
        case .fallback:
            return .orange
        }
    }
}

private struct IOSPolicyGroupFormView: View {
    @Binding var group: PolicyGroupPayload
    var chainNames: [String]

    private let groupTypes = ["select", "url-test", "fallback", "load-balance", "smart"]

    var body: some View {
        Form {
            Section("Group") {
                TextField("Name", text: $group.name)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                Picker("Type", selection: $group.type) {
                    ForEach(groupTypes, id: \.self) { type in
                        Text(type.replacingOccurrences(of: "-", with: " ")).tag(type)
                    }
                }
                Toggle("Hidden", isOn: $group.hidden)
            }

            Section("Members") {
                TextField("Chains", text: chainsTextBinding)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                if !chainNames.isEmpty {
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: 8) {
                            ForEach(chainNames, id: \.self) { chain in
                                Button {
                                    toggleChain(chain)
                                } label: {
                                    Label(chain, systemImage: group.chains.contains(chain) ? "checkmark.circle.fill" : "circle")
                                }
                                .buttonStyle(.bordered)
                                .controlSize(.small)
                            }
                        }
                    }
                }
                if group.type == "select" {
                    Picker("Selected", selection: selectedBinding) {
                        ForEach(group.chains, id: \.self) { chain in
                            Text(chain).tag(chain)
                        }
                    }
                    .disabled(group.chains.isEmpty)
                }
            }

            Section("Health Test") {
                TextField("Test URL", text: $group.testURL)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                TextField("Interval", text: $group.interval)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                TextField("Timeout", text: $group.timeout)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
            }
        }
        .navigationTitle(group.name.isEmpty ? "Policy Group" : group.name)
        .navigationBarTitleDisplayMode(.inline)
    }

    private var chainsTextBinding: Binding<String> {
        Binding {
            group.chains.joined(separator: ", ")
        } set: { raw in
            group.chains = raw
                .split(separator: ",")
                .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
                .filter { !$0.isEmpty }
            if !group.chains.contains(group.selectedChain) {
                group.selectedChain = group.chains.first ?? ""
                group.selected = group.selectedChain
            }
        }
    }

    private var selectedBinding: Binding<String> {
        Binding {
            group.selectedChain.isEmpty ? group.selected : group.selectedChain
        } set: { next in
            group.selectedChain = next
            group.selected = next
        }
    }

    private func toggleChain(_ chain: String) {
        if group.chains.contains(chain) {
            group.chains.removeAll { $0 == chain }
        } else {
            group.chains.append(chain)
        }
        if !group.chains.contains(group.selectedChain) {
            group.selectedChain = group.chains.first ?? ""
            group.selected = group.selectedChain
        }
    }
}
