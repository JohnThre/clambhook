import AppKit
import ClambhookShared
import SwiftUI

// MARK: - Sidebar item

enum SidebarItem: Hashable {
    case dashboard
    case profile(String)
    case policyGroups
    case rules
    case dns
    case activity
    case map
    case httpCapture
    case logs
    case settings
    case license
}

// MARK: - Window

struct MacDashboardWindowView: View {
    @ObservedObject var model: AppleAppModel
    @State private var selection: SidebarItem? = .dashboard

    var body: some View {
        NavigationSplitView {
            sidebarContent
                .navigationTitle("clambhook")
                .listStyle(.sidebar)
                .safeAreaInset(edge: .bottom) {
                    sidebarStatusFooter
                }
        } detail: {
            detailContent
        }
        .frame(minWidth: 720, minHeight: 480)
        .onChange(of: selection) { _, newValue in
            if case .profile(let name) = newValue {
                model.selectProfile(name)
            }
        }
        .onAppear {
            NSApp.setActivationPolicy(.regular)
            NSApp.activate(ignoringOtherApps: true)
        }
        .onDisappear {
            NSApp.setActivationPolicy(.accessory)
        }
    }

    // MARK: - Sidebar

    @ViewBuilder
    private var sidebarContent: some View {
        List(selection: $selection) {
            Section("OVERVIEW") {
                Label("Dashboard", systemImage: "gauge.with.dots.needle.33percent")
                    .tag(SidebarItem.dashboard)
            }

            Section("PROXY") {
                if model.dashboard.profiles.profiles.isEmpty {
                    Label("No profiles", systemImage: "person.crop.circle")
                        .foregroundStyle(.secondary)
                        .font(.subheadline)
                        .allowsHitTesting(false)
                } else {
                    ForEach(model.dashboard.profiles.profiles, id: \.self) { profile in
                        HStack {
                            Label(profile, systemImage: "person.crop.circle")
                            Spacer()
                            if profile == model.dashboard.activeProfile {
                                Image(systemName: "checkmark.circle.fill")
                                    .foregroundStyle(.green)
                                    .font(.caption)
                            }
                        }
                        .tag(SidebarItem.profile(profile))
                    }
                }
                Label("Policy Groups", systemImage: "point.3.connected.trianglepath.dotted")
                    .tag(SidebarItem.policyGroups)
                Label("Rules", systemImage: "line.3.horizontal.decrease.circle")
                    .tag(SidebarItem.rules)
            }

            Section("NETWORK") {
                Label("DNS", systemImage: "network")
                    .tag(SidebarItem.dns)
                Label("Activity", systemImage: "arrow.up.arrow.down")
                    .tag(SidebarItem.activity)
                Label("Map", systemImage: "globe.americas.fill")
                    .tag(SidebarItem.map)
                Label("HTTP Capture", systemImage: "list.bullet.rectangle")
                    .tag(SidebarItem.httpCapture)
            }

            Section("SYSTEM") {
                Label("Logs", systemImage: "text.alignleft")
                    .tag(SidebarItem.logs)
                Label("Settings", systemImage: "gear")
                    .tag(SidebarItem.settings)
                Label("License", systemImage: "lock.shield")
                    .tag(SidebarItem.license)
            }
        }
    }

    // MARK: - Sidebar footer

    private var sidebarStatusFooter: some View {
        HStack(spacing: 6) {
            Circle()
                .fill(footerDotColor)
                .frame(width: 7, height: 7)
            Text(footerStatusText)
                .font(.caption.weight(.medium))
                .foregroundStyle(.secondary)
                .lineLimit(1)
            Spacer(minLength: 4)
            if model.dashboard.status.running {
                let bw = model.dashboard.currentBandwidth
                Text("↓ \(formatRate(bw.rxBps))  ↑ \(formatRate(bw.txBps))")
                    .font(.caption2.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background(.bar)
    }

    private var footerDotColor: Color {
        if model.dashboard.status.running { return .green }
        switch model.daemonSupervisor.state {
        case .starting, .stopping: return .orange
        case .failed: return .red
        default: return .secondary
        }
    }

    private var footerStatusText: String {
        if model.dashboard.status.running { return "Connected" }
        switch model.daemonSupervisor.state {
        case .starting: return "Starting…"
        case .stopping: return "Stopping…"
        case .failed: return "Failed"
        case .running: return "Daemon running"
        default: return "Disconnected"
        }
    }

    // MARK: - Detail

    @ViewBuilder
    private var detailContent: some View {
        switch selection ?? .dashboard {
        case .dashboard:
            MacDashboardSection(model: model, onNavigate: { item in selection = item })
        case .profile:
            MacProfilesSection(model: model)
        case .policyGroups:
            MacPolicyGroupsSection(model: model)
        case .rules:
            MacRulesSection(model: model)
        case .dns:
            MacDNSSection(model: model)
        case .activity:
            MacActivitySection(model: model)
        case .map:
            MacConnectionMapSection(model: model)
        case .httpCapture:
            MacHTTPCaptureSection(model: model)
        case .logs:
            MacLogsSection(model: model)
        case .settings:
            MacSettingsSection(model: model)
        case .license:
            MacLicenseSectionInline(model: model)
        }
    }
}
