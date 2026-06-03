import ClambhookShared
import SwiftUI

struct IOSHTTPCaptureView: View {
    @ObservedObject var model: AppleAppModel
    @State private var filter: CaptureFilterKind = .all
    @State private var searchText = ""

    var body: some View {
        List {
            Section("Status") {
                IOSCaptureReadinessView(entries: entries, groups: groupedEntries)
            }

            Section {
                Picker("Metadata Filter", selection: $filter) {
                    ForEach(CaptureFilterKind.allCases) { filter in
                        Text(title(for: filter)).tag(filter)
                    }
                }
                .pickerStyle(.segmented)
            }

            if groupedEntries.isEmpty {
                Section("HTTP Metadata") {
                    ContentUnavailableView(
                        "No matching HTTP metadata",
                        systemImage: "network",
                        description: Text("HTTP and HTTPS CONNECT metadata appears here when traffic exposes HTTP visibility.")
                    )
                }
            } else {
                ForEach(groupedEntries) { group in
                    Section {
                        ForEach(group.entries) { entry in
                            NavigationLink {
                                IOSHTTPCaptureDetailView(model: model, entry: entry)
                            } label: {
                                IOSHTTPCaptureRow(entry: entry)
                            }
                        }
                    } header: {
                        IOSHTTPCaptureGroupHeader(group: group)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
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

    private var entries: [CaptureEntryPayload] {
        CaptureSupport.captureEntries(from: model.dashboard.traffic)
    }

    private var filteredEntries: [CaptureEntryPayload] {
        CaptureSupport.filteredEntries(entries, filter: filter, query: searchText)
    }

    private var groupedEntries: [CaptureGroupPayload] {
        CaptureSupport.groupEntriesByHost(filteredEntries)
    }

    private func title(for filter: CaptureFilterKind) -> String {
        switch filter {
        case .all: return "All"
        case .http: return "HTTP"
        case .https: return "HTTPS"
        }
    }
}

private struct IOSCaptureReadinessView: View {
    var entries: [CaptureEntryPayload]
    var groups: [CaptureGroupPayload]

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            IOSMetricsGrid(metrics: [
                IOSMetric(title: "Requests", value: "\(entries.count)", systemImage: "list.bullet.rectangle"),
                IOSMetric(title: "Hosts", value: "\(groups.count)", systemImage: "rectangle.stack"),
                IOSMetric(title: "HTTP", value: "\(entries.filter { $0.scheme.lowercased() == "http" }.count)", systemImage: "globe"),
                IOSMetric(title: "HTTPS", value: "\(entries.filter { $0.scheme.lowercased() == "https" }.count)", systemImage: "lock"),
            ])
            Text(CaptureSupport.captureNote)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }
}

private struct IOSHTTPCaptureGroupHeader: View {
    var group: CaptureGroupPayload

    var body: some View {
        HStack {
            Text(emptyDash(group.host))
            Spacer()
            Text(groupSubtitle)
        }
    }

    private var groupSubtitle: String {
        let schemes = group.schemes.map { $0.uppercased() }.joined(separator: ", ")
        if schemes.isEmpty {
            return "\(group.count)"
        }
        return "\(group.count) / \(schemes)"
    }
}

private struct IOSHTTPCaptureRow: View {
    var entry: CaptureMetadataEntryPayload

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
        }
        .padding(.vertical, 2)
    }
}

private struct IOSHTTPCaptureDetailView: View {
    @ObservedObject var model: AppleAppModel
    var entry: CaptureMetadataEntryPayload

    var body: some View {
        List {
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
