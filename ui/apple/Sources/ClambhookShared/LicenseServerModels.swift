// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

import Foundation

public let defaultLicenseValidationURL = URL(string: "https://store.swiphtgroup.com/clambhook/license")!
public let defaultLicensePortalURL = URL(string: "https://store.swiphtgroup.com/clambhook/portal")!
public let defaultLicensePurchaseURL = URL(string: "https://store.swiphtgroup.com/clambhook/buy")!
public let clambHookKoFiURL = URL(string: "https://ko-fi.com/jpfchang")!
public let clambHookLiberapayURL = URL(string: "https://en.liberapay.com/jpfchang/")!
public let clambHookIssueHuntURL = URL(string: "https://oss.issuehunt.io/u/johnthre")!
public let clambHookNowPaymentsDonationURL = URL(string: "https://nowpayments.io/donation?api_key=4f798f1e-c93e-456e-8067-b03b200790cd")!
public let mobileLicenseServerGrantDefaultsKey = "clambhook.apple.license.server-grant"

public struct MobilePaymentProviderDetails: Equatable, Sendable {
    public let id: String
    public let label: String
    public let paymentTypeLabel: String
    public let officialURL: URL
    public let flowDescription: String

    public init(
        id: String,
        label: String,
        paymentTypeLabel: String,
        officialURL: URL,
        flowDescription: String
    ) {
        self.id = id
        self.label = label
        self.paymentTypeLabel = paymentTypeLabel
        self.officialURL = officialURL
        self.flowDescription = flowDescription
    }
}

public let clambHookPaymentProviderDetails = [
    MobilePaymentProviderDetails(
        id: "creem",
        label: "Creem",
        paymentTypeLabel: "Card",
        officialURL: URL(string: "https://creem.io/")!,
        flowDescription: "You’ll continue to Creem’s hosted card checkout."
    ),
    MobilePaymentProviderDetails(
        id: "nowpayments",
        label: "NOWPayments",
        paymentTypeLabel: "Cryptocurrency",
        officialURL: URL(string: "https://nowpayments.io/")!,
        flowDescription: "NOWPayments will email your cryptocurrency subscription link."
    ),
]

public let clambHookPaymentTrustSummary = "Card via Creem · Cryptocurrency via NOWPayments"

public struct MobileServerLicenseGrantResponse: Codable, Equatable, Sendable {
    public var grant: MobileServerLicenseGrant
    public var snapshot: MobileServerGrantSnapshot
}

public struct MacLicenseServerResponse: Codable, Equatable, Sendable {
    public var grant: MobileServerLicenseGrant
    public var snapshot: MobileServerGrantSnapshot
    public var deviceState: MobileLicenseDeviceState

    public init(
        grant: MobileServerLicenseGrant,
        snapshot: MobileServerGrantSnapshot,
        deviceState: MobileLicenseDeviceState
    ) {
        self.grant = grant
        self.snapshot = snapshot
        self.deviceState = deviceState
    }

    enum CodingKeys: String, CodingKey {
        case grant
        case snapshot
        case deviceState = "device_state"
    }
}

public struct MacLicenseActivationRequest: Codable, Equatable, Sendable {
    public var licenseKey: String
    public var email: String?
    public var device: MobileLicenseDeviceRegistration

    public init(licenseKey: String, email: String?, device: MobileLicenseDeviceRegistration) {
        self.licenseKey = licenseKey
        self.email = email
        self.device = device
    }

    enum CodingKeys: String, CodingKey {
        case licenseKey = "license_key"
        case email
        case device
    }
}

public struct MacLicenseDeviceActionRequest: Codable, Equatable, Sendable {
    public var licenseKey: String
    public var installID: String
    public var deviceID: String?
    public var device: MobileLicenseDeviceRegistration

    public init(
        licenseKey: String,
        installID: String,
        deviceID: String?,
        device: MobileLicenseDeviceRegistration
    ) {
        self.licenseKey = licenseKey
        self.installID = installID
        self.deviceID = deviceID
        self.device = device
    }

    enum CodingKeys: String, CodingKey {
        case licenseKey = "license_key"
        case installID = "install_id"
        case deviceID = "device_id"
        case device
    }
}

public struct MobileServerLicenseGrant: Codable, Equatable, Sendable {
    public var version: Int
    public var issuedAt: Date
    public var expiresAt: Date
    public var reason: MobileLicenseAccessReason
    public var trialStartDate: Date?
    public var trialEndsAt: Date?
    public var hasLifetimeUnlock: Bool
    public var updateCutoffDate: Date?
    public var transactions: [MobileLicenseTransaction]
    public var signature: String

    enum CodingKeys: String, CodingKey {
        case version
        case issuedAt = "issued_at"
        case expiresAt = "expires_at"
        case reason
        case trialStartDate = "trial_start_date"
        case trialEndsAt = "trial_ends_at"
        case hasLifetimeUnlock = "has_lifetime_unlock"
        case updateCutoffDate = "update_cutoff_date"
        case transactions
        case signature
    }
}

public struct MobileServerGrantSnapshot: Codable, Equatable, Sendable {
    public var reason: MobileLicenseAccessReason
    public var trialStartDate: Date?
    public var trialEndsAt: Date?
    public var hasLifetimeUnlock: Bool
    public var updateCutoffDate: Date?
    public var transactions: [MobileLicenseTransaction]

    enum CodingKeys: String, CodingKey {
        case reason
        case trialStartDate = "trial_start_date"
        case trialEndsAt = "trial_ends_at"
        case hasLifetimeUnlock = "has_lifetime_unlock"
        case updateCutoffDate = "update_cutoff_date"
        case transactions
    }

    public var licenseSnapshot: MobileLicenseSnapshot {
        MobileLicenseSnapshot(
            trialStartDate: trialStartDate,
            transactions: transactions,
            lastVerifiedAt: Date(),
            lastVerificationFailedAt: nil,
            cachedAt: Date()
        )
    }
}

public enum MobileServerLicenseGrantStore {
    public static func load(
        defaults: UserDefaults = UserDefaults(suiteName: defaultAppGroupIdentifier) ?? .standard,
        key: String = mobileLicenseServerGrantDefaultsKey
    ) -> MobileServerLicenseGrant? {
        guard
            let data = defaults.data(forKey: key),
            let grant = try? decoder.decode(MobileServerLicenseGrant.self, from: data)
        else {
            return nil
        }
        return grant
    }

    public static func save(
        _ grant: MobileServerLicenseGrant,
        defaults: UserDefaults = UserDefaults(suiteName: defaultAppGroupIdentifier) ?? .standard,
        key: String = mobileLicenseServerGrantDefaultsKey
    ) {
        if let data = try? encoder.encode(grant) {
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
