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
