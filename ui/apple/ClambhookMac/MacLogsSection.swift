import AppKit
import ClambhookShared
import SwiftUI

// MARK: - Logs

struct MacLogsSection: View {
    @ObservedObject var model: AppleAppModel
    @State private var logSearch = ""

    private var filteredLogs: [(offset: Int, element: String)] {
        let query = logSearch.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let all = Array(model.dashboard.logs.enumerated())
        guard !query.isEmpty else { return all }
        return all.filter { $0.element.lowercased().contains(query) }
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                TextField("Filter logs…", text: $logSearch)
                    .textFieldStyle(.roundedBorder)
                    .font(.caption)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            Divider()
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 2) {
                        if filteredLogs.isEmpty {
                            Text(logSearch.isEmpty ? "No logs yet" : "No matches")
                                .foregroundStyle(.secondary)
                                .padding(20)
                        } else {
                            ForEach(filteredLogs, id: \.offset) { item in
                                Text(item.element)
                                    .font(.system(.caption, design: .monospaced))
                                    .foregroundStyle(logLineColor(item.element))
                                    .textSelection(.enabled)
                                    .id(item.offset)
                            }
                        }
                    }
                    .padding(12)
                }
                .onChange(of: model.dashboard.logs.count) {
                    if let last = filteredLogs.last {
                        proxy.scrollTo(last.offset, anchor: .bottom)
                    }
                }
            }
        }
    }

    private func logLineColor(_ line: String) -> Color {
        let lower = line.lowercased()
        if lower.contains("error") || lower.contains("err]") || lower.contains("[err") {
            return .red
        }
        if lower.contains("warn") {
            return .orange
        }
        return .secondary
    }
}
