import ClambhookShared
import SwiftUI

struct IOSHTTPCaptureView: View {
    @ObservedObject var model: AppleAppModel
    @State private var captureMode: IOSCaptureMode = .metadata
    @State private var filter: CaptureFilterKind = .all
    @State private var searchText = ""
    @State private var caPEM = ""
    @State private var harExport = ""
    @State private var captureMessage = ""

    var body: some View {
        List {
            Section("Status") {
                IOSCaptureReadinessView(
                    entries: entries,
                    developerStatus: model.developerStatus,
                    developerCount: model.developerEntries.count
                )
            }

            Section {
                Picker("Capture Mode", selection: $captureMode) {
                    ForEach(IOSCaptureMode.allCases) { mode in
                        Text(mode.title).tag(mode)
                    }
                }
                .pickerStyle(.segmented)
            }

            if !captureMessage.isEmpty {
                Section {
                    Text(captureMessage)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }

            if captureMode == .metadata {
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
            } else {
                Section("HTTPS Body Capture") {
                    if model.developerEntries.isEmpty {
                        ContentUnavailableView(
                            model.developerStatus.enabled ? "No body captures" : "Body capture disabled",
                            systemImage: "lock.doc",
                            description: Text(developerCaptureDisclosure)
                        )
                    } else {
                        ForEach(filteredDeveloperEntries) { entry in
                            NavigationLink {
                                IOSDeveloperCaptureDetailView(entry: entry)
                            } label: {
                                IOSDeveloperCaptureRow(entry: entry)
                            }
                        }
                    }
                }

                Section("Exports") {
                    ShareLink(
                        item: developerEntriesExportString,
                        subject: Text("ClambHook HTTPS body capture entries"),
                        message: Text("Local opt-in capture export.")
                    ) {
                        Label("Export JSON", systemImage: "square.and.arrow.up")
                    }

                    Button {
                        loadHAR()
                    } label: {
                        Label("Prepare HAR Export", systemImage: "doc.text")
                    }

                    if !harExport.isEmpty {
                        ShareLink(
                            item: harExport,
                            subject: Text("ClambHook HAR export"),
                            message: Text("Local opt-in HTTPS body capture HAR.")
                        ) {
                            Label("Share HAR", systemImage: "square.and.arrow.up")
                        }
                    }

                    if !caPEM.isEmpty {
                        ShareLink(
                            item: caPEM,
                            subject: Text("ClambHook capture CA"),
                            message: Text("Install only on devices you control and trust.")
                        ) {
                            Label("Share CA Certificate", systemImage: "certificate")
                        }
                    } else {
                        Button {
                            loadCA()
                        } label: {
                            Label("Load CA Certificate", systemImage: "certificate")
                        }
                        .disabled(!model.developerStatus.mitmEnabled)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .searchable(text: $searchText, prompt: "Search host, path, method, rule")
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                HStack {
                    Button {
                        model.clearDeveloperEntries()
                    } label: {
                        Image(systemName: "trash")
                    }
                    .disabled(captureMode == .metadata || model.developerEntries.isEmpty)

                    ShareLink(
                        item: captureMode == .metadata
                            ? CaptureSupport.exportString(traffic: model.dashboard.traffic, entries: filteredEntries)
                            : developerEntriesExportString,
                        subject: Text(captureMode == .metadata ? "ClambHook HTTP metadata export" : "ClambHook HTTPS body capture export"),
                        message: Text(captureMode == .metadata ? "Local metadata-only export." : "Local opt-in capture export.")
                    ) {
                        Image(systemName: "square.and.arrow.up")
                    }
                }
            }
        }
        .refreshable {
            await model.refreshNow()
        }
        .task {
            await model.refreshDeveloperCaptureNow()
        }
    }

    private var entries: [CaptureEntryPayload] {
        CaptureSupport.captureEntries(from: model.dashboard.traffic)
    }

    private var filteredEntries: [CaptureEntryPayload] {
        CaptureSupport.filteredEntries(entries, filter: filter, query: searchText)
    }

    private var filteredDeveloperEntries: [DeveloperEntryPayload] {
        let query = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !query.isEmpty else {
            return model.developerEntries
        }
        return model.developerEntries.filter { entry in
            [
                entry.method,
                entry.url,
                entry.host,
                entry.profile,
                entry.chainName,
                entry.error,
            ].contains { $0.lowercased().contains(query) }
        }
    }

    private var developerEntriesExportString: String {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        let payload = DeveloperEntriesPayload(entries: filteredDeveloperEntries)
        guard let data = try? encoder.encode(payload),
              let text = String(data: data, encoding: .utf8) else {
            return #"{"entries":[]}"#
        }
        return text
    }

    private func title(for filter: CaptureFilterKind) -> String {
        switch filter {
        case .all: return "All"
        case .http: return "HTTP"
        case .https: return "HTTPS"
        case .metadataOnly: return "Metadata"
        }
    }

    private func loadCA() {
        Task {
            do {
                caPEM = try await model.developerCAPEM()
                captureMessage = "CA certificate loaded for sharing."
            } catch {
                captureMessage = error.localizedDescription
            }
        }
    }

    private func loadHAR() {
        Task {
            do {
                harExport = try await model.developerHAR()
                captureMessage = "HAR export is ready to share."
            } catch {
                captureMessage = error.localizedDescription
            }
        }
    }
}

private enum IOSCaptureMode: String, CaseIterable, Identifiable {
    case metadata
    case body

    var id: Self { self }

    var title: String {
        switch self {
        case .metadata:
            return "Metadata"
        case .body:
            return "Body"
        }
    }
}

private struct IOSCaptureReadinessView: View {
    var entries: [CaptureEntryPayload]
    var developerStatus: DeveloperStatusPayload
    var developerCount: Int

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            IOSMetricsGrid(metrics: [
                IOSMetric(title: "Requests", value: "\(entries.count)", systemImage: "list.bullet.rectangle"),
                IOSMetric(title: "HTTP", value: "\(entries.filter { $0.scheme.lowercased() == "http" }.count)", systemImage: "globe"),
                IOSMetric(title: "HTTPS", value: "\(entries.filter { $0.scheme.lowercased() == "https" }.count)", systemImage: "lock"),
                IOSMetric(title: "Bodies", value: "\(developerCount)", systemImage: developerStatus.mitmEnabled ? "lock.open" : "lock"),
            ])
            Text(CaptureSupport.captureNote)
                .font(.caption)
                .foregroundStyle(.secondary)
            Text(developerStatus.enabled ? developerCaptureDisclosure : "HTTPS body capture is disabled unless explicitly enabled in the developer capture config.")
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

private struct IOSDeveloperCaptureRow: View {
    var entry: DeveloperEntryPayload

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            VStack(spacing: 4) {
                Text(entry.method.isEmpty ? "--" : entry.method)
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.white)
                    .padding(.horizontal, 7)
                    .padding(.vertical, 3)
                    .background(entry.status >= 400 ? Color.red : Color.blue)
                    .clipShape(RoundedRectangle(cornerRadius: 6))
                Text(entry.status > 0 ? "\(entry.status)" : "open")
                    .font(.caption2.weight(.medium))
                    .foregroundStyle(.secondary)
            }

            VStack(alignment: .leading, spacing: 4) {
                HStack(alignment: .firstTextBaseline) {
                    Text(emptyDash(entry.host))
                        .font(.body.weight(.medium))
                        .lineLimit(1)
                    Spacer(minLength: 8)
                    Text(emptyDash(entry.chainName))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Text(emptyDash(entry.url))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                Text("\(formatBytes(entry.request.body.previewBytes)) request preview / \(formatBytes(entry.response.body.previewBytes)) response preview")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, 2)
    }
}

private struct IOSDeveloperCaptureDetailView: View {
    var entry: DeveloperEntryPayload

    var body: some View {
        List {
            Section("Request") {
                LabeledContent("Method", value: emptyDash(entry.method))
                LabeledContent("URL", value: emptyDash(entry.url))
                LabeledContent("Host", value: emptyDash(entry.host))
                LabeledContent("Profile", value: emptyDash(entry.profile))
                LabeledContent("Chain", value: emptyDash(entry.chainName))
                LabeledContent("Started", value: emptyDash(entry.startedAt))
            }

            if entry.status > 0 || !entry.finishedAt.isEmpty || !entry.error.isEmpty {
                Section("Response") {
                    if entry.status > 0 {
                        LabeledContent("Status", value: "\(entry.status)")
                    }
                    LabeledContent("Finished", value: emptyDash(entry.finishedAt))
                    if !entry.error.isEmpty {
                        LabeledContent("Error", value: entry.error)
                    }
                }
            }

            IOSDeveloperHeadersSection(title: "Request Headers", headers: entry.request.headers)
            IOSDeveloperBodySection(title: "Request Body", payload: entry.request.body)
            IOSDeveloperHeadersSection(title: "Response Headers", headers: entry.response.headers)
            IOSDeveloperBodySection(title: "Response Body", payload: entry.response.body)
        }
        .listStyle(.insetGrouped)
        .navigationTitle(emptyDash(entry.host))
        .navigationBarTitleDisplayMode(.inline)
    }
}

private struct IOSDeveloperHeadersSection: View {
    var title: String
    var headers: [DeveloperHeaderPayload]

    var body: some View {
        Section(title) {
            if headers.isEmpty {
                Text("No headers captured.")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(Array(headers.enumerated()), id: \.offset) { _, header in
                    LabeledContent(header.name, value: header.redacted ? "<redacted>" : header.value)
                }
            }
        }
    }
}

private struct IOSDeveloperBodySection: View {
    var title: String
    var payload: DeveloperBodyPayload

    var body: some View {
        Section(title) {
            LabeledContent("Size", value: formatBytes(payload.size))
            LabeledContent("Preview", value: formatBytes(payload.previewBytes))
            if payload.truncated {
                LabeledContent("Truncated After", value: formatBytes(payload.truncatedAfter))
            }
            if payload.preview.isEmpty {
                Text("No body preview captured.")
                    .foregroundStyle(.secondary)
            } else {
                Text(payload.preview)
                    .font(.system(.caption, design: .monospaced))
                    .textSelection(.enabled)
            }
        }
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
