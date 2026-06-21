import Foundation

public let mobileLicenseMaxActiveDevices = 4
public let mobileLicenseDeviceStateDefaultsKey = "clambhook.apple.license.device-state"

public enum MobileLicenseCommercialTerms {
    public static let licensePriceUSD = "99.99"
    public static let paidFeatureUpdatePriceUSD = "8.99"
    public static let includedFeatureUpdateYears = 1
    public static let maxActiveDevices = mobileLicenseMaxActiveDevices
}

public enum MobileLicensePaymentProvider: String, Codable, Equatable, Sendable {
    case creem
    case nowPayments = "nowpayments"
    case manual
    case unknown
}

public enum MobileLicenseDeviceStatus: String, Codable, Equatable, Sendable {
    case active
    case deactivated
}

public struct MobileLicenseDeviceRegistration: Codable, Equatable, Sendable {
    public var installID: String
    public var displayName: String
    public var platform: String
    public var architecture: String
    public var appVersion: String?

    public init(
        installID: String,
        displayName: String,
        platform: String,
        architecture: String,
        appVersion: String? = nil
    ) {
        self.installID = installID
        self.displayName = displayName
        self.platform = platform
        self.architecture = architecture
        self.appVersion = appVersion
    }

    enum CodingKeys: String, CodingKey {
        case installID = "install_id"
        case displayName = "display_name"
        case platform
        case architecture
        case appVersion = "app_version"
    }
}

public struct MobileLicenseDevice: Identifiable, Codable, Equatable, Sendable {
    public var deviceID: String
    public var installID: String
    public var displayName: String
    public var platform: String
    public var architecture: String
    public var activatedAt: Date
    public var lastSeenAt: Date?
    public var deactivatedAt: Date?

    public var id: String { deviceID }

    public init(
        deviceID: String,
        installID: String,
        displayName: String,
        platform: String,
        architecture: String,
        activatedAt: Date,
        lastSeenAt: Date? = nil,
        deactivatedAt: Date? = nil
    ) {
        self.deviceID = deviceID
        self.installID = installID
        self.displayName = displayName
        self.platform = platform
        self.architecture = architecture
        self.activatedAt = activatedAt
        self.lastSeenAt = lastSeenAt
        self.deactivatedAt = deactivatedAt
    }

    public var status: MobileLicenseDeviceStatus {
        deactivatedAt == nil ? .active : .deactivated
    }

    public var isActive: Bool {
        status == .active
    }

    enum CodingKeys: String, CodingKey {
        case deviceID = "device_id"
        case installID = "install_id"
        case displayName = "display_name"
        case platform
        case architecture
        case activatedAt = "activated_at"
        case lastSeenAt = "last_seen_at"
        case deactivatedAt = "deactivated_at"
    }
}

public struct MobileLicenseDeviceState: Codable, Equatable, Sendable {
    public var currentInstallID: String
    public var currentDeviceID: String?
    public var maxActiveDevices: Int
    public var devices: [MobileLicenseDevice]
    public var paymentProvider: MobileLicensePaymentProvider?

    public init(
        currentInstallID: String = "",
        currentDeviceID: String? = nil,
        maxActiveDevices: Int = mobileLicenseMaxActiveDevices,
        devices: [MobileLicenseDevice] = [],
        paymentProvider: MobileLicensePaymentProvider? = nil
    ) {
        self.currentInstallID = currentInstallID
        self.currentDeviceID = currentDeviceID
        self.maxActiveDevices = maxActiveDevices
        self.devices = devices
        self.paymentProvider = paymentProvider
    }

    public var activeDevices: [MobileLicenseDevice] {
        devices.filter(\.isActive)
    }

    public var activeDeviceCount: Int {
        activeDevices.count
    }

    public var remainingActivations: Int {
        max(0, maxActiveDevices - activeDeviceCount)
    }

    public var currentDevice: MobileLicenseDevice? {
        if let currentDeviceID {
            return devices.first { $0.deviceID == currentDeviceID }
        }
        return devices.first { !$0.installID.isEmpty && $0.installID == currentInstallID }
    }

    public var isCurrentDeviceActive: Bool {
        currentDevice?.isActive == true
    }

    public var canActivateCurrentDevice: Bool {
        isCurrentDeviceActive || activeDeviceCount < maxActiveDevices
    }

    public var canReactivateCurrentDevice: Bool {
        guard currentDevice != nil, !isCurrentDeviceActive else {
            return false
        }
        return activeDeviceCount < maxActiveDevices
    }

    public var canTransferCurrentDevice: Bool {
        isCurrentDeviceActive
    }

    public func withCurrentInstallID(_ installID: String) -> MobileLicenseDeviceState {
        var copy = self
        copy.currentInstallID = installID
        return copy
    }

    enum CodingKeys: String, CodingKey {
        case currentInstallID = "current_install_id"
        case currentDeviceID = "current_device_id"
        case maxActiveDevices = "max_active_devices"
        case devices
        case paymentProvider = "payment_provider"
    }
}

public enum MobileLicenseDeviceStateStore {
    public static func load(
        defaults: UserDefaults = UserDefaults(suiteName: defaultAppGroupIdentifier) ?? .standard,
        key: String = mobileLicenseDeviceStateDefaultsKey
    ) -> MobileLicenseDeviceState {
        guard
            let data = defaults.data(forKey: key),
            let state = try? decoder.decode(MobileLicenseDeviceState.self, from: data)
        else {
            return MobileLicenseDeviceState()
        }
        return state
    }

    public static func save(
        _ state: MobileLicenseDeviceState,
        defaults: UserDefaults = UserDefaults(suiteName: defaultAppGroupIdentifier) ?? .standard,
        key: String = mobileLicenseDeviceStateDefaultsKey
    ) {
        if let data = try? encoder.encode(state) {
            defaults.set(data, forKey: key)
        }
    }

    private static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return decoder
    }()

    private static let encoder: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        return encoder
    }()
}
