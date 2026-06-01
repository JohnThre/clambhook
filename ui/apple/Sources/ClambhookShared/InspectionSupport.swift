import Combine
import Foundation
#if canImport(LocalAuthentication)
import LocalAuthentication
#endif

public enum InspectionFilterKind: String, CaseIterable, Sendable {
    case all
    case active
    case pinned
    case proxy
    case direct
    case block
}

public extension TrafficSnapshotPayload {
    func inspectionConnections(
        filter: InspectionFilterKind,
        query: String = "",
        pinnedIDs: Set<String> = []
    ) -> [TrafficConnectionPayload] {
        let normalizedQuery = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let rows = connections.filter { connection in
            switch filter {
            case .all:
                break
            case .active:
                guard connection.state.lowercased() == "active" else { return false }
            case .pinned:
                guard pinnedIDs.contains(connection.connID) else { return false }
            case .proxy, .direct, .block:
                guard connection.actionFamily == filter.rawValue else { return false }
            }
            return normalizedQuery.isEmpty || connection.matchesInspectionQuery(normalizedQuery)
        }
        return rows.stablyPinnedFirst(pinnedIDs: pinnedIDs)
    }
}

public extension TrafficConnectionPayload {
    func matchesInspectionQuery(_ query: String) -> Bool {
        let normalized = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !normalized.isEmpty else { return true }
        let fields = [
            target,
            targetHost,
            targetPort,
            ruleName,
            ruleAction,
            chainName,
            application,
            network,
            listener.protocol,
            listener.addr,
            clientAddr,
            displayVisibility,
        ]
        if fields.contains(where: { $0.lowercased().contains(normalized) }) {
            return true
        }
        if hops.contains(where: {
            [$0.name, $0.protocol, $0.address, $0.state, $0.error]
                .contains(where: { $0.lowercased().contains(normalized) })
        }) {
            return true
        }
        return timeline.contains(where: {
            [$0.type, $0.title, $0.detail]
                .contains(where: { $0.lowercased().contains(normalized) })
        })
    }
}

public struct InspectionExportPayload: Codable, Equatable, Sendable {
    public var version: Int
    public var generatedAt: Date
    public var scope: String
    public var note: String
    public var summary: InspectionTrafficSummaryPayload
    public var connections: [InspectionConnectionPayload]
    public var logs: [String]

    enum CodingKeys: String, CodingKey {
        case version
        case generatedAt = "generated_at"
        case scope
        case note
        case summary
        case connections
        case logs
    }
}

public struct InspectionTrafficSummaryPayload: Codable, Equatable, Sendable {
    public var activeConnections: Int
    public var rxBps: Double
    public var txBps: Double
    public var rxTotal: UInt64
    public var txTotal: UInt64
    public var historyLimit: Int
    public var historyPath: String
    public var historyPersisted: Bool
    public var persistError: String

    enum CodingKeys: String, CodingKey {
        case activeConnections = "active_connections"
        case rxBps = "rx_bps"
        case txBps = "tx_bps"
        case rxTotal = "rx_total"
        case txTotal = "tx_total"
        case historyLimit = "history_limit"
        case historyPath = "history_path"
        case historyPersisted = "history_persisted"
        case persistError = "persist_error"
    }
}

public struct InspectionConnectionPayload: Codable, Equatable, Sendable {
    public var connID: String
    public var state: String
    public var startTsNs: Int64
    public var updatedTsNs: Int64
    public var endTsNs: Int64
    public var listener: InspectionListenerPayload
    public var clientAddr: String
    public var chainName: String
    public var ruleName: String
    public var ruleAction: String
    public var decisionNs: Int64
    public var target: String
    public var targetHost: String
    public var targetPort: String
    public var network: String
    public var application: String
    public var hops: [InspectionHopPayload]
    public var timeline: [TrafficTimelinePayload]
    public var visibility: TrafficVisibilityPayload?
    public var geo: LocationPayload
    public var geoError: String
    public var totalDialNs: Int64
    public var rxBps: Double
    public var txBps: Double
    public var rxTotal: UInt64
    public var txTotal: UInt64
    public var durationNs: Int64
    public var closeReason: String

    enum CodingKeys: String, CodingKey {
        case connID = "conn_id"
        case state
        case startTsNs = "start_ts_ns"
        case updatedTsNs = "updated_ts_ns"
        case endTsNs = "end_ts_ns"
        case listener
        case clientAddr = "client_addr"
        case chainName = "chain_name"
        case ruleName = "rule_name"
        case ruleAction = "rule_action"
        case decisionNs = "decision_ns"
        case target
        case targetHost = "target_host"
        case targetPort = "target_port"
        case network
        case application
        case hops
        case timeline
        case visibility
        case geo
        case geoError = "geo_error"
        case totalDialNs = "total_dial_ns"
        case rxBps = "rx_bps"
        case txBps = "tx_bps"
        case rxTotal = "rx_total"
        case txTotal = "tx_total"
        case durationNs = "duration_ns"
        case closeReason = "close_reason"
    }
}

public struct InspectionListenerPayload: Codable, Equatable, Sendable {
    public var `protocol`: String
    public var addr: String
}

public struct InspectionHopPayload: Codable, Equatable, Sendable {
    public var index: Int
    public var name: String
    public var `protocol`: String
    public var address: String
    public var state: String
    public var elapsedNs: Int64
    public var error: String

    enum CodingKeys: String, CodingKey {
        case index
        case name
        case `protocol`
        case address
        case state
        case elapsedNs = "elapsed_ns"
        case error
    }
}

public enum InspectionExportBuilder {
    public static let note = "Metadata-only export. Payload bodies, request/response headers, query strings, local client addresses, listener addresses, hop addresses, paths, and likely secrets are redacted."

    public static func payload(
        scope: String,
        traffic: TrafficSnapshotPayload,
        connections: [TrafficConnectionPayload],
        logs: [String] = [],
        generatedAt: Date = Date()
    ) -> InspectionExportPayload {
        InspectionExportPayload(
            version: 1,
            generatedAt: generatedAt,
            scope: scope,
            note: note,
            summary: InspectionTrafficSummaryPayload(
                activeConnections: traffic.summary.activeConnections,
                rxBps: traffic.summary.rxBps,
                txBps: traffic.summary.txBps,
                rxTotal: traffic.summary.rxTotal,
                txTotal: traffic.summary.txTotal,
                historyLimit: traffic.summary.historyLimit,
                historyPath: traffic.summary.historyPath.isEmpty ? "" : "[redacted-path]",
                historyPersisted: traffic.summary.historyPersisted,
                persistError: redactText(traffic.summary.persistError)
            ),
            connections: connections.map(redactedConnection),
            logs: logs.map(redactText)
        )
    }

    public static func jsonData(
        scope: String,
        traffic: TrafficSnapshotPayload,
        connections: [TrafficConnectionPayload],
        logs: [String] = [],
        generatedAt: Date = Date()
    ) throws -> Data {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        encoder.dateEncodingStrategy = .iso8601
        return try encoder.encode(payload(
            scope: scope,
            traffic: traffic,
            connections: connections,
            logs: logs,
            generatedAt: generatedAt
        ))
    }

    public static func jsonString(
        scope: String,
        traffic: TrafficSnapshotPayload,
        connections: [TrafficConnectionPayload],
        logs: [String] = [],
        generatedAt: Date = Date()
    ) -> String {
        guard let data = try? jsonData(
            scope: scope,
            traffic: traffic,
            connections: connections,
            logs: logs,
            generatedAt: generatedAt
        ) else {
            return "{}"
        }
        return String(data: data, encoding: .utf8) ?? "{}"
    }

    public static func redactText(_ value: String) -> String {
        var out = value
        out = replace(out, pattern: #"(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+"#, template: "$1[redacted]")
        out = replace(out, pattern: #"(?i)((?:token|password|passwd|secret|private[_ -]?key|preshared[_ -]?key|uuid)\s*[:=]\s*)[^\s,;]+"#, template: "$1[redacted]")
        out = replace(out, pattern: #"/[^\s]+?\.(?:toml|json|pem|key|conf|log)\b"#, template: "[redacted-path]")
        out = replace(out, pattern: #"\b(?:\d{1,3}\.){3}\d{1,3}(?::\d{1,5})?\b"#, template: "[redacted-address]")
        out = replace(out, pattern: #"\[[0-9a-fA-F:]+\](?::\d{1,5})?"#, template: "[redacted-address]")
        return out
    }

    private static func redactedConnection(_ connection: TrafficConnectionPayload) -> InspectionConnectionPayload {
        InspectionConnectionPayload(
            connID: connection.connID,
            state: connection.state,
            startTsNs: connection.startTsNs,
            updatedTsNs: connection.updatedTsNs,
            endTsNs: connection.endTsNs,
            listener: InspectionListenerPayload(
                protocol: connection.listener.protocol,
                addr: redactAddress(connection.listener.addr)
            ),
            clientAddr: redactAddress(connection.clientAddr),
            chainName: connection.chainName,
            ruleName: connection.ruleName,
            ruleAction: connection.ruleAction,
            decisionNs: connection.decisionNs,
            target: connection.target,
            targetHost: connection.targetHost,
            targetPort: connection.targetPort,
            network: connection.network,
            application: connection.application,
            hops: connection.hops.map { hop in
                InspectionHopPayload(
                    index: hop.index,
                    name: hop.name,
                    protocol: hop.protocol,
                    address: redactAddress(hop.address),
                    state: hop.state,
                    elapsedNs: hop.elapsedNs,
                    error: redactText(hop.error)
                )
            },
            timeline: connection.timeline.map { item in
                TrafficTimelinePayload(
                    tsNs: item.tsNs,
                    type: item.type,
                    title: item.title,
                    detail: redactText(item.detail)
                )
            },
            visibility: connection.visibility,
            geo: connection.geo,
            geoError: redactText(connection.geoError),
            totalDialNs: connection.totalDialNs,
            rxBps: connection.rxBps,
            txBps: connection.txBps,
            rxTotal: connection.rxTotal,
            txTotal: connection.txTotal,
            durationNs: connection.durationNs,
            closeReason: connection.closeReason
        )
    }

    private static func redactAddress(_ value: String) -> String {
        value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "" : "[redacted-address]"
    }

    private static func replace(_ value: String, pattern: String, template: String) -> String {
        guard let regex = try? NSRegularExpression(pattern: pattern) else {
            return value
        }
        let range = NSRange(value.startIndex..<value.endIndex, in: value)
        return regex.stringByReplacingMatches(in: value, range: range, withTemplate: template)
    }
}

public struct BiometricAuthStatus: Equatable, Sendable {
    public var isAvailable: Bool
    public var label: String
    public var reason: String

    public init(isAvailable: Bool, label: String, reason: String = "") {
        self.isAvailable = isAvailable
        self.label = label
        self.reason = reason
    }
}

public protocol BiometricAuthenticating {
    func status() -> BiometricAuthStatus
    func authenticate(reason: String) async throws
}

public enum BiometricAuthError: Error, LocalizedError, Equatable {
    case unavailable(String)
    case failed(String)

    public var errorDescription: String? {
        switch self {
        case .unavailable(let reason), .failed(let reason):
            return reason
        }
    }
}

#if canImport(LocalAuthentication)
public final class SystemBiometricAuthenticator: BiometricAuthenticating {
    public init() {}

    public func status() -> BiometricAuthStatus {
        let context = LAContext()
        var error: NSError?
        let available = context.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: &error)
        return BiometricAuthStatus(
            isAvailable: available,
            label: label(for: context.biometryType),
            reason: available ? "" : (error?.localizedDescription ?? "Biometric authentication is unavailable.")
        )
    }

    public func authenticate(reason: String) async throws {
        let context = LAContext()
        context.localizedFallbackTitle = ""
        var error: NSError?
        guard context.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: &error) else {
            throw BiometricAuthError.unavailable(error?.localizedDescription ?? "Biometric authentication is unavailable.")
        }
        try await withCheckedThrowingContinuation { continuation in
            context.evaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, localizedReason: reason) { success, error in
                if success {
                    continuation.resume()
                } else {
                    continuation.resume(throwing: BiometricAuthError.failed(error?.localizedDescription ?? "Authentication failed."))
                }
            }
        }
    }

    private func label(for biometryType: LABiometryType) -> String {
        switch biometryType {
        case .faceID:
            return "Face ID"
        case .touchID:
            return "Touch ID"
        default:
            return "Biometric Lock"
        }
    }
}
#else
public final class SystemBiometricAuthenticator: BiometricAuthenticating {
    public init() {}

    public func status() -> BiometricAuthStatus {
        BiometricAuthStatus(isAvailable: false, label: "Biometric Lock", reason: "Biometric authentication is unavailable on this platform.")
    }

    public func authenticate(reason: String) async throws {
        throw BiometricAuthError.unavailable("Biometric authentication is unavailable on this platform.")
    }
}
#endif

@MainActor
public final class InspectionLockState: ObservableObject {
    @Published public private(set) var isLocked = false
    @Published public private(set) var isAuthenticating = false
    @Published public private(set) var status = BiometricAuthStatus(isAvailable: false, label: "Biometric Lock")
    @Published public private(set) var message = ""

    private let authenticator: BiometricAuthenticating

    public init(authenticator: BiometricAuthenticating = SystemBiometricAuthenticator()) {
        self.authenticator = authenticator
        refreshAvailability()
    }

    public func refreshAvailability() {
        status = authenticator.status()
        if !status.isAvailable {
            isLocked = false
            message = status.reason
        }
    }

    public func lockIfNeeded(enabled: Bool) {
        refreshAvailability()
        guard enabled, status.isAvailable else {
            isLocked = false
            return
        }
        isLocked = true
    }

    public func clearLock() {
        isLocked = false
        isAuthenticating = false
        message = ""
    }

    public func authenticateIfNeeded(enabled: Bool, reason: String = "Unlock ClambHook inspection details.") async {
        guard enabled, isLocked, status.isAvailable, !isAuthenticating else {
            return
        }
        isAuthenticating = true
        message = ""
        do {
            try await authenticator.authenticate(reason: reason)
            isLocked = false
        } catch {
            message = error.localizedDescription
        }
        isAuthenticating = false
    }
}

private extension Array where Element == TrafficConnectionPayload {
    func stablyPinnedFirst(pinnedIDs: Set<String>) -> [TrafficConnectionPayload] {
        enumerated()
            .sorted { lhs, rhs in
                let lhsPinned = pinnedIDs.contains(lhs.element.connID)
                let rhsPinned = pinnedIDs.contains(rhs.element.connID)
                if lhsPinned != rhsPinned {
                    return lhsPinned
                }
                return lhs.offset < rhs.offset
            }
            .map(\.element)
    }
}
