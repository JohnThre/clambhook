import AppKit
import ClambhookShared
import SwiftUI

// MARK: - Policy Groups

struct MacPolicyGroupsSection: View {
    @ObservedObject var model: AppleAppModel
    @State private var testingGroup: String = ""

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                HStack {
                    Text("Policy Groups")
                        .font(.headline)
                    Spacer()
                    Button {
                        testingGroup = ""
                        Task { await model.dashboard.testPolicyGroup() }
                    } label: {
                        Label("Test All", systemImage: "speedometer")
                    }
                    .buttonStyle(.borderless)
                    .font(.caption)
                }
                CompactPolicySelectorView(
                    summary: model.dashboard.policySelectorSummary,
                    groups: model.dashboard.policyGroups.groups,
                    onSelect: { group, chain in
                        model.selectPolicyGroup(group: group, chain: chain)
                    },
                    onTest: { group in
                        testingGroup = group
                        Task {
                            await model.dashboard.testPolicyGroup(group: group)
                            testingGroup = ""
                        }
                    }
                )
            }
            .padding(20)
        }
    }
}
