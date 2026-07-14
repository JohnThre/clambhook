import ClambhookShared
import CoreLocation
import MapKit
import SwiftUI

// MARK: - Connection map

/// Little Snitch-style world map of live Internet connections. Plots one pin
/// per geolocated place, colored by the dominant routing decision, with a
/// side list mirroring `MacActivitySection` for selecting and inspecting a
/// location. Reads `model.dashboard.traffic.connections` directly, so it
/// refreshes with the shared dashboard timer and event stream.
struct MacConnectionMapSection: View {
    @ObservedObject var model: AppleAppModel
    @State private var camera: MapCameraPosition = .automatic
    @State private var selectedID: String?

    private var points: [ConnectionMapPoint] {
        model.dashboard.traffic.connectionMapPoints()
    }

    private var selectedPoint: ConnectionMapPoint? {
        guard let id = selectedID else { return nil }
        return points.first { $0.id == id }
    }

    var body: some View {
        VStack(spacing: 0) {
            legendBar
            Divider()
            if points.isEmpty {
                emptyState
            } else {
                HSplitView {
                    mapView
                        .frame(minWidth: 420)
                    locationList
                        .frame(minWidth: 240)
                }
            }
        }
        .onChange(of: selectedID) { _, _ in focusSelection() }
    }

    // MARK: Legend / stats bar

    private var legendBar: some View {
        HStack(spacing: 14) {
            Label("Connection Map", systemImage: "globe.americas.fill")
                .font(.headline)
            Spacer(minLength: 8)
            legendDot(.green, "Proxy")
            legendDot(.blue, "Direct")
            legendDot(.red, "Blocked")
            if !points.isEmpty {
                Text("\(points.count) \(points.count == 1 ? "location" : "locations")")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 10)
    }

    private func legendDot(_ color: Color, _ label: String) -> some View {
        HStack(spacing: 4) {
            Circle().fill(color).frame(width: 8, height: 8)
            Text(label)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }

    // MARK: Map

    private var mapView: some View {
        Map(position: $camera, selection: $selectedID) {
            ForEach(points) { point in
                Annotation(point.locationName, coordinate: point.coordinate) {
                    ConnectionMapMarker(
                        point: point,
                        isSelected: point.id == selectedID
                    )
                    .onTapGesture { selectedID = point.id }
                }
                .tag(point.id)
                .annotationTitles(.hidden)
            }
        }
        .mapStyle(.standard(elevation: .flat, pointsOfInterest: .excludingAll))
        .mapControls {
            MapCompass()
            MapZoomStepper()
        }
    }

    // MARK: Location list

    private var locationList: some View {
        List(points, selection: $selectedID) { point in
            ConnectionMapLocationRow(point: point)
                .tag(point.id)
        }
        .listStyle(.plain)
        .safeAreaInset(edge: .bottom) {
            if let point = selectedPoint {
                ConnectionMapDetailCard(point: point)
            }
        }
    }

    // MARK: Empty state

    private var emptyState: some View {
        VStack(spacing: 8) {
            Spacer()
            Image(systemName: "globe.badge.chevron.backward")
                .font(.system(size: 40))
                .foregroundStyle(.quaternary)
            Text("No geolocated connections")
                .foregroundStyle(.secondary)
            Text("Connections appear here once the daemon resolves their location.")
                .font(.caption)
                .foregroundStyle(.tertiary)
                .multilineTextAlignment(.center)
            Spacer()
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding(32)
    }

    private func focusSelection() {
        guard let point = selectedPoint else { return }
        withAnimation(.easeInOut(duration: 0.4)) {
            camera = .region(
                MKCoordinateRegion(
                    center: point.coordinate,
                    span: MKCoordinateSpan(latitudeDelta: 12, longitudeDelta: 12)
                )
            )
        }
    }
}

// MARK: - Marker

private struct ConnectionMapMarker: View {
    var point: ConnectionMapPoint
    var isSelected: Bool

    private var diameter: CGFloat {
        let base = 16.0
        let growth = min(Double(point.connectionCount), 12) * 1.6
        return base + growth
    }

    var body: some View {
        ZStack {
            if point.activeCount > 0 {
                Circle()
                    .fill(color.opacity(0.25))
                    .frame(width: diameter + 10, height: diameter + 10)
            }
            Circle()
                .fill(color)
                .frame(width: diameter, height: diameter)
                .overlay(
                    Circle().stroke(.white, lineWidth: isSelected ? 3 : 1.5)
                )
                .shadow(radius: isSelected ? 4 : 1)
            if point.connectionCount > 1 {
                Text("\(point.connectionCount)")
                    .font(.system(size: 10, weight: .bold))
                    .foregroundStyle(.white)
            }
        }
    }

    private var color: Color { actionFamilyColor(point.dominantActionFamily) }
}

// MARK: - Location row

private struct ConnectionMapLocationRow: View {
    var point: ConnectionMapPoint

    var body: some View {
        HStack(spacing: 8) {
            Text(countryFlag(point.countryCode))
                .frame(width: 22)
            VStack(alignment: .leading, spacing: 2) {
                Text(point.locationName)
                    .font(.subheadline.weight(.medium))
                    .lineLimit(1)
                Text("\(formatBytes(point.rxTotal)) down · \(formatBytes(point.txTotal)) up")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            VStack(alignment: .trailing, spacing: 2) {
                if point.activeCount > 0 {
                    HStack(spacing: 3) {
                        Circle().fill(Color.green).frame(width: 6, height: 6)
                        Text("\(point.activeCount) active")
                            .font(.caption2)
                            .foregroundStyle(.green)
                    }
                }
                Text("\(point.connectionCount) total")
                    .font(.caption2.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 2)
    }
}

// MARK: - Detail card

private struct ConnectionMapDetailCard: View {
    var point: ConnectionMapPoint

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Text(countryFlag(point.countryCode))
                Text(point.locationName)
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                Spacer(minLength: 4)
            }
            HStack(spacing: 6) {
                mapDecisionChip("P \(point.proxyCount)", .green)
                mapDecisionChip("D \(point.directCount)", .blue)
                mapDecisionChip("B \(point.blockCount)", .red)
            }
            if !point.sampleHosts.isEmpty {
                Text(point.sampleHosts.joined(separator: ", "))
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                    .truncationMode(.middle)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(12)
        .background(.bar)
    }

    private func mapDecisionChip(_ label: String, _ color: Color) -> some View {
        Text(label)
            .font(.caption2.weight(.semibold))
            .monospacedDigit()
            .foregroundStyle(color)
            .padding(.horizontal, 6)
            .padding(.vertical, 3)
            .background(color.opacity(0.12), in: Capsule())
    }
}

// MARK: - Helpers

private func actionFamilyColor(_ family: String) -> Color {
    switch family {
    case "block": return .red
    case "direct": return .blue
    default: return .green
    }
}

private extension ConnectionMapPoint {
    var coordinate: CLLocationCoordinate2D {
        CLLocationCoordinate2D(latitude: latitude, longitude: longitude)
    }
}
