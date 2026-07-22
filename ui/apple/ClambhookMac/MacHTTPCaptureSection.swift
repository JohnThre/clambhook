import AppKit
import ClambhookShared
import SwiftUI

// MARK: - HTTP Capture

struct MacHTTPCaptureSection: View {
    @ObservedObject var model: AppleAppModel
    @State private var captureSearch = ""
    @State private var selectedEntryID = ""
    @State private var localPath = ""
    @State private var remoteURL = ""
    @State private var harExport = ""
    @State private var showingHARExportWarning = false
    @State private var editingBreakpoint: DeveloperPendingBreakpointPayload?
    @State private var breakpointRequestBody = ""
    @State private var breakpointResponseBody = ""
    @State private var breakpointStatus = ""
    @State private var selectedMessageSide = "request"
    @State private var selectedMessageTab = "headers"
    @State private var composeEntry: DeveloperEntryPayload?

    var body: some View {
        VStack(spacing: 0) {
            toolbar
                .padding(12)
            if model.developerStatus.mitmEnabled {
                Label("HTTPS capture is decrypting traffic routed through the daemon HTTP proxy.", systemImage: "exclamationmark.triangle")
                    .font(.caption)
                    .foregroundStyle(.orange)
                    .padding(.horizontal, 12)
                    .padding(.bottom, 8)
            }
            Divider()
            pendingBreakpoints
            HSplitView {
                requestList
                    .frame(minWidth: 280, idealWidth: 360)
                entryDetail
                    .frame(minWidth: 420)
            }
        }
        .task {
            await model.refreshDeveloperCaptureNow()
        }
        .sheet(item: $editingBreakpoint) { breakpoint in
            breakpointEditor(breakpoint)
                .frame(minWidth: 560, minHeight: 460)
        }
        .sheet(item: $composeEntry) { entry in
            MacComposeRequestSheet(entry: entry) { request in
                model.sendComposedDeveloperRequest(request)
            }
            .frame(minWidth: 580, minHeight: 520)
        }
        .confirmationDialog(
            "Export HAR?",
            isPresented: $showingHARExportWarning,
            titleVisibility: .visible
        ) {
            Button("Load HAR Export") {
                Task {
                    harExport = (try? await model.developerHAR()) ?? ""
                }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text(developerHARExportDisclosure)
        }
    }

    private var toolbar: some View {
        HStack(spacing: 10) {
            Label("\(model.developerEntries.count) requests", systemImage: "list.bullet.rectangle")
                .foregroundStyle(model.developerStatus.enabled ? Color.blue : Color.secondary)
            if model.developerStatus.mitmEnabled {
                Label("HTTPS capture on", systemImage: "lock.open")
                    .foregroundStyle(.orange)
            }
            if model.developerStatus.noCacheEnabled {
                Label("No-cache", systemImage: "arrow.clockwise.circle")
                    .foregroundStyle(.purple)
            }
            TextField("Search requests", text: $captureSearch)
                .textFieldStyle(.roundedBorder)
                .frame(maxWidth: 260)
            Spacer()
            Button {
                model.refreshDeveloperCapture()
            } label: {
                Image(systemName: "arrow.clockwise")
            }
            .help("Refresh")
            Button {
                model.clearDeveloperEntries()
            } label: {
                Image(systemName: "trash")
            }
            .help("Clear")
            Button {
                showingHARExportWarning = true
            } label: {
                Image(systemName: "square.and.arrow.down")
            }
            .help("Load HAR export")
            if !harExport.isEmpty {
                ShareLink(item: harExport, subject: Text("ClambHook HAR export")) {
                    Image(systemName: "square.and.arrow.up")
                }
                .help("Share HAR")
            }
        }
    }

    private var requestList: some View {
        List(filteredEntries, selection: $selectedEntryID) { entry in
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(entry.method.isEmpty ? "--" : entry.method)
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(entry.scheme.lowercased() == "https" ? .blue : .green)
                        .frame(width: 54, alignment: .leading)
                    Text(entry.status == 0 ? "--" : "\(entry.status)")
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(statusColor(entry.status))
                    Spacer()
                }
                Text(displayURL(entry))
                    .font(.caption)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text([entry.chainName, bodySummary(entry)].filter { !$0.isEmpty }.joined(separator: " / "))
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            .padding(.vertical, 4)
            .tag(entry.id)
        }
        .onChange(of: filteredEntries) {
            if selectedEntryID.isEmpty || !filteredEntries.contains(where: { $0.id == selectedEntryID }) {
                selectedEntryID = filteredEntries.first?.id ?? ""
            }
        }
    }

    private var entryDetail: some View {
        ScrollView {
            if let entry = selectedEntry {
                VStack(alignment: .leading, spacing: 16) {
                    HStack {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(displayURL(entry))
                                .font(.headline)
                                .textSelection(.enabled)
                            Text([entry.method, entry.scheme.uppercased(), entry.host, entry.error].filter { !$0.isEmpty }.joined(separator: " / "))
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button {
                            model.repeatDeveloperEntry(entry)
                        } label: {
                            Label("Repeat", systemImage: "arrow.triangle.2.circlepath")
                        }
                        Button {
                            composeEntry = entry
                        } label: {
                            Label("Edit & Send", systemImage: "square.and.pencil")
                        }
                    }
                    ruleControls(entry)
                    Divider()
                    Picker("Message", selection: $selectedMessageSide) {
                        Text("Request").tag("request")
                        Text("Response").tag("response")
                    }
                    .pickerStyle(.segmented)
                    Picker("Detail", selection: $selectedMessageTab) {
                        Text("Headers").tag("headers")
                        Text("Body").tag("body")
                        Text("JSON").tag("json")
                        Text("Cookies").tag("cookies")
                    }
                    .pickerStyle(.segmented)
                    messageSection(
                        title: selectedMessageSide == "request" ? "Request" : "Response",
                        message: selectedMessageSide == "request" ? entry.request : entry.response,
                        tab: selectedMessageTab
                    )
                }
                .padding(18)
            } else {
                ContentUnavailableView("No Request", systemImage: "list.bullet.rectangle")
                    .padding(40)
            }
        }
    }

    private var pendingBreakpoints: some View {
        Group {
            if !model.developerPendingBreakpoints.isEmpty {
                VStack(alignment: .leading, spacing: 8) {
                    ForEach(model.developerPendingBreakpoints) { breakpoint in
                        HStack {
                            Label("\(breakpoint.stage.capitalized) breakpoint", systemImage: "pause.circle")
                                .foregroundStyle(.orange)
                            Text(breakpoint.request.url)
                                .font(.caption)
                                .lineLimit(1)
                                .truncationMode(.middle)
                            Spacer()
                            Button("Edit") {
                                editingBreakpoint = breakpoint
                                breakpointRequestBody = breakpoint.request.body
                                breakpointResponseBody = breakpoint.response?.body ?? ""
                                breakpointStatus = breakpoint.response.map { "\($0.status)" } ?? ""
                            }
                            Button("Continue") {
                                model.resolveDeveloperBreakpoint(breakpoint, action: "continue")
                            }
                            Button("Drop", role: .destructive) {
                                model.resolveDeveloperBreakpoint(breakpoint, action: "drop")
                            }
                        }
                    }
                }
                .padding(12)
                Divider()
            }
        }
    }

    private func breakpointEditor(_ breakpoint: DeveloperPendingBreakpointPayload) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                Label("\(breakpoint.stage.capitalized) Breakpoint", systemImage: "pause.circle")
                    .font(.headline)
                Spacer()
                Button("Drop", role: .destructive) {
                    model.resolveDeveloperBreakpoint(breakpoint, action: "drop")
                    editingBreakpoint = nil
                }
            }
            Text(breakpoint.request.url)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
                .truncationMode(.middle)
            Text("Request Body")
                .font(.subheadline.weight(.semibold))
            TextEditor(text: $breakpointRequestBody)
                .font(.system(.caption, design: .monospaced))
                .frame(minHeight: 110)
            if breakpoint.response != nil {
                HStack {
                    Text("Response Status")
                        .font(.subheadline.weight(.semibold))
                    TextField("Status", text: $breakpointStatus)
                        .textFieldStyle(.roundedBorder)
                        .frame(width: 90)
                }
                Text("Response Body")
                    .font(.subheadline.weight(.semibold))
                TextEditor(text: $breakpointResponseBody)
                    .font(.system(.caption, design: .monospaced))
                    .frame(minHeight: 110)
            }
            Spacer()
            HStack {
                Spacer()
                Button("Cancel") {
                    editingBreakpoint = nil
                }
                Button("Continue Edited") {
                    var request = breakpoint.request
                    request.body = breakpointRequestBody
                    request.bodySet = true
                    var response = breakpoint.response
                    if var editedResponse = response {
                        editedResponse.body = breakpointResponseBody
                        editedResponse.bodySet = true
                        editedResponse.status = Int(breakpointStatus.trimmingCharacters(in: .whitespacesAndNewlines)) ?? editedResponse.status
                        response = editedResponse
                    }
                    model.resolveDeveloperBreakpoint(
                        breakpoint,
                        resolution: DeveloperBreakpointResolutionPayload(action: "continue", request: request, response: response)
                    )
                    editingBreakpoint = nil
                }
                .buttonStyle(.borderedProminent)
            }
        }
        .padding(18)
    }

    private func ruleControls(_ entry: DeveloperEntryPayload) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Tools")
                .font(.subheadline.weight(.semibold))
            HStack {
                TextField("Local file or directory", text: $localPath)
                    .textFieldStyle(.roundedBorder)
                Button("Map Local") {
                    model.addDeveloperMapRule(DeveloperMapRulePayload(
                        name: "Local \(entry.host)",
                        match: matchPayload(entry),
                        kind: "local",
                        localPath: localPath
                    ))
                }
                .disabled(localPath.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
            HStack {
                TextField("Remote base URL", text: $remoteURL)
                    .textFieldStyle(.roundedBorder)
                Button("Map Remote") {
                    model.addDeveloperMapRule(DeveloperMapRulePayload(
                        name: "Remote \(entry.host)",
                        match: matchPayload(entry),
                        kind: "remote",
                        remoteURL: remoteURL
                    ))
                }
                .disabled(remoteURL.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
            HStack {
                Button {
                    model.addDeveloperBreakpointRule(DeveloperBreakpointRulePayload(
                        name: "Breakpoint \(entry.host)",
                        match: matchPayload(entry),
                        stage: "both"
                    ))
                } label: {
                    Label("Breakpoint", systemImage: "pause.circle")
                }
                Text("\(model.developerMapRules.count) map rules / \(model.developerBreakpointRules.count) breakpoint rules")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            DisclosureGroup("Rules") {
                if model.developerMapRules.isEmpty && model.developerBreakpointRules.isEmpty {
                    Text("No developer rules")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(model.developerMapRules) { rule in
                        developerRuleRow(
                            title: rule.name.isEmpty ? "Map \(rule.kind)" : rule.name,
                            subtitle: rule.kind == "local" ? rule.localPath : rule.remoteURL,
                            enabled: rule.enabled,
                            onToggle: { enabled in
                                var rules = model.developerMapRules
                                if let index = rules.firstIndex(where: { $0.id == rule.id }) {
                                    rules[index].enabled = enabled
                                    model.replaceDeveloperMapRules(rules)
                                }
                            },
                            onDelete: {
                                model.replaceDeveloperMapRules(model.developerMapRules.filter { $0.id != rule.id })
                            }
                        )
                    }
                    ForEach(model.developerBreakpointRules) { rule in
                        developerRuleRow(
                            title: rule.name.isEmpty ? "Breakpoint" : rule.name,
                            subtitle: "\(rule.stage) \(rule.match.host)",
                            enabled: rule.enabled,
                            onToggle: { enabled in
                                var rules = model.developerBreakpointRules
                                if let index = rules.firstIndex(where: { $0.id == rule.id }) {
                                    rules[index].enabled = enabled
                                    model.replaceDeveloperBreakpointRules(rules)
                                }
                            },
                            onDelete: {
                                model.replaceDeveloperBreakpointRules(model.developerBreakpointRules.filter { $0.id != rule.id })
                            }
                        )
                    }
                }
            }
        }
    }

    private func developerRuleRow(title: String, subtitle: String, enabled: Bool, onToggle: @escaping (Bool) -> Void, onDelete: @escaping () -> Void) -> some View {
        HStack {
            Toggle("", isOn: Binding(get: { enabled }, set: onToggle))
                .labelsHidden()
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.caption.weight(.semibold))
                if !subtitle.isEmpty {
                    Text(subtitle)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
            }
            Spacer()
            Button(role: .destructive, action: onDelete) {
                Image(systemName: "trash")
            }
            .buttonStyle(.borderless)
        }
    }

    private func messageSection(title: String, message: DeveloperMessagePayload, tab: String) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text(title)
                    .font(.subheadline.weight(.semibold))
                Spacer()
                Text("\(formatBytes(message.body.size))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            switch tab {
            case "body":
                bodyTab(message.body)
            case "json":
                jsonTab(message.body)
            case "cookies":
                cookiesTab(message.cookies)
            default:
                headersTab(message.headers)
            }
        }
    }

    @ViewBuilder
    private func headersTab(_ headers: [DeveloperHeaderPayload]) -> some View {
        if headers.isEmpty {
            Text("No headers")
                .font(.caption)
                .foregroundStyle(.secondary)
        } else {
            ForEach(headers) { header in
                HStack(alignment: .top) {
                    Text(header.name)
                        .font(.system(.caption, design: .monospaced).weight(.semibold))
                        .frame(width: 160, alignment: .leading)
                    Text(header.value)
                        .font(.system(.caption, design: .monospaced))
                        .foregroundStyle(header.redacted ? .red : .secondary)
                        .textSelection(.enabled)
                }
            }
        }
    }

    @ViewBuilder
    private func bodyTab(_ body: DeveloperBodyPayload) -> some View {
        let preview = bodyPreviewText(body)
        if preview.isEmpty {
            Text("No body preview")
                .font(.caption)
                .foregroundStyle(.secondary)
        } else {
            Text(preview)
                .font(.system(.caption, design: .monospaced))
                .textSelection(.enabled)
                .padding(8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.secondary.opacity(0.06), in: RoundedRectangle(cornerRadius: 6))
            Text([body.mimeType, body.encoding].filter { !$0.isEmpty }.joined(separator: " / "))
                .font(.caption)
                .foregroundStyle(.secondary)
            if body.truncated {
                Text("Truncated after \(formatBytes(body.truncatedAfter))")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
        }
    }

    @ViewBuilder
    private func jsonTab(_ body: DeveloperBodyPayload) -> some View {
        if let pretty = prettyJSON(body.preview) {
            Text(pretty)
                .font(.system(.caption, design: .monospaced))
                .textSelection(.enabled)
                .padding(8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.secondary.opacity(0.06), in: RoundedRectangle(cornerRadius: 6))
            if body.truncated {
                Text("JSON preview is truncated")
                    .font(.caption)
                    .foregroundStyle(.orange)
            }
        } else {
            Text("No valid JSON preview")
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    @ViewBuilder
    private func cookiesTab(_ cookies: [DeveloperCookiePayload]) -> some View {
        if cookies.isEmpty {
            Text("No cookies")
                .font(.caption)
                .foregroundStyle(.secondary)
        } else {
            ForEach(cookies) { cookie in
                HStack(alignment: .top) {
                    Text(cookie.name)
                        .font(.system(.caption, design: .monospaced).weight(.semibold))
                        .frame(width: 160, alignment: .leading)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(cookie.value)
                            .font(.system(.caption, design: .monospaced))
                            .foregroundStyle(cookie.redacted ? .red : .secondary)
                            .textSelection(.enabled)
                        let attrs = cookieAttributes(cookie)
                        if !attrs.isEmpty {
                            Text(attrs)
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }
        }
    }

    private var filteredEntries: [DeveloperEntryPayload] {
        let query = captureSearch.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !query.isEmpty else {
            return model.developerEntries
        }
        return model.developerEntries.filter { entry in
            [entry.method, entry.url, entry.host, entry.chainName, "\(entry.status)"]
                .joined(separator: " ")
                .lowercased()
                .contains(query)
        }
    }

    private var selectedEntry: DeveloperEntryPayload? {
        if let selected = model.developerEntries.first(where: { $0.id == selectedEntryID }) {
            return selected
        }
        return filteredEntries.first
    }

    private func displayURL(_ entry: DeveloperEntryPayload) -> String {
        entry.url.isEmpty ? entry.host : entry.url
    }

    private func bodySummary(_ entry: DeveloperEntryPayload) -> String {
        let req = entry.request.body.previewBytes
        let resp = entry.response.body.previewBytes
        if req == 0 && resp == 0 {
            return ""
        }
        return "\(formatBytes(req)) req / \(formatBytes(resp)) resp"
    }

    private func bodyPreviewText(_ body: DeveloperBodyPayload) -> String {
        if !body.preview.isEmpty {
            return body.preview
        }
        if !body.previewBase64.isEmpty {
            return "[base64] \(body.previewBase64)"
        }
        return ""
    }

    private func prettyJSON(_ text: String) -> String? {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, let data = trimmed.data(using: .utf8) else {
            return nil
        }
        guard let object = try? JSONSerialization.jsonObject(with: data),
              JSONSerialization.isValidJSONObject(object),
              let pretty = try? JSONSerialization.data(withJSONObject: object, options: [.prettyPrinted, .sortedKeys])
        else {
            return nil
        }
        return String(data: pretty, encoding: .utf8)
    }

    private func cookieAttributes(_ cookie: DeveloperCookiePayload) -> String {
        var parts: [String] = []
        if !cookie.domain.isEmpty {
            parts.append("domain=\(cookie.domain)")
        }
        if !cookie.path.isEmpty {
            parts.append("path=\(cookie.path)")
        }
        if !cookie.expires.isEmpty {
            parts.append("expires=\(cookie.expires)")
        }
        if cookie.maxAge != 0 {
            parts.append("max-age=\(cookie.maxAge)")
        }
        if cookie.secure {
            parts.append("secure")
        }
        if cookie.httpOnly {
            parts.append("httponly")
        }
        if !cookie.sameSite.isEmpty {
            parts.append("samesite=\(cookie.sameSite)")
        }
        return parts.joined(separator: "  ")
    }

    private func statusColor(_ status: Int) -> Color {
        switch status {
        case 200..<300: return .green
        case 300..<400: return .blue
        case 400..<600: return .red
        default: return .secondary
        }
    }

    private func matchPayload(_ entry: DeveloperEntryPayload) -> DeveloperMatchPayload {
        let path = URL(string: entry.url)?.path ?? "/"
        return DeveloperMatchPayload(
            methods: entry.method.isEmpty ? [] : [entry.method],
            host: entry.host,
            pathPrefix: path.isEmpty ? "/" : path
        )
    }
}
