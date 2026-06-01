import SwiftUI

struct IOSOperationsLogsView: View {
    @ObservedObject var model: AppleAppModel
    @State private var searchText = ""
    @State private var filter: IOSLogFilter = .all

    var body: some View {
        List {
            Section("Filter") {
                Picker("Log Filter", selection: $filter) {
                    ForEach(IOSLogFilter.allCases) { filter in
                        Text(filter.title).tag(filter)
                    }
                }
                .pickerStyle(.segmented)
            }

            Section("Recent Logs") {
                if filteredLogs.isEmpty {
                    ContentUnavailableView(
                        "No matching logs",
                        systemImage: "doc.text.magnifyingglass",
                        description: Text("Daemon and tunnel log events appear here.")
                    )
                } else {
                    ForEach(Array(filteredLogs.enumerated()), id: \.offset) { _, line in
                        IOSLogLineRow(line: line)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .searchable(text: $searchText, prompt: "Search logs")
        .refreshable {
            await model.refreshNow()
        }
    }

    private var filteredLogs: [String] {
        let query = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return model.dashboard.logs.filter { line in
            filter.matches(line) && (query.isEmpty || line.lowercased().contains(query))
        }
    }
}

private enum IOSLogFilter: String, CaseIterable, Identifiable {
    case all
    case errors
    case warnings

    var id: Self { self }

    var title: String {
        switch self {
        case .all: return "All"
        case .errors: return "Errors"
        case .warnings: return "Warn"
        }
    }

    func matches(_ line: String) -> Bool {
        let lower = line.lowercased()
        switch self {
        case .all:
            return true
        case .errors:
            return lower.contains("error") || lower.contains("failed")
        case .warnings:
            return lower.contains("warn")
        }
    }
}

private struct IOSLogLineRow: View {
    var line: String

    var body: some View {
        Text(line)
            .font(.system(.caption, design: .monospaced))
            .foregroundStyle(tint)
            .textSelection(.enabled)
            .lineLimit(5)
    }

    private var tint: Color {
        let lower = line.lowercased()
        if lower.contains("error") || lower.contains("failed") {
            return .red
        }
        if lower.contains("warn") {
            return .orange
        }
        return .secondary
    }
}
