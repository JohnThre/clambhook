import ClambhookShared
import SwiftUI
import UniformTypeIdentifiers
import UIKit

struct IOSProfilesView: View {
    @ObservedObject var model: AppleAppModel
    @State private var searchText = ""
    @State private var showingFileImporter = false
    @State private var activeSheet: IOSProfileCaptureSheet?
    @State private var message = ""

    var body: some View {
        List {
            if !message.isEmpty {
                Section {
                    Text(message)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }

            Section {
                if filteredProfiles.isEmpty {
                    ContentUnavailableView(
                        searchText.isEmpty ? "No profiles" : "No matching profiles",
                        systemImage: "person.crop.rectangle.stack",
                        description: Text("Import or create a profile to connect.")
                    )
                } else {
                    ForEach(filteredProfiles, id: \.self) { profile in
                        NavigationLink {
                            IOSProfileDetailView(model: model, profile: profile)
                        } label: {
                            IOSProfileRow(
                                profile: profile,
                                isActive: profile == model.dashboard.activeProfile,
                                routeCount: activeRouteCount(for: profile)
                            )
                        }
                        .swipeActions(edge: .trailing, allowsFullSwipe: true) {
                            if profile != model.dashboard.activeProfile {
                                Button("Use") {
                                    model.selectProfile(profile)
                                }
                                .tint(.blue)
                            }
                        }
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .searchable(text: $searchText, prompt: "Search profiles")
        .refreshable {
            await model.refreshNow()
        }
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    Button {
                        showingFileImporter = true
                    } label: {
                        Label("Import From Files", systemImage: "doc.badge.plus")
                    }

                    Button {
                        importFromClipboard()
                    } label: {
                        Label("Import From Clipboard", systemImage: "doc.on.clipboard")
                    }

                    Button {
                        message = ""
                        activeSheet = .scanQR
                    } label: {
                        Label("Scan QR", systemImage: "qrcode.viewfinder")
                    }

                    Button {
                        activeSheet = .createProfile
                    } label: {
                        Label("Create Manually", systemImage: "plus.circle")
                    }
                } label: {
                    Image(systemName: "plus")
                }
                .accessibilityLabel("Add Profile")
            }
        }
        .fileImporter(
            isPresented: $showingFileImporter,
            allowedContentTypes: [.text, .plainText, .data],
            allowsMultipleSelection: false
        ) { result in
            importFromFile(result)
        }
        .sheet(item: $activeSheet) { sheet in
            switch sheet {
            case .scanQR:
                IOSProfileQRCodeImportView(message: $message) { value in
                    importText(value, successMessage: "Imported QR code.")
                }
            case .createProfile:
                IOSProfileCreateView(model: model) { message in
                    self.message = message
                    model.refresh()
                }
            }
        }
    }

    private var filteredProfiles: [String] {
        let query = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !query.isEmpty else {
            return model.dashboard.profiles.profiles
        }
        return model.dashboard.profiles.profiles.filter { $0.lowercased().contains(query) }
    }

    private func activeRouteCount(for profile: String) -> Int {
        guard profile == model.dashboard.activeProfile else {
            return 0
        }
        return model.dashboard.servers.chains.reduce(0) { $0 + $1.servers.count }
    }

    private func importFromClipboard() {
        guard let text = UIPasteboard.general.string, !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            message = "Clipboard does not contain profile text."
            return
        }
        _ = importText(text, successMessage: "Imported clipboard profile.")
    }

    private func importFromFile(_ result: Result<[URL], Error>) {
        do {
            guard let url = try result.get().first else {
                return
            }
            let scoped = url.startAccessingSecurityScopedResource()
            defer {
                if scoped {
                    url.stopAccessingSecurityScopedResource()
                }
            }
            _ = importText(try String(contentsOf: url, encoding: .utf8), successMessage: "Imported file profile.")
        } catch {
            message = error.localizedDescription
        }
    }

    private func importText(_ raw: String, successMessage: String) -> Bool {
        do {
            try model.importTunnelConfigText(raw)
            message = successMessage
            model.refresh()
            return true
        } catch {
            message = error.localizedDescription
            return false
        }
    }
}

private enum IOSProfileCaptureSheet: String, Identifiable {
    case scanQR
    case createProfile

    var id: String { rawValue }
}

private struct IOSProfileRow: View {
    var profile: String
    var isActive: Bool
    var routeCount: Int

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: isActive ? "checkmark.circle.fill" : "circle")
                .foregroundStyle(isActive ? Color.green : Color.secondary)
                .frame(width: 24)

            VStack(alignment: .leading, spacing: 3) {
                Text(emptyDash(profile))
                    .font(.body.weight(.medium))
                    .lineLimit(1)
                Text(subtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, 2)
    }

    private var subtitle: String {
        if isActive {
            return routeCount == 1 ? "Active / 1 route" : "Active / \(routeCount) routes"
        }
        return "Inactive"
    }
}

private struct IOSProfileDetailView: View {
    @ObservedObject var model: AppleAppModel
    var profile: String

    var body: some View {
        List {
            Section {
                LabeledContent("State", value: isActive ? "Active" : "Inactive")

                if !isActive {
                    Button {
                        model.selectProfile(profile)
                    } label: {
                        Label("Use Profile", systemImage: "checkmark.circle")
                    }
                }
            }

            if isActive {
                if routeRows.isEmpty {
                    Section {
                        ContentUnavailableView(
                            "No routes",
                            systemImage: "point.3.connected.trianglepath.dotted",
                            description: Text("Routes from this profile appear here.")
                        )
                    }
                } else {
                    Section("Routes") {
                        ForEach(routeRows) { row in
                            IOSServerHealthRow(row: row)
                        }
                    }
                }

                Section("Rules") {
                    LabeledContent("Active rules", value: "\(model.dashboard.rules.rules.count)")
                }
            } else {
                Section {
                    IOSInlineEmptyState(text: "Make active to inspect routes.", systemImage: "checkmark.circle")
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle(emptyDash(profile))
        .navigationBarTitleDisplayMode(.inline)
        .refreshable {
            await model.refreshNow()
        }
    }

    private var isActive: Bool {
        profile == model.dashboard.activeProfile
    }

    private var routeRows: [IOSServerHealthRowData] {
        let health = model.dashboard.passiveServerHealth
        return model.dashboard.servers.chains.flatMap { chain in
            chain.servers.map { server in
                IOSServerHealthRowData(chainName: chain.name, server: server, health: health[server.id])
            }
        }
    }
}

private struct IOSProfileQRCodeImportView: View {
    @Binding var message: String
    var onImport: (String) -> Bool
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                IOSQRCodeScannerView { value in
                    if onImport(value) {
                        dismiss()
                        return true
                    }
                    return false
                }
                .frame(maxWidth: .infinity)
                .frame(height: 360)
                .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
                .padding(20)

                if !message.isEmpty {
                    Text(message)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 20)
                }

                Spacer(minLength: 0)
            }
            .background(Color(.systemGroupedBackground))
            .navigationTitle("Scan QR")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
            }
        }
    }
}

private struct IOSProfileCreateView: View {
    @ObservedObject var model: AppleAppModel
    var onCreated: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var request = TunnelProfileCreateRequest()
    @State private var message = ""

    var body: some View {
        NavigationStack {
            Form {
                Section("Profile") {
                    TextField("Profile name", text: $request.profileName)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }

                Section("Endpoint") {
                    TextField("Display name", text: $request.serverName)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("Address", text: $request.serverAddress)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("Protocol", text: $request.protocol)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("Route", text: $request.chainName)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                }

                Section("Settings") {
                    TextEditor(text: $request.settingsTOML)
                        .font(.system(.footnote, design: .monospaced))
                        .frame(minHeight: 130)
                }

                if !message.isEmpty {
                    Section {
                        Text(message)
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            .navigationTitle("Create Profile")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button("Cancel") {
                        dismiss()
                    }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Create") {
                        createProfile()
                    }
                    .fontWeight(.semibold)
                    .disabled(createDisabled)
                }
            }
        }
    }

    private var createDisabled: Bool {
        request.profileName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ||
        request.serverAddress.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private func createProfile() {
        do {
            try model.createTunnelProfile(request)
            onCreated("Created profile.")
            dismiss()
        } catch {
            message = error.localizedDescription
        }
    }
}
