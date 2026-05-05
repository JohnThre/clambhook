import Foundation

public actor FileSnapshotStore {
    public static var inMemory: FileSnapshotStore {
        FileSnapshotStore(fileURL: nil)
    }

    private let fileURL: URL?
    private var memorySnapshot = DashboardSnapshot()
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()

    public init(fileURL: URL?) {
        self.fileURL = fileURL
        encoder.dateEncodingStrategy = .iso8601
        decoder.dateDecodingStrategy = .iso8601
    }

    public static func appGroupStore(groupIdentifier: String, fileName: String = "dashboard-snapshot.json") -> FileSnapshotStore {
        FileSnapshotStore(fileURL: appGroupURL(groupIdentifier: groupIdentifier, fileName: fileName))
    }

    public static func appGroupURL(groupIdentifier: String, fileName: String = "dashboard-snapshot.json") -> URL? {
        FileManager.default
            .containerURL(forSecurityApplicationGroupIdentifier: groupIdentifier)?
            .appendingPathComponent(fileName)
    }

    public static func loadSync(fileURL: URL?) -> DashboardSnapshot {
        guard
            let fileURL,
            FileManager.default.fileExists(atPath: fileURL.path),
            let data = try? Data(contentsOf: fileURL)
        else {
            return DashboardSnapshot()
        }
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return (try? decoder.decode(DashboardSnapshot.self, from: data)) ?? DashboardSnapshot()
    }

    public func save(_ snapshot: DashboardSnapshot) async throws {
        memorySnapshot = snapshot
        guard let fileURL else {
            return
        }
        let parent = fileURL.deletingLastPathComponent()
        try FileManager.default.createDirectory(at: parent, withIntermediateDirectories: true)
        let data = try encoder.encode(snapshot)
        try data.write(to: fileURL, options: [.atomic])
    }

    public func load() async throws -> DashboardSnapshot {
        guard let fileURL else {
            return memorySnapshot
        }
        guard FileManager.default.fileExists(atPath: fileURL.path) else {
            return memorySnapshot
        }
        let data = try Data(contentsOf: fileURL)
        let snapshot = try decoder.decode(DashboardSnapshot.self, from: data)
        memorySnapshot = snapshot
        return snapshot
    }
}
