import ClambhookShared
import SwiftUI

struct IOSStatusView: View {
    @ObservedObject var model: AppleAppModel
    var onRecoveryAction: ((TunnelRecoveryAction) -> Void)? = nil

    var body: some View {
        List {
            Section {
                IOSStatusPanel(model: model, onRecoveryAction: onRecoveryAction)
            }

            Section("Profile") {
                IOSProfileControl(model: model)
            }

            Section("Now") {
                IOSMetricsGrid(metrics: overviewMetrics)
                    .listRowInsets(EdgeInsets(top: 10, leading: 16, bottom: 10, trailing: 16))
            }

            Section("Recent") {
                if model.dashboard.recentDecisions.isEmpty {
                    IOSInlineEmptyState(text: "No recent activity.", systemImage: "arrow.triangle.branch")
                } else {
                    ForEach(model.dashboard.recentDecisions.prefix(5)) { decision in
                        IOSDecisionRow(decision: decision)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .refreshable {
            await model.refreshNow()
        }
    }

    private var overviewMetrics: [IOSMetric] {
        let sample = model.dashboard.currentBandwidth
        return [
            IOSMetric(title: "Down", value: formatRate(sample.rxBps), systemImage: "arrow.down"),
            IOSMetric(title: "Up", value: formatRate(sample.txBps), systemImage: "arrow.up"),
            IOSMetric(title: "Active", value: "\(model.dashboard.traffic.summary.activeConnections)", systemImage: "bolt.horizontal.circle"),
            IOSMetric(title: "Rules", value: "\(model.dashboard.rules.rules.count)", systemImage: "slider.horizontal.3"),
        ]
    }
}

private struct IOSStatusPanel: View {
    @ObservedObject var model: AppleAppModel
    var onRecoveryAction: ((TunnelRecoveryAction) -> Void)?
    @AppStorage("org.jpfchang.clambhook.vpnDisclosureAccepted") private var vpnDisclosureAccepted = false
    @State private var showingVPNDisclosure = false

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(alignment: .center, spacing: 12) {
                ZStack {
                    Circle()
                        .fill(connectionTint.opacity(0.14))
                    Image(systemName: model.dashboard.status.running ? "network" : "network.slash")
                        .font(.title3)
                        .foregroundStyle(connectionTint)
                }
                .frame(width: 44, height: 44)

                VStack(alignment: .leading, spacing: 3) {
                    Text(model.dashboard.status.running ? "Connected" : "Disconnected")
                        .font(.headline)
                    Text(emptyDash(model.dashboard.activeProfile))
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }

                Spacer(minLength: 12)

                VStack(alignment: .trailing, spacing: 6) {
                    IOSStatusBadge(
                        text: model.dashboard.apiOnline ? "Tunnel ready" : "Tunnel unavailable",
                        systemImage: "network",
                        tint: model.dashboard.apiOnline ? .green : .red
                    )
                    IOSStatusBadge(
                        text: model.dashboard.status.running ? "Running" : "Stopped",
                        systemImage: model.dashboard.status.running ? "checkmark.circle.fill" : "pause.circle",
                        tint: connectionTint
                    )
                }
            }

            if let issue = model.dashboard.recoveryIssue {
                IOSRecoveryBanner(issue: issue) { action in
                    if let onRecoveryAction {
                        onRecoveryAction(action)
                    } else {
                        model.performRecoveryAction(action)
                    }
                }
            } else if !model.dashboard.errorText.isEmpty {
                Label(model.dashboard.errorText, systemImage: "exclamationmark.triangle.fill")
                    .font(.caption)
                    .foregroundStyle(.red)
                    .lineLimit(3)
            }

            ViewThatFits(in: .horizontal) {
                HStack(spacing: 10) {
                    actionButtons
                }
                VStack(spacing: 10) {
                    actionButtons
                }
            }
        }
        .padding(.vertical, 2)
        .sheet(isPresented: $showingVPNDisclosure) {
            IOSVPNDisclosureSheet {
                vpnDisclosureAccepted = true
                model.connectOrDisconnect()
            }
        }
    }

    private var actionButtons: some View {
        Group {
            Button {
                handleConnectAction()
            } label: {
                Label(
                    model.dashboard.status.running ? "Disconnect" : "Connect",
                    systemImage: model.dashboard.status.running ? "stop.fill" : "play.fill"
                )
                .frame(maxWidth: .infinity)
            }
            .buttonStyle(.borderedProminent)
            .disabled(!model.dashboard.apiOnline && !model.dashboard.status.running)

            Button {
                model.refresh()
            } label: {
                Label("Refresh", systemImage: "arrow.clockwise")
                    .frame(maxWidth: .infinity)
            }
            .buttonStyle(.bordered)
        }
        .controlSize(.large)
    }

    private var connectionTint: Color {
        model.dashboard.status.running ? .green : .secondary
    }

    private func handleConnectAction() {
        if model.dashboard.status.running || vpnDisclosureAccepted {
            model.connectOrDisconnect()
            return
        }
        showingVPNDisclosure = true
    }
}

private struct IOSVPNDisclosureSheet: View {
    var onAccept: () -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    Image(systemName: "network.badge.shield.half.filled")
                        .font(.system(size: 44))
                        .foregroundStyle(.tint)
                        .frame(maxWidth: .infinity, alignment: .leading)

                    Text("VPN Data Use")
                        .font(.title2.weight(.semibold))

                    Text(vpnDataUseDisclosure)
                        .font(.body)
                        .foregroundStyle(.primary)
                        .fixedSize(horizontal: false, vertical: true)

                    Link("Privacy Policy", destination: defaultPrivacyPolicyURL)
                        .font(.body.weight(.medium))
                }
                .padding(20)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .navigationTitle("Before You Connect")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Continue") {
                        dismiss()
                        onAccept()
                    }
                    .fontWeight(.semibold)
                }
            }
        }
    }
}

private struct IOSProfileControl: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        if model.dashboard.profiles.profiles.isEmpty {
            IOSInlineEmptyState(text: "No profiles are available.", systemImage: "person.crop.rectangle.stack")
        } else {
            HStack(spacing: 12) {
                Label {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(emptyDash(model.dashboard.activeProfile))
                            .font(.body.weight(.medium))
                            .lineLimit(1)
                        Text("\(model.dashboard.profiles.profiles.count) profiles")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                } icon: {
                    Image(systemName: "person.crop.rectangle.stack")
                        .foregroundStyle(.secondary)
                }

                Spacer(minLength: 8)

                Menu {
                    ForEach(model.dashboard.profiles.profiles, id: \.self) { profile in
                        Button {
                            model.selectProfile(profile)
                        } label: {
                            if profile == model.dashboard.activeProfile {
                                Label(profile, systemImage: "checkmark")
                            } else {
                                Text(profile)
                            }
                        }
                    }
                } label: {
                    Label("Change", systemImage: "arrow.up.arrow.down.circle")
                }
                .buttonStyle(.bordered)
            }
        }
    }
}

private struct IOSDecisionRow: View {
    var decision: RecentDecision

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            IOSActionChip(action: decision.action)
            VStack(alignment: .leading, spacing: 4) {
                Text(emptyDash(decision.target))
                    .font(.body.weight(.medium))
                    .lineLimit(1)
                Text([decision.ruleName, decision.connection.chainName, decision.connection.displayVisibility].filter { !$0.isEmpty }.joined(separator: " / "))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 2)
    }
}

private struct IOSRuleHitRow: View {
    var summary: RuleHitSummary

    var body: some View {
        HStack(spacing: 12) {
            IOSActionChip(action: summary.action)
            Text(summary.ruleName.isEmpty ? "Default route" : summary.ruleName)
                .font(.body.weight(.medium))
                .lineLimit(1)
            Spacer(minLength: 8)
            Text("\(summary.count)")
                .font(.subheadline.weight(.semibold))
                .monospacedDigit()
                .foregroundStyle(.secondary)
        }
    }
}
