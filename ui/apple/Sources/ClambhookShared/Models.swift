import Foundation

public struct StatusPayload: Codable, Equatable, Sendable {
    public var running: Bool
    public var profile: String
    public var listeners: [ListenerStatusPayload]

    public init(running: Bool = false, profile: String = "", listeners: [ListenerStatusPayload] = []) {
        self.running = running
        self.profile = profile
        self.listeners = listeners
    }
}

public struct ListenerStatusPayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String { "\(self.protocol)-\(addr)" }
    public var `protocol`: String
    public var addr: String
    public var activeConns: Int

    enum CodingKeys: String, CodingKey {
        case `protocol`
        case addr
        case activeConns = "active_conns"
    }

    public init(protocol: String, addr: String, activeConns: Int) {
        self.protocol = `protocol`
        self.addr = addr
        self.activeConns = activeConns
    }
}

public struct ProfilesPayload: Codable, Equatable, Sendable {
    public var profiles: [String]
    public var active: String

    public init(profiles: [String] = [], active: String = "") {
        self.profiles = profiles
        self.active = active
    }
}

public struct ServersPayload: Codable, Equatable, Sendable {
    public var profile: String
    public var chains: [ChainPayload]

    public init(profile: String = "", chains: [ChainPayload] = []) {
        self.profile = profile
        self.chains = chains
    }
}

public struct RulesPayload: Codable, Equatable, Sendable {
    public var profile: String
    public var rules: [RulePayload]

    public init(profile: String = "", rules: [RulePayload] = []) {
        self.profile = profile
        self.rules = rules
    }
}

public struct RulePayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String { name }
    public var name: String
    public var action: String
    public var domains: [String]
    public var domainSuffixes: [String]
    public var domainKeywords: [String]
    public var cidrs: [String]
    public var ports: [Int]
    public var networks: [String]

    enum CodingKeys: String, CodingKey {
        case name
        case action
        case domains
        case domainSuffixes = "domain_suffixes"
        case domainKeywords = "domain_keywords"
        case cidrs
        case ports
        case networks
    }

    public init(name: String = "", action: String = "", domains: [String] = [], domainSuffixes: [String] = [], domainKeywords: [String] = [], cidrs: [String] = [], ports: [Int] = [], networks: [String] = []) {
        self.name = name
        self.action = action
        self.domains = domains
        self.domainSuffixes = domainSuffixes
        self.domainKeywords = domainKeywords
        self.cidrs = cidrs
        self.ports = ports
        self.networks = networks
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.name = try container.decodeIfPresent(String.self, forKey: .name) ?? ""
        self.action = try container.decodeIfPresent(String.self, forKey: .action) ?? ""
        self.domains = try container.decodeIfPresent([String].self, forKey: .domains) ?? []
        self.domainSuffixes = try container.decodeIfPresent([String].self, forKey: .domainSuffixes) ?? []
        self.domainKeywords = try container.decodeIfPresent([String].self, forKey: .domainKeywords) ?? []
        self.cidrs = try container.decodeIfPresent([String].self, forKey: .cidrs) ?? []
        self.ports = try container.decodeIfPresent([Int].self, forKey: .ports) ?? []
        self.networks = try container.decodeIfPresent([String].self, forKey: .networks) ?? []
    }
}

public struct ChainPayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String { name }
    public var name: String
    public var servers: [ServerPayload]

    public init(name: String, servers: [ServerPayload]) {
        self.name = name
        self.servers = servers
    }
}

public struct ServerPayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String { "\(name)-\(address)-\(self.protocol)" }
    public var name: String
    public var address: String
    public var `protocol`: String
    public var geo: LocationPayload
    public var geoError: String?

    enum CodingKeys: String, CodingKey {
        case name
        case address
        case `protocol`
        case geo
        case geoError = "geo_error"
    }

    public init(name: String, address: String, protocol: String, geo: LocationPayload = LocationPayload(), geoError: String? = nil) {
        self.name = name
        self.address = address
        self.protocol = `protocol`
        self.geo = geo
        self.geoError = geoError
    }
}

public struct LocationPayload: Codable, Equatable, Sendable {
    public var country: String
    public var countryCode: String
    public var city: String
    public var latitude: Double
    public var longitude: Double

    enum CodingKeys: String, CodingKey {
        case country
        case countryCode = "country_code"
        case city
        case latitude
        case longitude
    }

    public init(country: String = "", countryCode: String = "", city: String = "", latitude: Double = 0, longitude: Double = 0) {
        self.country = country
        self.countryCode = countryCode
        self.city = city
        self.latitude = latitude
        self.longitude = longitude
    }
}

public struct DaemonEvent: Decodable, Equatable, Sendable {
    public var shardID: UInt64
    public var lamport: UInt64
    public var tsNs: Int64
    public var type: String
    public var data: [String: EventValue]

    enum CodingKeys: String, CodingKey {
        case shardID = "shard_id"
        case lamport
        case tsNs = "ts_ns"
        case type
        case data
    }

    public init(shardID: UInt64, lamport: UInt64, tsNs: Int64, type: String, data: [String: Any] = [:]) {
        self.shardID = shardID
        self.lamport = lamport
        self.tsNs = tsNs
        self.type = type
        self.data = data.mapValues(EventValue.init(any:))
    }
}

public enum EventValue: Codable, Equatable, Sendable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case null

    public init(any value: Any) {
        switch value {
        case let value as String:
            self = .string(value)
        case let value as Double:
            self = .number(value)
        case let value as Float:
            self = .number(Double(value))
        case let value as Int:
            self = .number(Double(value))
        case let value as UInt64:
            self = .number(Double(value))
        case let value as Bool:
            self = .bool(value)
        default:
            self = .null
        }
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let string = try? container.decode(String.self) {
            self = .string(string)
        } else if let number = try? container.decode(Double.self) {
            self = .number(number)
        } else if let bool = try? container.decode(Bool.self) {
            self = .bool(bool)
        } else {
            self = .null
        }
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .string(let string):
            try container.encode(string)
        case .number(let number):
            try container.encode(number)
        case .bool(let bool):
            try container.encode(bool)
        case .null:
            try container.encodeNil()
        }
    }

    public var stringValue: String? {
        if case .string(let value) = self {
            return value
        }
        return nil
    }

    public var doubleValue: Double? {
        switch self {
        case .number(let value):
            return value
        case .string(let value):
            return Double(value)
        default:
            return nil
        }
    }
}

public struct BandwidthSample: Codable, Equatable, Sendable {
    public var rxBps: Double
    public var txBps: Double

    public init(rxBps: Double = 0, txBps: Double = 0) {
        self.rxBps = rxBps
        self.txBps = txBps
    }
}

public struct TrafficSnapshotPayload: Codable, Equatable, Sendable {
    public var updatedTsNs: Int64
    public var summary: TrafficSummaryPayload
    public var connections: [TrafficConnectionPayload]

    enum CodingKeys: String, CodingKey {
        case updatedTsNs = "updated_ts_ns"
        case summary
        case connections
    }

    public init(updatedTsNs: Int64 = 0, summary: TrafficSummaryPayload = TrafficSummaryPayload(), connections: [TrafficConnectionPayload] = []) {
        self.updatedTsNs = updatedTsNs
        self.summary = summary
        self.connections = connections
    }
}

public struct TrafficSummaryPayload: Codable, Equatable, Sendable {
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

    public init(activeConnections: Int = 0, rxBps: Double = 0, txBps: Double = 0, rxTotal: UInt64 = 0, txTotal: UInt64 = 0, historyLimit: Int = 0, historyPath: String = "", historyPersisted: Bool = false, persistError: String = "") {
        self.activeConnections = activeConnections
        self.rxBps = rxBps
        self.txBps = txBps
        self.rxTotal = rxTotal
        self.txTotal = txTotal
        self.historyLimit = historyLimit
        self.historyPath = historyPath
        self.historyPersisted = historyPersisted
        self.persistError = persistError
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.activeConnections = try container.decodeIfPresent(Int.self, forKey: .activeConnections) ?? 0
        self.rxBps = try container.decodeIfPresent(Double.self, forKey: .rxBps) ?? 0
        self.txBps = try container.decodeIfPresent(Double.self, forKey: .txBps) ?? 0
        self.rxTotal = try container.decodeIfPresent(UInt64.self, forKey: .rxTotal) ?? 0
        self.txTotal = try container.decodeIfPresent(UInt64.self, forKey: .txTotal) ?? 0
        self.historyLimit = try container.decodeIfPresent(Int.self, forKey: .historyLimit) ?? 0
        self.historyPath = try container.decodeIfPresent(String.self, forKey: .historyPath) ?? ""
        self.historyPersisted = try container.decodeIfPresent(Bool.self, forKey: .historyPersisted) ?? false
        self.persistError = try container.decodeIfPresent(String.self, forKey: .persistError) ?? ""
    }
}

public struct TrafficConnectionPayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String { connID }
    public var connID: String
    public var state: String
    public var startTsNs: Int64
    public var updatedTsNs: Int64
    public var endTsNs: Int64
    public var listener: TrafficListenerPayload
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
    public var hops: [TrafficHopPayload]
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

    public init(connID: String = "", state: String = "", startTsNs: Int64 = 0, updatedTsNs: Int64 = 0, endTsNs: Int64 = 0, listener: TrafficListenerPayload = TrafficListenerPayload(), clientAddr: String = "", chainName: String = "", ruleName: String = "", ruleAction: String = "", decisionNs: Int64 = 0, target: String = "", targetHost: String = "", targetPort: String = "", network: String = "", application: String = "", hops: [TrafficHopPayload] = [], timeline: [TrafficTimelinePayload] = [], visibility: TrafficVisibilityPayload? = nil, geo: LocationPayload = LocationPayload(), geoError: String = "", totalDialNs: Int64 = 0, rxBps: Double = 0, txBps: Double = 0, rxTotal: UInt64 = 0, txTotal: UInt64 = 0, durationNs: Int64 = 0, closeReason: String = "") {
        self.connID = connID
        self.state = state
        self.startTsNs = startTsNs
        self.updatedTsNs = updatedTsNs
        self.endTsNs = endTsNs
        self.listener = listener
        self.clientAddr = clientAddr
        self.chainName = chainName
        self.ruleName = ruleName
        self.ruleAction = ruleAction
        self.decisionNs = decisionNs
        self.target = target
        self.targetHost = targetHost
        self.targetPort = targetPort
        self.network = network
        self.application = application
        self.hops = hops
        self.timeline = timeline
        self.visibility = visibility
        self.geo = geo
        self.geoError = geoError
        self.totalDialNs = totalDialNs
        self.rxBps = rxBps
        self.txBps = txBps
        self.rxTotal = rxTotal
        self.txTotal = txTotal
        self.durationNs = durationNs
        self.closeReason = closeReason
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.connID = try container.decodeIfPresent(String.self, forKey: .connID) ?? ""
        self.state = try container.decodeIfPresent(String.self, forKey: .state) ?? ""
        self.startTsNs = try container.decodeIfPresent(Int64.self, forKey: .startTsNs) ?? 0
        self.updatedTsNs = try container.decodeIfPresent(Int64.self, forKey: .updatedTsNs) ?? 0
        self.endTsNs = try container.decodeIfPresent(Int64.self, forKey: .endTsNs) ?? 0
        self.listener = try container.decodeIfPresent(TrafficListenerPayload.self, forKey: .listener) ?? TrafficListenerPayload()
        self.clientAddr = try container.decodeIfPresent(String.self, forKey: .clientAddr) ?? ""
        self.chainName = try container.decodeIfPresent(String.self, forKey: .chainName) ?? ""
        self.ruleName = try container.decodeIfPresent(String.self, forKey: .ruleName) ?? ""
        self.ruleAction = try container.decodeIfPresent(String.self, forKey: .ruleAction) ?? ""
        self.decisionNs = try container.decodeIfPresent(Int64.self, forKey: .decisionNs) ?? 0
        self.target = try container.decodeIfPresent(String.self, forKey: .target) ?? ""
        self.targetHost = try container.decodeIfPresent(String.self, forKey: .targetHost) ?? ""
        self.targetPort = try container.decodeIfPresent(String.self, forKey: .targetPort) ?? ""
        self.network = try container.decodeIfPresent(String.self, forKey: .network) ?? ""
        self.application = try container.decodeIfPresent(String.self, forKey: .application) ?? ""
        self.hops = try container.decodeIfPresent([TrafficHopPayload].self, forKey: .hops) ?? []
        self.timeline = try container.decodeIfPresent([TrafficTimelinePayload].self, forKey: .timeline) ?? []
        self.visibility = try container.decodeIfPresent(TrafficVisibilityPayload.self, forKey: .visibility)
        self.geo = try container.decodeIfPresent(LocationPayload.self, forKey: .geo) ?? LocationPayload()
        self.geoError = try container.decodeIfPresent(String.self, forKey: .geoError) ?? ""
        self.totalDialNs = try container.decodeIfPresent(Int64.self, forKey: .totalDialNs) ?? 0
        self.rxBps = try container.decodeIfPresent(Double.self, forKey: .rxBps) ?? 0
        self.txBps = try container.decodeIfPresent(Double.self, forKey: .txBps) ?? 0
        self.rxTotal = try container.decodeIfPresent(UInt64.self, forKey: .rxTotal) ?? 0
        self.txTotal = try container.decodeIfPresent(UInt64.self, forKey: .txTotal) ?? 0
        self.durationNs = try container.decodeIfPresent(Int64.self, forKey: .durationNs) ?? 0
        self.closeReason = try container.decodeIfPresent(String.self, forKey: .closeReason) ?? ""
    }
}

public struct TrafficListenerPayload: Codable, Equatable, Sendable {
    public var `protocol`: String
    public var addr: String

    public init(protocol: String = "", addr: String = "") {
        self.protocol = `protocol`
        self.addr = addr
    }
}

public struct TrafficHopPayload: Codable, Equatable, Sendable {
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

    public init(index: Int = 0, name: String = "", protocol: String = "", address: String = "", state: String = "", elapsedNs: Int64 = 0, error: String = "") {
        self.index = index
        self.name = name
        self.protocol = `protocol`
        self.address = address
        self.state = state
        self.elapsedNs = elapsedNs
        self.error = error
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.index = try container.decodeIfPresent(Int.self, forKey: .index) ?? 0
        self.name = try container.decodeIfPresent(String.self, forKey: .name) ?? ""
        self.protocol = try container.decodeIfPresent(String.self, forKey: .protocol) ?? ""
        self.address = try container.decodeIfPresent(String.self, forKey: .address) ?? ""
        self.state = try container.decodeIfPresent(String.self, forKey: .state) ?? ""
        self.elapsedNs = try container.decodeIfPresent(Int64.self, forKey: .elapsedNs) ?? 0
        self.error = try container.decodeIfPresent(String.self, forKey: .error) ?? ""
    }
}

public struct TrafficTimelinePayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String { "\(tsNs)-\(type)-\(title)-\(detail)" }
    public var tsNs: Int64
    public var type: String
    public var title: String
    public var detail: String

    enum CodingKeys: String, CodingKey {
        case tsNs = "ts_ns"
        case type
        case title
        case detail
    }

    public init(tsNs: Int64 = 0, type: String = "", title: String = "", detail: String = "") {
        self.tsNs = tsNs
        self.type = type
        self.title = title
        self.detail = detail
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.tsNs = try container.decodeIfPresent(Int64.self, forKey: .tsNs) ?? 0
        self.type = try container.decodeIfPresent(String.self, forKey: .type) ?? ""
        self.title = try container.decodeIfPresent(String.self, forKey: .title) ?? ""
        self.detail = try container.decodeIfPresent(String.self, forKey: .detail) ?? ""
    }
}

public struct TrafficVisibilityPayload: Codable, Equatable, Sendable {
    public var kind: String
    public var method: String
    public var scheme: String
    public var host: String
    public var port: String
    public var path: String
    public var queryType: String

    enum CodingKeys: String, CodingKey {
        case kind
        case method
        case scheme
        case host
        case port
        case path
        case queryType = "query_type"
    }

    public init(kind: String = "", method: String = "", scheme: String = "", host: String = "", port: String = "", path: String = "", queryType: String = "") {
        self.kind = kind
        self.method = method
        self.scheme = scheme
        self.host = host
        self.port = port
        self.path = path
        self.queryType = queryType
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        self.kind = try container.decodeIfPresent(String.self, forKey: .kind) ?? ""
        self.method = try container.decodeIfPresent(String.self, forKey: .method) ?? ""
        self.scheme = try container.decodeIfPresent(String.self, forKey: .scheme) ?? ""
        self.host = try container.decodeIfPresent(String.self, forKey: .host) ?? ""
        self.port = try container.decodeIfPresent(String.self, forKey: .port) ?? ""
        self.path = try container.decodeIfPresent(String.self, forKey: .path) ?? ""
        self.queryType = try container.decodeIfPresent(String.self, forKey: .queryType) ?? ""
    }
}

public struct DashboardSnapshot: Codable, Equatable, Sendable {
    public var updatedAt: Date
    public var apiOnline: Bool
    public var running: Bool
    public var profile: String
    public var listenerCount: Int
    public var activeConnections: Int
    public var rxBps: Double
    public var txBps: Double
    public var logs: [String]

    public init(
        updatedAt: Date = Date(),
        apiOnline: Bool = false,
        running: Bool = false,
        profile: String = "",
        listenerCount: Int = 0,
        activeConnections: Int = 0,
        rxBps: Double = 0,
        txBps: Double = 0,
        logs: [String] = []
    ) {
        self.updatedAt = updatedAt
        self.apiOnline = apiOnline
        self.running = running
        self.profile = profile
        self.listenerCount = listenerCount
        self.activeConnections = activeConnections
        self.rxBps = rxBps
        self.txBps = txBps
        self.logs = logs
    }
}
