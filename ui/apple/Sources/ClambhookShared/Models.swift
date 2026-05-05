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
