import ClambhookShared
import RealityKit
import SwiftUI
import UIKit

struct VisionNetworkMapImmersiveView: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        let snapshot = VisionMapSnapshot(dashboard: model.dashboard)

        ZStack(alignment: .bottomLeading) {
            RealityView { content in
                let root = Entity()
                root.name = "clambhook-network-map-root"
                root.position = SIMD3<Float>(0, 1.2, -1.6)
                content.add(root)
            } update: { content in
                guard let root = content.entities.first(where: { $0.name == "clambhook-network-map-root" }) else {
                    return
                }
                VisionNetworkMapBuilder.rebuild(root: root, snapshot: snapshot)
            }

            VisionImmersiveInfoPanel(snapshot: snapshot)
                .padding(40)
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottomLeading)
        }
    }
}

private struct VisionImmersiveInfoPanel: View {
    var snapshot: VisionMapSnapshot

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Label("Network Map", systemImage: "globe")
                .font(.headline)
                .foregroundStyle(.secondary)

            HStack(spacing: 18) {
                metric("Servers", "\(snapshot.nodes.count)")
                metric("Active", "\(snapshot.activeConnectionCount)")
                metric("Down", formatRate(snapshot.rxBps))
                metric("Up", formatRate(snapshot.txBps))
            }

            if let focusedNode = snapshot.focusedNode {
                Divider()
                VStack(alignment: .leading, spacing: 4) {
                    Text(focusedNode.name)
                        .font(.title3.weight(.semibold))
                        .lineLimit(1)
                    Text("\(focusedNode.protocolText) / \(focusedNode.locationText)")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                    Text("\(focusedNode.activeConnections) active / \(formatRate(focusedNode.rxBps)) down / \(formatRate(focusedNode.txBps)) up")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
        }
        .padding(18)
        .frame(width: 470, alignment: .leading)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
    }

    private func metric(_ title: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Text(title)
                .font(.caption)
                .foregroundStyle(.secondary)
            Text(value)
                .font(.headline)
                .monospacedDigit()
                .lineLimit(1)
                .minimumScaleFactor(0.75)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

private enum VisionNetworkMapBuilder {
    static func rebuild(root: Entity, snapshot: VisionMapSnapshot) {
        root.children.forEach { $0.removeFromParent() }

        root.addChild(makeNode(
            name: "Local API",
            position: .zero,
            radius: 0.09,
            color: UIColor(red: 0.20, green: 0.48, blue: 1.0, alpha: 1.0)
        ))

        for node in snapshot.nodes {
            let active = node.activeConnections > 0 || node.rxBps > 0 || node.txBps > 0
            let linkColor = active
                ? UIColor(red: 0.20, green: 0.80, blue: 0.38, alpha: 0.72)
                : UIColor(white: 0.72, alpha: 0.34)
            root.addChild(makeLink(
                from: .zero,
                to: node.position,
                color: linkColor,
                thickness: active ? 0.012 : 0.006
            ))

            root.addChild(makeNode(
                name: node.name,
                position: node.position,
                radius: nodeRadius(for: node),
                color: nodeColor(for: node)
            ))
        }
    }

    private static func makeNode(name: String, position: SIMD3<Float>, radius: Float, color: UIColor) -> ModelEntity {
        let entity = ModelEntity(
            mesh: .generateSphere(radius: radius),
            materials: [makeMaterial(color)]
        )
        entity.name = name
        entity.position = position
        return entity
    }

    private static func makeLink(from: SIMD3<Float>, to: SIMD3<Float>, color: UIColor, thickness: Float) -> ModelEntity {
        let delta = to - from
        let length = simd_length(delta)
        let entity = ModelEntity(
            mesh: .generateBox(width: max(length, 0.001), height: thickness, depth: thickness),
            materials: [makeMaterial(color)]
        )
        entity.position = (from + to) / 2
        if length > 0.001 {
            entity.orientation = simd_quatf(from: SIMD3<Float>(1, 0, 0), to: simd_normalize(delta))
        }
        return entity
    }

    private static func nodeRadius(for node: VisionMapNode) -> Float {
        let rate = max(node.rxBps, node.txBps)
        guard rate > 0 else {
            return 0.045
        }
        let scaled = min(Float(log10(rate + 1) / 8.0), 1.0)
        return 0.05 + scaled * 0.06
    }

    private static func nodeColor(for node: VisionMapNode) -> UIColor {
        if node.activeConnections > 0 || node.rxBps > 0 || node.txBps > 0 {
            return UIColor(red: 0.16, green: 0.86, blue: 0.38, alpha: 1.0)
        }
        return UIColor(white: 0.82, alpha: 0.92)
    }

    private static func makeMaterial(_ color: UIColor) -> SimpleMaterial {
        SimpleMaterial(color: color, roughness: 0.42, isMetallic: false)
    }
}

private struct VisionMapSnapshot {
    var nodes: [VisionMapNode]
    var activeConnectionCount: Int
    var rxBps: Double
    var txBps: Double

    var focusedNode: VisionMapNode? {
        nodes.max { lhs, rhs in
            lhs.activityScore < rhs.activityScore
        }
    }

    @MainActor
    init(dashboard: DashboardStore) {
        let activeConnections = dashboard.traffic.connections.filter { connection in
            connection.state.lowercased() == "active" || connection.rxBps > 0 || connection.txBps > 0
        }
        let serverRows = dashboard.servers.chains.flatMap { chain in
            chain.servers.map { (chain.name, $0) }
        }

        if serverRows.isEmpty {
            nodes = activeConnections.prefix(16).enumerated().map { index, connection in
                VisionMapNode(
                    id: connection.id,
                    name: emptyDash(connection.targetHost),
                    address: emptyDash(connection.target),
                    protocolText: emptyDash(connection.listener.protocol).uppercased(),
                    locationText: connection.geoError.isEmpty ? serverLocation(ServerPayload(name: "", address: "", protocol: "", geo: connection.geo)) : "geo error",
                    position: Self.projectFallback(index: index, count: max(activeConnections.count, 1)),
                    activeConnections: 1,
                    rxBps: connection.rxBps,
                    txBps: connection.txBps
                )
            }
        } else {
            nodes = serverRows.enumerated().map { index, row in
                let chainName = row.0
                let server = row.1
                let matches = activeConnections.filter { connection in
                    Self.connection(connection, matches: server, chainName: chainName)
                }
                return VisionMapNode(
                    id: "\(chainName)-\(server.id)",
                    name: server.name,
                    address: server.address,
                    protocolText: server.protocol.uppercased(),
                    locationText: serverLocation(server),
                    position: Self.project(location: server.geo, index: index, count: serverRows.count),
                    activeConnections: matches.count,
                    rxBps: matches.reduce(0) { $0 + $1.rxBps },
                    txBps: matches.reduce(0) { $0 + $1.txBps }
                )
            }
        }

        activeConnectionCount = activeConnections.count
        rxBps = dashboard.traffic.summary.rxBps
        txBps = dashboard.traffic.summary.txBps
    }

    private static func connection(_ connection: TrafficConnectionPayload, matches server: ServerPayload, chainName: String) -> Bool {
        if connection.chainName == chainName {
            return true
        }
        return connection.hops.contains { hop in
            let hopName = hop.name.trimmingCharacters(in: .whitespacesAndNewlines)
            let hopAddress = hop.address.trimmingCharacters(in: .whitespacesAndNewlines)
            let serverAddress = server.address.trimmingCharacters(in: .whitespacesAndNewlines)
            return (!hopName.isEmpty && hopName == server.name)
                || (!hopAddress.isEmpty && hopAddress == serverAddress)
                || (!hopAddress.isEmpty && !serverAddress.isEmpty && serverAddress.contains(hopAddress))
                || (!hopAddress.isEmpty && !serverAddress.isEmpty && hopAddress.contains(serverAddress))
        }
    }

    private static func project(location: LocationPayload, index: Int, count: Int) -> SIMD3<Float> {
        let hasCoordinates = abs(location.latitude) > 0.001 || abs(location.longitude) > 0.001
        guard hasCoordinates else {
            return projectFallback(index: index, count: count)
        }
        let longitude = max(-180, min(180, location.longitude))
        let latitude = max(-85, min(85, location.latitude))
        let x = Float(longitude / 180.0) * 1.25
        let y = Float(latitude / 85.0) * 0.62
        let z = Float((index % 5) - 2) * 0.08
        return SIMD3<Float>(x, y, z)
    }

    private static func projectFallback(index: Int, count: Int) -> SIMD3<Float> {
        let safeCount = max(count, 1)
        let angle = (Float(index) / Float(safeCount)) * 2 * .pi
        let radius: Float = 0.98
        return SIMD3<Float>(
            cos(angle) * radius,
            sin(angle) * 0.56,
            sin(angle * 0.7) * 0.28
        )
    }
}

private struct VisionMapNode: Identifiable {
    var id: String
    var name: String
    var address: String
    var protocolText: String
    var locationText: String
    var position: SIMD3<Float>
    var activeConnections: Int
    var rxBps: Double
    var txBps: Double

    var activityScore: Double {
        Double(activeConnections) * 1_000_000 + rxBps + txBps
    }
}
