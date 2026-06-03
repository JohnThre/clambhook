import ClambhookShared
import SwiftUI

struct IOSHTTPCaptureView: View {
    @ObservedObject var model: AppleAppModel
    @State private var filter: CaptureFilterKind = .all
    @State private var searchText = ""

    var body: some View {
        List {
            Section("Status") {
                IOSCaptureReadinessView(entries: entries)
            }

            Section {
                Picker("Metadata Filter", selection: $filter) {
                    ForEach(CaptureFilterKind.allCases) { filter in
                        Text(title(for: filter)).tag(filter)
                    }
                }
                .pickerStyle(.segmented)
            }

            Section("HTTP Metadata") {
                if filteredEntries.isEmpty {
                    ContentUnavailableView(
                        "No matching HTTP metadata",
                        systemImage: "network",
                        description: Text("HTTP and HTTPS CONNECT metadata appears here when traffic exposes HTTP visibility.")
                    )
                } else {
                    ForEach(filteredEntries) { entry in
                        NavigationLink {
                            IOSHTTPCaptureDetailView(model: model, entry: entry)
                        } label: {
                            IOSHTTPCaptureRow(entry: entry)
                        }
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
                    Label("Export", systemImage: "square.and.arrow.up")
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

    private func title(for filter: CaptureFilterKind) -> String {
        switch filter {
        case .all: return "All"
        case .http: return "HTTP"
        case .https: return "HTTPS"
        case .metadataOnly: return "Metadata"
        }
    }
}

private struct IOSCaptureReadinessView: View {
    var entries: [CaptureEntryPayload]

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            IOSMetricsGrid(metrics: [
                IOSMetric(title: "Requests", value: "\(entries.count)", systemImage: "list.bullet.rectangle"),
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
    var entry: CaptureEntryPayload

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
                    Text(emptyDash(entry.host))
                        .font(.body.weight(.medium))
                        .lineLimit(1)
                    Spacer(minLength: 8)
                    Text(emptyDash(entry.state).capitalized)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Text([entry.path, entry.ruleName, entry.chainName].filter { !$0.isEmpty }.joined(separator: " / "))
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
    var entry: CaptureEntryPayload

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
        }
        .listStyle(.insetGrouped)
        .navigationTitle(emptyDash(entry.host))
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItemGroup(placement: .topBarTrailing) {
                ShareLink(
                    item: CaptureSupport.exportString(
                        traffic: model.dashboard.traffic,
                        entries: [entry]
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
