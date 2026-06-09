import ClambhookShared
import SwiftUI

struct IOSHTTPCaptureView: View {
    @ObservedObject var model: AppleAppModel
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass
    @State private var filter: CaptureFilterKind = .all
    @State private var searchText = ""
    @State private var selectedEntryID: String?

    var body: some View {
        Group {
            if horizontalSizeClass == .regular {
                regularMetadataLayout
            } else {
                compactMetadataLayout
            }
        }
        .background(Color(.systemGroupedBackground))
        .searchable(text: $searchText, prompt: "Search host, path, method, rule")
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                ShareLink(
                    item: CaptureSupport.exportString(
                        traffic: model.dashboard.traffic,
                        entries: filteredEntries
                    ),
                    subject: Text("ClambHook HTTP metadata export"),
                    message: Text("Local metadata-only export.")
                ) {
                    Image(systemName: "square.and.arrow.up")
                }
            }
        }
        .refreshable {
            await model.refreshNow()
        }
    }

    private var compactMetadataLayout: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 12) {
            IOSConsoleSection("Status", detail: "\(entries.count) requests") {
                IOSCaptureReadinessView(entries: entries, groups: groupedEntries, pinnedIDs: pinnedIDs)
            }

                IOSConsoleSection("Filters", detail: title(for: filter)) {
                Picker("Metadata Filter", selection: $filter) {
                    ForEach(CaptureFilterKind.allCases) { filter in
                        Text(title(for: filter)).tag(filter)
                    }
                }
                .pickerStyle(.menu)
            }

            if groupedEntries.isEmpty {
                    IOSConsoleSection("HTTP Metadata") {
                    ContentUnavailableView(
                        "No matching HTTP metadata",
                        systemImage: "network",
                        description: Text("HTTP and HTTPS CONNECT metadata appears here when traffic exposes HTTP visibility.")
                    )
                }
            } else {
                ForEach(groupedEntries) { group in
                        IOSConsoleSection(emptyDash(group.host), detail: groupSubtitle(group)) {
                            VStack(spacing: 8) {
                                ForEach(group.entries) { entry in
                                    NavigationLink {
                                        IOSHTTPCaptureDetailView(model: model, entry: entry)
                                    } label: {
                                        IOSHTTPCaptureRow(
                                            entry: entry,
                                            pinned: isPinned(entry),
                                            onTogglePin: { togglePinned(entry) }
                                        )
                                    }
                                    .buttonStyle(.plain)
                                }
                            }
                        }
                }
            }
        }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
        }
    }

    private var regularMetadataLayout: some View {
        HStack(spacing: 0) {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 12) {
                    IOSConsoleSection("Status", detail: "\(entries.count) requests") {
                        IOSCaptureReadinessView(entries: entries, groups: groupedEntries, pinnedIDs: pinnedIDs)
                    }

                    IOSConsoleSection("Filters", detail: title(for: filter)) {
                        Picker("Metadata Filter", selection: $filter) {
                            ForEach(CaptureFilterKind.allCases) { filter in
                                Text(title(for: filter)).tag(filter)
                            }
                        }
                        .pickerStyle(.menu)
                    }

                    if groupedEntries.isEmpty {
                        IOSConsoleSection("HTTP Metadata") {
                            ContentUnavailableView(
                                "No matching HTTP metadata",
                                systemImage: "network",
                                description: Text("HTTP and HTTPS CONNECT metadata appears here when traffic exposes HTTP visibility.")
                            )
                        }
                    } else {
                        ForEach(groupedEntries) { group in
                            IOSConsoleSection(emptyDash(group.host), detail: groupSubtitle(group)) {
                                VStack(spacing: 8) {
                                    ForEach(group.entries) { entry in
                                        IOSHTTPCaptureRow(
                                            entry: entry,
                                            pinned: isPinned(entry),
                                            selected: selectedEntry?.id == entry.id,
                                            onTogglePin: { togglePinned(entry) }
                                        )
                                        .contentShape(Rectangle())
                                        .onTapGesture {
                                            selectedEntryID = entry.id
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 12)
            }
            .frame(minWidth: 340, idealWidth: 400, maxWidth: 460)

            Divider()

            NavigationStack {
                if let selectedEntry {
                    IOSHTTPCaptureDetailView(model: model, entry: selectedEntry)
                } else {
                    IOSInspectionPlaceholderView(
                        title: "Select Metadata",
                        message: "HTTP and HTTPS CONNECT metadata appears here.",
                        systemImage: "network"
                    )
                }
            }
        }
    }

    private var entries: [CaptureEntryPayload] {
        CaptureSupport.captureEntries(from: model.dashboard.traffic)
    }

    private var filteredEntries: [CaptureEntryPayload] {
        CaptureSupport.filteredEntries(entries, filter: filter, query: searchText, pinnedIDs: pinnedIDs)
    }

    private var groupedEntries: [CaptureGroupPayload] {
        CaptureSupport.groupEntriesByHost(filteredEntries, pinnedIDs: pinnedIDs)
    }

    private var pinnedIDs: Set<String> {
        model.pinnedConnectionIDs
    }

    private var selectedEntry: CaptureMetadataEntryPayload? {
        let entries = groupedEntries.flatMap(\.entries)
        if let selectedEntryID,
           let entry = entries.first(where: { $0.id == selectedEntryID }) {
            return entry
        }
        return entries.first
    }

    private func isPinned(_ entry: CaptureMetadataEntryPayload) -> Bool {
        pinnedIDs.contains(entry.pinID)
    }

    private func togglePinned(_ entry: CaptureMetadataEntryPayload) {
        var ids = pinnedIDs
        if ids.contains(entry.pinID) {
            ids.remove(entry.pinID)
        } else {
            ids.insert(entry.pinID)
        }
        model.settingsStore.settings.pinnedConnectionIDs = ids.sorted()
    }

    private func groupSubtitle(_ group: CaptureGroupPayload) -> String {
        let schemes = group.schemes.map { $0.uppercased() }.joined(separator: ", ")
        if schemes.isEmpty {
            return "\(group.count)"
        }
        return "\(group.count) / \(schemes)"
    }

    private func title(for filter: CaptureFilterKind) -> String {
        switch filter {
        case .all: return "All"
        case .active: return "Active"
        case .http: return "HTTP"
        case .https: return "HTTPS"
        case .pinned: return "Pinned"
        case .proxy: return "Proxy"
        case .direct: return "Direct"
        case .block: return "Block"
        }
    }
}

private struct IOSCaptureReadinessView: View {
    var entries: [CaptureEntryPayload]
    var groups: [CaptureGroupPayload]
    var pinnedIDs: Set<String>

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            IOSMetricsGrid(metrics: [
                IOSMetric(title: "Requests", value: "\(entries.count)", systemImage: "list.bullet.rectangle"),
                IOSMetric(title: "Hosts", value: "\(groups.count)", systemImage: "rectangle.stack"),
                IOSMetric(title: "Pinned", value: "\(entries.filter { pinnedIDs.contains($0.pinID) }.count)", systemImage: "pin"),
                IOSMetric(title: "HTTP", value: "\(entries.filter { $0.scheme.lowercased() == "http" }.count)", systemImage: "globe"),
                IOSMetric(title: "HTTPS", value: "\(entries.filter { $0.scheme.lowercased() == "https" }.count)", systemImage: "lock"),
            ])
            Text(CaptureSupport.captureNote)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }
}

private struct IOSHTTPCaptureRow: View {
    var entry: CaptureMetadataEntryPayload
    var pinned: Bool
    var selected = false
    var onTogglePin: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            VStack(spacing: 4) {
                Text(entry.method.isEmpty ? "--" : entry.method)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.white)
                    .padding(.horizontal, 7)
                    .padding(.vertical, 3)
                    .background(entry.scheme.lowercased() == "https" ? Color.blue : Color.green)
                    .clipShape(RoundedRectangle(cornerRadius: 6))
                Text(entry.scheme.uppercased())
                    .font(.caption2.weight(.medium))
                    .foregroundStyle(.secondary)
            }

            VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .firstTextBaseline) {
                    if pinned {
                        Image(systemName: "pin.fill")
                            .font(.caption)
                            .foregroundStyle(.yellow)
                    }
                    Text(emptyDash(entry.displayTarget))
                        .font(.body.weight(.medium))
                        .lineLimit(1)
                    Spacer(minLength: 8)
                    Text(emptyDash(entry.state).capitalized)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Text([entry.ruleName, entry.chainName, entry.ruleAction].filter { !$0.isEmpty }.joined(separator: " / "))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                Text("\(formatBytes(entry.rxTotal)) down / \(formatBytes(entry.txTotal)) up / \(entry.sslState.replacingOccurrences(of: "_", with: " "))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 8)
            Button(action: onTogglePin) {
                Image(systemName: pinned ? "pin.slash.fill" : "pin.fill")
                    .frame(width: 28, height: 28)
            }
            .buttonStyle(.bordered)
            .controlSize(.small)
            .accessibilityLabel(pinned ? "Unpin metadata row" : "Pin metadata row")
        }
        .padding(10)
        .background(selected ? Color.accentColor.opacity(0.14) : Color(.tertiarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 7, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 7, style: .continuous)
                .stroke(selected ? Color.accentColor.opacity(0.45) : Color.clear, lineWidth: 1)
        )
    }
}

private struct IOSHTTPCaptureDetailView: View {
    @ObservedObject var model: AppleAppModel
    var entry: CaptureMetadataEntryPayload

    var body: some View {
        List {
            Section("Connection") {
                LabeledContent("ID", value: emptyDash(entry.pinID))
                LabeledContent("Pinned", value: pinned ? "Yes" : "No")
                LabeledContent("Started", value: captureDateTimeLabel(entry.startedAtNs))
                LabeledContent("Updated", value: captureDateTimeLabel(entry.updatedAtNs))
            }

            Section("Request") {
                LabeledContent("Method", value: emptyDash(entry.method))
                LabeledContent("Scheme", value: emptyDash(entry.scheme))
                LabeledContent("Host", value: emptyDash(entry.host))
                LabeledContent("Port", value: emptyDash(entry.port))
                LabeledContent("Path", value: emptyDash(entry.path))
                LabeledContent("State", value: emptyDash(entry.state).capitalized)
                LabeledContent("Visibility", value: entry.sslState.replacingOccurrences(of: "_", with: " "))
            }

            Section("Route") {
                LabeledContent("Action", value: emptyDash(entry.ruleAction))
                LabeledContent("Rule", value: emptyDash(entry.ruleName))
                LabeledContent("Chain", value: emptyDash(entry.chainName))
            }

            Section("Data") {
                LabeledContent("Down", value: formatBytes(entry.rxTotal))
                LabeledContent("Up", value: formatBytes(entry.txTotal))
                LabeledContent("Duration", value: formatDurationNs(entry.durationNs))
            }

            Section("Timeline") {
                if entry.timeline.isEmpty {
                    Text("No timeline events recorded.")
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(entry.timeline) { event in
                        IOSHTTPCaptureTimelineRow(event: event)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle(emptyDash(entry.host))
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItemGroup(placement: .topBarTrailing) {
                Button {
                    togglePinned()
                } label: {
                    Image(systemName: pinned ? "pin.slash.fill" : "pin.fill")
                }
                ShareLink(
                    item: CaptureSupport.exportString(
                        traffic: model.dashboard.traffic,
                        entries: CaptureSupport.captureEntries(from: model.dashboard.traffic).filter { $0.id == entry.id }
                    ),
                    subject: Text("ClambHook HTTP metadata"),
                    message: Text("Local metadata-only export.")
                ) {
                    Image(systemName: "square.and.arrow.up")
                }
            }
        }
    }

    private var pinned: Bool {
        model.pinnedConnectionIDs.contains(entry.pinID)
    }

    private func togglePinned() {
        var ids = model.pinnedConnectionIDs
        if ids.contains(entry.pinID) {
            ids.remove(entry.pinID)
        } else {
            ids.insert(entry.pinID)
        }
        model.settingsStore.settings.pinnedConnectionIDs = ids.sorted()
    }
}

private struct IOSHTTPCaptureTimelineRow: View {
    var event: TrafficTimelinePayload

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(emptyDash(event.title))
                    .font(.body.weight(.medium))
                Spacer()
                Text(timeLabel)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if !event.detail.isEmpty {
                Text(event.detail)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
        }
        .padding(.vertical, 2)
    }

    private var timeLabel: String {
        guard event.tsNs > 0 else {
            return "--"
        }
        return Date(timeIntervalSince1970: Double(event.tsNs) / 1_000_000_000)
            .formatted(date: .omitted, time: .standard)
    }
}

private extension CaptureMetadataEntryPayload {
    var displayTarget: String {
        var value = host
        if !port.isEmpty {
            value += ":\(port)"
        }
        if !path.isEmpty {
            value += path
        }
        return value
    }
}

private func captureDateTimeLabel(_ tsNs: Int64) -> String {
    guard tsNs > 0 else {
        return "--"
    }
    return Date(timeIntervalSince1970: Double(tsNs) / 1_000_000_000)
        .formatted(date: .abbreviated, time: .standard)
}
