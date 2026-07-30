import Foundation

public struct ConditionerPayload: Codable, Equatable, Sendable {
    public var profile: String
    public var enabled: Bool
    public var downloadKbps: Int
    public var uploadKbps: Int
    public var latency: String
    public var jitter: String
    public var lossPercent: Double
    public var backupPath: String

    enum CodingKeys: String, CodingKey {
        case profile
        case enabled
        case downloadKbps = "download_kbps"
        case uploadKbps = "upload_kbps"
        case latency
        case jitter
        case lossPercent = "loss_percent"
        case backupPath = "backup_path"
    }

    public init(
        profile: String = "",
        enabled: Bool = false,
        downloadKbps: Int = 0,
        uploadKbps: Int = 0,
        latency: String = "",
        jitter: String = "",
        lossPercent: Double = 0,
        backupPath: String = ""
    ) {
        self.profile = profile
        self.enabled = enabled
        self.downloadKbps = downloadKbps
        self.uploadKbps = uploadKbps
        self.latency = latency
        self.jitter = jitter
        self.lossPercent = lossPercent
        self.backupPath = backupPath
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.profile = try container.decodeIfPresent(String.self, forKey: .profile) ?? ""
        self.enabled = try container.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        self.downloadKbps = try container.decodeIfPresent(Int.self, forKey: .downloadKbps) ?? 0
        self.uploadKbps = try container.decodeIfPresent(Int.self, forKey: .uploadKbps) ?? 0
        self.latency = try container.decodeIfPresent(String.self, forKey: .latency) ?? ""
        self.jitter = try container.decodeIfPresent(String.self, forKey: .jitter) ?? ""
        self.lossPercent = try container.decodeIfPresent(Double.self, forKey: .lossPercent) ?? 0
        self.backupPath = try container.decodeIfPresent(String.self, forKey: .backupPath) ?? ""
    }
}

public struct ConditionerUpdateRequest: Codable, Equatable, Sendable {
    public var profile: String?
    public var enabled: Bool?
    public var downloadKbps: Int?
    public var uploadKbps: Int?
    public var latency: String?
    public var jitter: String?
    public var lossPercent: Double?

    enum CodingKeys: String, CodingKey {
        case profile
        case enabled
        case downloadKbps = "download_kbps"
        case uploadKbps = "upload_kbps"
        case latency
        case jitter
        case lossPercent = "loss_percent"
    }

    public init(
        profile: String? = nil,
        enabled: Bool? = nil,
        downloadKbps: Int? = nil,
        uploadKbps: Int? = nil,
        latency: String? = nil,
        jitter: String? = nil,
        lossPercent: Double? = nil
    ) {
        self.profile = profile
        self.enabled = enabled
        self.downloadKbps = downloadKbps
        self.uploadKbps = uploadKbps
        self.latency = latency
        self.jitter = jitter
        self.lossPercent = lossPercent
    }
}
