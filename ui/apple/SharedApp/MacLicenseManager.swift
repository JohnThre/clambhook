import ClambhookShared
import Combine
import Foundation

#if os(macOS)
private let macLicenseInstallAccount = "mac-license-install-id"
private let macLicenseKeyAccount = "mac-license-key"
private let macLicenseEmailAccount = "mac-license-email"

@MainActor
final class MacLicenseManager: ObservableObject {
    @Published private(set) var snapshot: MobileLicenseSnapshot
    @Published private(set) var decision: MobileLicenseDecision
    @Published private(set) var deviceState: MobileLicenseDeviceState
    @Published private(set) var installID = ""
    @Published private(set) var isLoading = false
    @Published private(set) var statusMessage = ""

    private let defaults: UserDefaults
    private let credentialStore: CredentialStoring
    private let licenseClient: MacLicenseValidationClient
    private var started = false

    init(
        defaults: UserDefaults = UserDefaults(suiteName: defaultAppGroupIdentifier) ?? .standard,
        credentialStore: CredentialStoring = KeychainCredentialStore(service: "org.jpfchang.clambhook.license"),
        licenseValidationEndpoint: URL = defaultLicenseValidationURL
    ) {
        self.defaults = defaults
        self.credentialStore = credentialStore
        self.licenseClient = MacLicenseValidationClient(endpoint: licenseValidationEndpoint)
        let initialSnapshot = Self.initialSnapshot(defaults: defaults)
        let initialInstallID = (try? credentialStore.readToken(account: macLicenseInstallAccount)) ?? ""
        self.snapshot = initialSnapshot
        self.decision = MobileLicenseEvaluator.evaluate(snapshot: initialSnapshot)
        self.deviceState = MobileLicenseDeviceStateStore
            .load(defaults: defaults)
            .withCurrentInstallID(initialInstallID)
        self.installID = initialInstallID
    }

    func start(now: Date = Date()) {
        guard !started else {
            refreshDecision(now: now)
            return
        }
        started = true
        installID = ensureInstallID()
        let nextDeviceState = deviceState.withCurrentInstallID(installID)
        deviceState = nextDeviceState
        MobileLicenseDeviceStateStore.save(nextDeviceState, defaults: defaults)
        save(MobileLicenseTrialStore.resolvedSnapshot(snapshot: snapshot, credentialStore: credentialStore, now: now), now: now)
    }

    func refreshDecision(now: Date = Date()) {
        decision = MobileLicenseEvaluator.evaluate(snapshot: snapshot, now: now)
    }

    func savedLicenseKey() -> String {
        (try? credentialStore.readToken(account: macLicenseKeyAccount)) ?? ""
    }

    func savedEmail() -> String {
        (try? credentialStore.readToken(account: macLicenseEmailAccount)) ?? ""
    }

    func activate(licenseKey: String, email: String?) async {
        let trimmedKey = licenseKey.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedKey.isEmpty else {
            statusMessage = "Enter a license key to activate this Mac."
            return
        }
        await performServerAction {
            let response = try await licenseClient.activate(
                licenseKey: trimmedKey,
                email: normalizedOptional(email),
                device: deviceRegistration()
            )
            try credentialStore.saveToken(trimmedKey, account: macLicenseKeyAccount)
            try credentialStore.saveToken(normalizedOptional(email), account: macLicenseEmailAccount)
            apply(response, message: "License activated.")
        }
    }

    func deactivateCurrentDevice() async {
        await performDeviceAction(path: "deactivate", message: "This Mac was deactivated.")
    }

    func reactivateCurrentDevice() async {
        guard deviceState.canActivateCurrentDevice || deviceState.canReactivateCurrentDevice else {
            statusMessage = "All \(deviceState.maxActiveDevices) device seats are active. Deactivate another device in ClambHook or the License Portal before reactivating this Mac."
            return
        }
        await performDeviceAction(path: "reactivate", message: "This Mac was reactivated.")
    }

    func transferCurrentDevice() async {
        guard deviceState.canTransferCurrentDevice else {
            statusMessage = "This Mac is not active, so there is no active seat to transfer."
            return
        }
        await performDeviceAction(path: "transfer", message: "This Mac was deactivated and the license seat is available for transfer.")
    }

    private func performDeviceAction(path: String, message: String) async {
        let trimmedKey = savedLicenseKey().trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmedKey.isEmpty else {
            statusMessage = "Activate with a license key before managing this device."
            return
        }
        await performServerAction {
            let response = try await licenseClient.deviceAction(
                path: path,
                licenseKey: trimmedKey,
                installID: installID.isEmpty ? ensureInstallID() : installID,
                deviceID: deviceState.currentDevice?.deviceID,
                device: deviceRegistration()
            )
            apply(response, message: message)
        }
    }

    private func performServerAction(_ operation: () async throws -> Void) async {
        isLoading = true
        statusMessage = ""
        defer { isLoading = false }
        do {
            try await operation()
        } catch {
            markVerificationFailure(error)
        }
    }

    private func apply(_ response: MacLicenseServerResponse, message: String) {
        MobileServerLicenseGrantStore.save(response.grant, defaults: defaults)
        var next = response.snapshot.licenseSnapshot
        let now = Date()
        next.lastVerifiedAt = now
        next.lastVerificationFailedAt = nil
        next.cachedAt = now
        save(next, now: now)
        let nextDeviceState = response.deviceState.withCurrentInstallID(installID)
        deviceState = nextDeviceState
        MobileLicenseDeviceStateStore.save(nextDeviceState, defaults: defaults)
        statusMessage = message
    }

    private func markVerificationFailure(_ error: Error) {
        var next = snapshot
        let now = Date()
        next.lastVerificationFailedAt = now
        next.cachedAt = now
        save(next, now: now)
        statusMessage = error.localizedDescription
    }

    private func save(_ next: MobileLicenseSnapshot, now: Date = Date()) {
        snapshot = next
        MobileLicenseSnapshotStore.save(next, defaults: defaults)
        refreshDecision(now: now)
    }

    private func ensureInstallID() -> String {
        if let existing = try? credentialStore.readToken(account: macLicenseInstallAccount),
           !existing.isEmpty {
            return existing
        }
        let generated = UUID().uuidString.lowercased()
        try? credentialStore.saveToken(generated, account: macLicenseInstallAccount)
        return generated
    }

    private func deviceRegistration() -> MobileLicenseDeviceRegistration {
        MobileLicenseDeviceRegistration(
            installID: installID.isEmpty ? ensureInstallID() : installID,
            displayName: Host.current().localizedName ?? "Mac",
            platform: "macOS",
            architecture: "arm64",
            appVersion: Self.appVersion
        )
    }

    private static var appVersion: String? {
        let info = Bundle.main.infoDictionary ?? [:]
        let shortVersion = info["CFBundleShortVersionString"] as? String
        let build = info["CFBundleVersion"] as? String
        switch (shortVersion, build) {
        case let (short?, build?) where !short.isEmpty && !build.isEmpty:
            return "\(short) (\(build))"
        case let (short?, _) where !short.isEmpty:
            return short
        case let (_, build?) where !build.isEmpty:
            return build
        default:
            return nil
        }
    }

    private static func initialSnapshot(defaults: UserDefaults) -> MobileLicenseSnapshot {
        if let grant = MobileServerLicenseGrantStore.load(defaults: defaults), grant.expiresAt > Date() {
            return MobileLicenseSnapshot(
                trialStartDate: grant.trialStartDate,
                transactions: grant.transactions,
                lastVerifiedAt: grant.issuedAt,
                lastVerificationFailedAt: nil,
                cachedAt: grant.issuedAt
            )
        }
        return MobileLicenseSnapshotStore.load(defaults: defaults)
    }
}

private final class MacLicenseValidationClient {
    private let endpoint: URL
    private let session: URLSession
    private let encoder: JSONEncoder
    private let decoder: JSONDecoder

    init(endpoint: URL, session: URLSession = .shared) {
        self.endpoint = endpoint
        self.session = session
        self.encoder = JSONEncoder()
        self.encoder.dateEncodingStrategy = .iso8601
        self.decoder = JSONDecoder()
        self.decoder.dateDecodingStrategy = .iso8601
    }

    func activate(
        licenseKey: String,
        email: String?,
        device: MobileLicenseDeviceRegistration
    ) async throws -> MacLicenseServerResponse {
        try await post(
            MacLicenseActivationRequest(licenseKey: licenseKey, email: email, device: device),
            path: "activate"
        )
    }

    func deviceAction(
        path: String,
        licenseKey: String,
        installID: String,
        deviceID: String?,
        device: MobileLicenseDeviceRegistration
    ) async throws -> MacLicenseServerResponse {
        try await post(
            MacLicenseDeviceActionRequest(
                licenseKey: licenseKey,
                installID: installID,
                deviceID: deviceID,
                device: device
            ),
            path: path
        )
    }

    private func post<T: Encodable>(_ payload: T, path: String) async throws -> MacLicenseServerResponse {
        let url = endpoint.appendingPathComponent("v1/devices/\(path)")
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try encoder.encode(payload)
        let (data, response) = try await session.data(for: request)
        guard let httpResponse = response as? HTTPURLResponse else {
            throw MacLicenseError.invalidResponse
        }
        guard (200..<300).contains(httpResponse.statusCode) else {
            let message = (try? decoder.decode(LicenseServerErrorEnvelope.self, from: data).error) ?? "License request failed."
            throw MacLicenseError.server(message)
        }
        return try decoder.decode(MacLicenseServerResponse.self, from: data)
    }
}

private struct LicenseServerErrorEnvelope: Decodable {
    var error: String
}

private enum MacLicenseError: LocalizedError {
    case invalidResponse
    case server(String)

    var errorDescription: String? {
        switch self {
        case .invalidResponse:
            return "License validation returned an invalid response."
        case .server(let message):
            return message
        }
    }
}

private func normalizedOptional(_ value: String?) -> String? {
    let trimmed = value?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    return trimmed.isEmpty ? nil : trimmed
}
#endif
