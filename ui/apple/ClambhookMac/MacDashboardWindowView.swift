import AppKit
import ClambhookShared
import SwiftUI

enum SidebarSection: String, CaseIterable, Identifiable {
    case dashboard = "Dashboard"
    case profiles = "Profiles"
    case policyGroups = "Policy Groups"
    case rules = "Rules"
    case dns = "DNS"
    case activity = "Activity"
    case httpCapture = "HTTP Capture"
    case logs = "Logs"
    case settings = "Settings"
    case license = "License"

    var id: String { rawValue }

    var systemImage: String {
        switch self {
        case .dashboard:     "gauge.with.dots.needle.33percent"
        case .profiles:      "person.crop.circle"
        case .policyGroups:  "point.3.connected.trianglepath.dotted"
        case .rules:         "line.3.horizontal.decrease.circle"
        case .dns:           "network"
        case .activity:      "arrow.up.arrow.down"
        case .httpCapture:   "list.bullet.rectangle"
        case .logs:          "text.alignleft"
        case .settings:      "gear"
        case .license:       "lock.shield"
        }
    }
}

struct MacDashboardWindowView: View {
    @ObservedObject var model: AppleAppModel
    @State private var selectedSection: SidebarSection = .dashboard

    var body: some View {
        NavigationSplitView {
            List(SidebarSection.allCases, selection: $selectedSection) { section in
                Label(section.rawValue, systemImage: section.systemImage)
                    .tag(section)
            }
            .navigationTitle("clambhook")
            .listStyle(.sidebar)
        } detail: {
            switch selectedSection {
            case .dashboard:    MacDashboardSection(model: model)
            case .profiles:    MacProfilesSection(model: model)
            case .policyGroups: MacPolicyGroupsSection(model: model)
            case .rules:       MacRulesSection(model: model)
            case .dns:         MacDNSSection(model: model)
            case .activity:    MacActivitySection(model: model)
            case .httpCapture: MacHTTPCaptureSection(model: model)
            case .logs:        MacLogsSection(model: model)
            case .settings:    MacSettingsSection(model: model)
            case .license:     MacLicenseSectionInline(model: model)
            }
        }
        .frame(minWidth: 720, minHeight: 480)
        .onAppear {
            NSApp.setActivationPolicy(.regular)
            NSApp.activate(ignoringOtherApps: true)
        }
        .onDisappear {
            NSApp.setActivationPolicy(.accessory)
        }
    }
}
