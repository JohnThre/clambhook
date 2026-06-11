import ClambhookShared
import Foundation

#if os(iOS)
import CryptoKit
import DeviceCheck
import StoreKit

private let licenseInstallAccount = "license-install-id"
private let appAttestKeyAccount = "app-attest-key-id"

struct LicenseServerChallengeRequest: Encodable {
    var purpose: String
    var installID: String
    var keyID: String

    enum CodingKeys: String, CodingKey {
        case purpose
        case installID = "install_id"
        case keyID = "key_id"
    }
}

struct LicenseServerChallengeResponse: Decodable {
    var challengeID: String
    var challenge: String
    var expiresAt: Date

    enum CodingKeys: String, CodingKey {
        case challengeID = "challenge_id"
        case challenge
        case expiresAt = "expires_at"
    }
}

struct LicenseServerAttestRequest: Encodable {
    var challengeID: String
    var installID: String
    var keyID: String
    var attestationObject: String

    enum CodingKeys: String, CodingKey {
        case challengeID = "challenge_id"
        case installID = "install_id"
        case keyID = "key_id"
        case attestationObject = "attestation_object"
    }
}

struct LicenseServerValidateRequest: Encodable {
    var keyID: String
    var clientData: String
    var assertion: String
    var transactions: [String]

    enum CodingKeys: String, CodingKey {
        case keyID = "key_id"
        case clientData = "client_data"
        case assertion
        case transactions
    }
}

struct LicenseServerAssertionClientData: Codable {
    var challengeID: String
    var challenge: String
    var installID: String
    var keyID: String
    var transactions: [String]

    enum CodingKeys: String, CodingKey {
        case challengeID = "challenge_id"
        case challenge
        case installID = "install_id"
        case keyID = "key_id"
        case transactions
    }
}

@MainActor
final class LicenseValidationClient {
    private let endpoint: URL
    private let defaults: UserDefaults
    private let credentialStore: CredentialStoring
    private let session: URLSession
    private let encoder = JSONEncoder()
    private let decoder = JSONDecoder()

    init(
        endpoint: URL,
        defaults: UserDefaults,
        credentialStore: CredentialStoring,
        session: URLSession = .shared
    ) {
        self.endpoint = endpoint
        self.defaults = defaults
        self.credentialStore = credentialStore
        self.session = session
        encoder.dateEncodingStrategy = .iso8601
        decoder.dateDecodingStrategy = .iso8601
    }

    func purchaseOptions() throws -> Set<Product.PurchaseOption> {
        [.appAccountToken(try installUUID())]
    }

    func refreshGrant(transactionJWS: [String]) async throws -> MobileServerLicenseGrantResponse {
        guard DCAppAttestService.shared.isSupported else {
            throw LicenseValidationError.appAttestUnsupported
        }
        let installID = try installUUID().uuidString.lowercased()
        let keyID = try await resolvedKeyID()
        if MobileServerLicenseGrantStore.load(defaults: defaults) == nil {
            let response = try await attest(installID: installID, keyID: keyID)
            guard !transactionJWS.isEmpty else {
                return response
            }
            return try await validate(installID: installID, keyID: keyID, transactionJWS: transactionJWS)
        }
        return try await validate(installID: installID, keyID: keyID, transactionJWS: transactionJWS)
    }

    private func attest(installID: String, keyID: String) async throws -> MobileServerLicenseGrantResponse {
        let challenge = try await challenge(purpose: "attest", installID: installID, keyID: keyID)
        let hash = Data(SHA256.hash(data: Data(challenge.challenge.utf8)))
        let attestation = try await DCAppAttestService.shared.attestKey(keyID, clientDataHash: hash)
        let request = LicenseServerAttestRequest(
            challengeID: challenge.challengeID,
            installID: installID,
            keyID: keyID,
            attestationObject: attestation.base64EncodedString()
        )
        return try await post("attest", request)
    }

    private func validate(installID: String, keyID: String, transactionJWS: [String]) async throws -> MobileServerLicenseGrantResponse {
        let challenge = try await challenge(purpose: "validate", installID: installID, keyID: keyID)
        let clientData = LicenseServerAssertionClientData(
            challengeID: challenge.challengeID,
            challenge: challenge.challenge,
            installID: installID,
            keyID: keyID,
            transactions: transactionJWS
        )
        let clientDataBytes = try encoder.encode(clientData)
        let hash = Data(SHA256.hash(data: clientDataBytes))
        let assertion = try await DCAppAttestService.shared.generateAssertion(keyID, clientDataHash: hash)
        let request = LicenseServerValidateRequest(
            keyID: keyID,
            clientData: clientDataBytes.base64EncodedString(),
            assertion: assertion.base64EncodedString(),
            transactions: transactionJWS
        )
        return try await post("validate", request)
    }

    private func challenge(purpose: String, installID: String, keyID: String) async throws -> LicenseServerChallengeResponse {
        let request = LicenseServerChallengeRequest(purpose: purpose, installID: installID, keyID: keyID)
        return try await post("challenge", request)
    }

    private func post<Request: Encodable, Response: Decodable>(_ path: String, _ body: Request) async throws -> Response {
        var request = URLRequest(url: endpoint.appendingPathComponent("v1/license/\(path)"))
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try encoder.encode(body)
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else {
            throw LicenseValidationError.invalidResponse
        }
        guard 200..<300 ~= http.statusCode else {
            let message = (try? decoder.decode(LicenseServerErrorEnvelope.self, from: data).error) ?? "License validation failed."
            throw LicenseValidationError.server(message)
        }
        return try decoder.decode(Response.self, from: data)
    }

    private func resolvedKeyID() async throws -> String {
        if let existing = try credentialStore.readToken(account: appAttestKeyAccount), !existing.isEmpty {
            return existing
        }
        let keyID = try await DCAppAttestService.shared.generateKey()
        try credentialStore.saveToken(keyID, account: appAttestKeyAccount)
        return keyID
    }

    private func installUUID() throws -> UUID {
        if let existing = try credentialStore.readToken(account: licenseInstallAccount),
           let uuid = UUID(uuidString: existing) {
            return uuid
        }
        let uuid = UUID()
        try credentialStore.saveToken(uuid.uuidString.lowercased(), account: licenseInstallAccount)
        return uuid
    }
}

struct LicenseServerErrorEnvelope: Decodable {
    var error: String
}

enum LicenseValidationError: LocalizedError {
    case appAttestUnsupported
    case invalidResponse
    case server(String)

    var errorDescription: String? {
        switch self {
        case .appAttestUnsupported:
            return "App Attest is not available on this device."
        case .invalidResponse:
            return "License validation returned an invalid response."
        case .server(let message):
            return message
        }
    }
}
#endif
