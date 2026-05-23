import Foundation

public func countryFlag(_ code: String) -> String {
    let upper = code.trimmingCharacters(in: .whitespacesAndNewlines).uppercased()
    guard upper.count == 2 else {
        return "--"
    }
    let scalars = upper.unicodeScalars
    guard scalars.allSatisfy({ $0.value >= 65 && $0.value <= 90 }) else {
        return "--"
    }
    let flags = scalars.compactMap { UnicodeScalar(0x1F1E6 + $0.value - 65) }
    return String(String.UnicodeScalarView(flags))
}

public func formatRate(_ bytesPerSecond: Double) -> String {
    if bytesPerSecond < 1024 {
        return String(format: "%.0f B/s", bytesPerSecond)
    }
    if bytesPerSecond < 1024 * 1024 {
        return String(format: "%.1f KB/s", bytesPerSecond / 1024)
    }
    return String(format: "%.1f MB/s", bytesPerSecond / (1024 * 1024))
}

public func formatBytes(_ bytes: UInt64) -> String {
    if bytes < 1024 {
        return "\(bytes) B"
    }
    if bytes < 1024 * 1024 {
        return String(format: "%.1f KB", Double(bytes) / 1024)
    }
    if bytes < 1024 * 1024 * 1024 {
        return String(format: "%.1f MB", Double(bytes) / (1024 * 1024))
    }
    return String(format: "%.1f GB", Double(bytes) / (1024 * 1024 * 1024))
}

public func formatDurationNs(_ ns: Int64) -> String {
    if ns <= 0 {
        return "--"
    }
    let seconds = ns / 1_000_000_000
    if seconds < 1 {
        return "\(ns / 1_000_000) ms"
    }
    if seconds < 60 {
        return "\(seconds) s"
    }
    return "\(seconds / 60) min"
}

public func serverLocation(_ server: ServerPayload) -> String {
    if server.geoError?.isEmpty == false {
        return "geo error"
    }
    if !server.geo.city.isEmpty && !server.geo.country.isEmpty {
        return "\(server.geo.city), \(server.geo.country)"
    }
    if !server.geo.country.isEmpty {
        return server.geo.country
    }
    return "--"
}

public func emptyDash(_ value: String) -> String {
    value.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "--" : value
}
