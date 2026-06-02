import Foundation

public struct AttentionState: Codable, Equatable, Sendable {
    public var version: Int
    public var inbox: [InboxImportItem]
    public var scheduled: [ScheduledAttentionItem]
    public var someday: [SomedayExperimentItem]

    public init(
        version: Int = 1,
        inbox: [InboxImportItem] = [],
        scheduled: [ScheduledAttentionItem] = [],
        someday: [SomedayExperimentItem] = []
    ) {
        self.version = version
        self.inbox = inbox
        self.scheduled = scheduled
        self.someday = someday
    }
}

public enum InboxImportSource: String, Codable, CaseIterable, Identifiable, Sendable {
    case file
    case clipboard
    case qr
    case manual

    public var id: Self { self }

    public var displayName: String {
        switch self {
        case .file:
            return "File"
        case .clipboard:
            return "Clipboard"
        case .qr:
            return "QR"
        case .manual:
            return "Manual"
        }
    }
}

public struct InboxImportItem: Codable, Equatable, Identifiable, Sendable {
    public var id: UUID
    public var createdAt: Date
    public var source: InboxImportSource
    public var title: String
    public var decodedConfigText: String
    public var lastError: String

    public init(
        id: UUID = UUID(),
        createdAt: Date = Date(),
        source: InboxImportSource,
        title: String,
        decodedConfigText: String,
        lastError: String = ""
    ) {
        self.id = id
        self.createdAt = createdAt
        self.source = source
        self.title = title
        self.decodedConfigText = decodedConfigText
        self.lastError = lastError
    }

    public var preview: InboxImportPreview {
        InboxImportPreview(configText: decodedConfigText)
    }
}

public struct InboxImportPreview: Equatable, Sendable {
    public var activeProfile: String
    public var profileNames: [String]
    public var serverCount: Int
    public var redactedSnippet: String

    public init(configText: String) {
        self.activeProfile = Self.value(for: "active", in: configText) ?? ""
        self.profileNames = Self.values(for: "name", afterSection: "[[profile]]", in: configText)
        self.serverCount = configText
            .components(separatedBy: .newlines)
            .filter { $0.trimmingCharacters(in: .whitespaces).lowercased() == "[[profile.chain.server]]" }
            .count
        self.redactedSnippet = Self.redactedSnippet(from: configText)
    }

    public var summary: String {
        var parts: [String] = []
        if !activeProfile.isEmpty {
            parts.append("Active \(activeProfile)")
        }
        if !profileNames.isEmpty {
            let count = profileNames.count
            parts.append(count == 1 ? "1 profile" : "\(count) profiles")
        }
        if serverCount > 0 {
            parts.append(serverCount == 1 ? "1 server" : "\(serverCount) servers")
        }
        return parts.isEmpty ? "Config preview unavailable" : parts.joined(separator: " / ")
    }

    private static func values(for key: String, afterSection section: String, in text: String) -> [String] {
        var values: [String] = []
        var captureNextName = false
        for rawLine in text.components(separatedBy: .newlines) {
            let line = rawLine.trimmingCharacters(in: .whitespaces)
            let lower = line.lowercased()
            if lower == section.lowercased() {
                captureNextName = true
                continue
            }
            if lower.hasPrefix("[") {
                captureNextName = false
            }
            if captureNextName, let value = value(for: key, inLine: line) {
                values.append(value)
                captureNextName = false
            }
        }
        return values
    }

    private static func value(for key: String, in text: String) -> String? {
        for rawLine in text.components(separatedBy: .newlines) {
            if let value = value(for: key, inLine: rawLine.trimmingCharacters(in: .whitespaces)) {
                return value
            }
        }
        return nil
    }

    private static func value(for key: String, inLine line: String) -> String? {
        let prefix = "\(key) ="
        guard line.lowercased().hasPrefix(prefix) else {
            return nil
        }
        let raw = line.dropFirst(prefix.count).trimmingCharacters(in: .whitespaces)
        return raw.trimmingCharacters(in: CharacterSet(charactersIn: "\"'"))
    }

    private static func redactedSnippet(from text: String, maxLines: Int = 8) -> String {
        text.components(separatedBy: .newlines)
            .prefix(maxLines)
            .map(redactedLine)
            .joined(separator: "\n")
    }

    private static func redactedLine(_ line: String) -> String {
        let lower = line.lowercased()
        let sensitiveKeys = ["password", "private_key", "preshared_key", "token", "secret", "uuid"]
        guard sensitiveKeys.contains(where: { lower.contains($0) }), let equals = line.firstIndex(of: "=") else {
            return line
        }
        return String(line[..<equals]) + "= \"[redacted]\""
    }
}

public enum ScheduledAttentionKind: String, Codable, CaseIterable, Identifiable, Sendable {
    case serverTest
    case credentialRenewal

    public var id: Self { self }

    public var displayName: String {
        switch self {
        case .serverTest:
            return "Server Test"
        case .credentialRenewal:
            return "Credential Renewal"
        }
    }
}

public enum ScheduledRecurrence: String, Codable, CaseIterable, Identifiable, Sendable {
    case none
    case daily
    case weekly
    case monthly

    public var id: Self { self }

    public var displayName: String {
        switch self {
        case .none:
            return "None"
        case .daily:
            return "Daily"
        case .weekly:
            return "Weekly"
        case .monthly:
            return "Monthly"
        }
    }

    public func nextDate(after date: Date, calendar: Calendar = .current) -> Date? {
        switch self {
        case .none:
            return nil
        case .daily:
            return calendar.date(byAdding: .day, value: 1, to: date)
        case .weekly:
            return calendar.date(byAdding: .day, value: 7, to: date)
        case .monthly:
            return calendar.date(byAdding: .month, value: 1, to: date)
        }
    }
}

public struct ScheduledAttentionItem: Codable, Equatable, Identifiable, Sendable {
    public var id: UUID
    public var createdAt: Date
    public var dueAt: Date
    public var completedAt: Date?
    public var kind: ScheduledAttentionKind
    public var recurrence: ScheduledRecurrence
    public var title: String
    public var detail: String

    public init(
        id: UUID = UUID(),
        createdAt: Date = Date(),
        dueAt: Date,
        completedAt: Date? = nil,
        kind: ScheduledAttentionKind,
        recurrence: ScheduledRecurrence = .none,
        title: String,
        detail: String = ""
    ) {
        self.id = id
        self.createdAt = createdAt
        self.dueAt = dueAt
        self.completedAt = completedAt
        self.kind = kind
        self.recurrence = recurrence
        self.title = title
        self.detail = detail
    }

    public func isDue(on date: Date, calendar: Calendar = .current) -> Bool {
        completedAt == nil && dueAt <= calendar.startOfDay(for: date).addingTimeInterval(24 * 60 * 60 - 1)
    }
}

public struct SomedayExperimentItem: Codable, Equatable, Identifiable, Sendable {
    public var id: UUID
    public var createdAt: Date
    public var title: String
    public var detail: String
    public var configText: String

    public init(
        id: UUID = UUID(),
        createdAt: Date = Date(),
        title: String,
        detail: String = "",
        configText: String = ""
    ) {
        self.id = id
        self.createdAt = createdAt
        self.title = title
        self.detail = detail
        self.configText = configText
    }
}

@MainActor
public final class AttentionStore: ObservableObject {
    @Published public private(set) var state: AttentionState

    private let fileURL: URL?
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()

    public init(fileURL: URL? = nil) {
        self.fileURL = fileURL
        encoder.dateEncodingStrategy = .iso8601
        decoder.dateDecodingStrategy = .iso8601
        self.state = Self.load(fileURL: fileURL, decoder: decoder)
    }

    public static func appGroupStore(groupIdentifier: String, fileName: String = "attention-state.json") -> AttentionStore {
        AttentionStore(fileURL: FileSnapshotStore.appGroupURL(groupIdentifier: groupIdentifier, fileName: fileName))
    }

    @discardableResult
    public func captureImport(
        rawValue: String,
        source: InboxImportSource,
        title: String = ""
    ) throws -> InboxImportItem {
        let decoded = try TunnelImportDecoder.decode(rawValue)
        let item = InboxImportItem(
            source: source,
            title: title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? defaultInboxTitle(source: source, decodedText: decoded) : title,
            decodedConfigText: decoded
        )
        state.inbox.insert(item, at: 0)
        persist()
        return item
    }

    public func removeInboxItem(id: UUID) {
        state.inbox.removeAll { $0.id == id }
        persist()
    }

    public func markInboxImportError(id: UUID, error: String) {
        guard let index = state.inbox.firstIndex(where: { $0.id == id }) else {
            return
        }
        state.inbox[index].lastError = error
        persist()
    }

    @discardableResult
    public func moveInboxItemToSomeday(id: UUID) -> SomedayExperimentItem? {
        guard let index = state.inbox.firstIndex(where: { $0.id == id }) else {
            return nil
        }
        let item = state.inbox.remove(at: index)
        let someday = SomedayExperimentItem(
            title: item.title,
            detail: item.preview.summary,
            configText: item.decodedConfigText
        )
        state.someday.insert(someday, at: 0)
        persist()
        return someday
    }

    public func restoreSomedayItemToInbox(id: UUID) {
        guard let index = state.someday.firstIndex(where: { $0.id == id }) else {
            return
        }
        let item = state.someday.remove(at: index)
        state.inbox.insert(InboxImportItem(
            source: .manual,
            title: item.title,
            decodedConfigText: item.configText
        ), at: 0)
        persist()
    }

    public func removeSomedayItem(id: UUID) {
        state.someday.removeAll { $0.id == id }
        persist()
    }

    @discardableResult
    public func addSomedayItem(title: String, detail: String = "", configText: String = "") -> SomedayExperimentItem {
        let item = SomedayExperimentItem(title: title, detail: detail, configText: configText)
        state.someday.insert(item, at: 0)
        persist()
        return item
    }

    @discardableResult
    public func addScheduledItem(
        title: String,
        detail: String = "",
        kind: ScheduledAttentionKind,
        dueAt: Date,
        recurrence: ScheduledRecurrence = .none
    ) -> ScheduledAttentionItem {
        let item = ScheduledAttentionItem(
            dueAt: dueAt,
            kind: kind,
            recurrence: recurrence,
            title: title,
            detail: detail
        )
        state.scheduled.append(item)
        state.scheduled.sort { $0.dueAt < $1.dueAt }
        persist()
        return item
    }

    public func completeScheduledItem(id: UUID, completedAt: Date = Date(), calendar: Calendar = .current) {
        guard let index = state.scheduled.firstIndex(where: { $0.id == id }) else {
            return
        }
        if let next = state.scheduled[index].recurrence.nextDate(after: state.scheduled[index].dueAt, calendar: calendar) {
            state.scheduled[index].dueAt = next
            state.scheduled[index].completedAt = nil
        } else {
            state.scheduled[index].completedAt = completedAt
        }
        state.scheduled.sort { $0.dueAt < $1.dueAt }
        persist()
    }

    public func removeScheduledItem(id: UUID) {
        state.scheduled.removeAll { $0.id == id }
        persist()
    }

    public func updateScheduledItem(_ item: ScheduledAttentionItem) {
        guard let index = state.scheduled.firstIndex(where: { $0.id == item.id }) else {
            return
        }
        state.scheduled[index] = item
        state.scheduled.sort { $0.dueAt < $1.dueAt }
        persist()
    }

    public func dueScheduledItems(on date: Date = Date(), calendar: Calendar = .current) -> [ScheduledAttentionItem] {
        state.scheduled.filter { $0.isDue(on: date, calendar: calendar) }.sorted { $0.dueAt < $1.dueAt }
    }

    public func upcomingScheduledItems(after date: Date = Date()) -> [ScheduledAttentionItem] {
        state.scheduled
            .filter { $0.completedAt == nil && $0.dueAt > date }
            .sorted { $0.dueAt < $1.dueAt }
    }

    private func persist() {
        guard let fileURL else {
            return
        }
        do {
            try FileManager.default.createDirectory(at: fileURL.deletingLastPathComponent(), withIntermediateDirectories: true)
            let data = try encoder.encode(state)
            try data.write(to: fileURL, options: [.atomic])
        } catch {
            // The UI keeps the in-memory state even if disk persistence fails.
        }
    }

    private static func load(fileURL: URL?, decoder: JSONDecoder) -> AttentionState {
        guard
            let fileURL,
            FileManager.default.fileExists(atPath: fileURL.path),
            let data = try? Data(contentsOf: fileURL),
            let decoded = try? decoder.decode(AttentionState.self, from: data),
            decoded.version == 1
        else {
            return AttentionState()
        }
        return decoded
    }

    private func defaultInboxTitle(source: InboxImportSource, decodedText: String) -> String {
        let preview = InboxImportPreview(configText: decodedText)
        if !preview.activeProfile.isEmpty {
            return preview.activeProfile
        }
        return "\(source.displayName) import"
    }
}
