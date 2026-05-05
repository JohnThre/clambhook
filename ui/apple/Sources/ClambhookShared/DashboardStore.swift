import Foundation

public let bandwidthSampleLimit = 60
public let maxLogLines = 200

@MainActor
public final class DashboardStore: ObservableObject {
    @Published public private(set) var status = StatusPayload()
    @Published public private(set) var profiles = ProfilesPayload()
    @Published public private(set) var servers = ServersPayload()
    @Published public private(set) var bandwidthSamples: [BandwidthSample] = []
    @Published public private(set) var logs: [String] = []
    @Published public private(set) var apiOnline = false
    @Published public private(set) var errorText = ""

    private let api: ClambhookAPIProviding
    private let snapshotStore: FileSnapshotStore
    private var eventTask: Task<Void, Never>?

    public init(api: ClambhookAPIProviding, snapshotStore: FileSnapshotStore) {
        self.api = api
        self.snapshotStore = snapshotStore
    }

    deinit {
        eventTask?.cancel()
    }

    public var activeProfile: String {
        profiles.active.isEmpty ? status.profile : profiles.active
    }

    public var currentBandwidth: BandwidthSample {
        bandwidthSamples.last ?? BandwidthSample()
    }

    public func refreshDashboard() async {
        do {
            let status = try await api.status()
            let profiles = try await api.profiles()
            let servers = try await api.servers()
            self.status = status
            self.profiles = profiles
            self.servers = servers
            self.apiOnline = true
            self.errorText = ""
            await persistSnapshot()
        } catch {
            self.apiOnline = false
            self.errorText = error.localizedDescription
            await persistSnapshot()
        }
    }

    public func refreshStatus() async {
        do {
            status = try await api.status()
            apiOnline = true
            errorText = ""
            await persistSnapshot()
        } catch {
            apiOnline = false
            errorText = error.localizedDescription
            await persistSnapshot()
        }
    }

    public func connect() async {
        await performAction { try await api.connect() }
    }

    public func disconnect() async {
        await performAction { try await api.disconnect() }
    }

    public func setActiveProfile(_ name: String) async {
        guard name != activeProfile else { return }
        await performAction { try await api.setActiveProfile(name) }
    }

    public func startEventStream(from client: ClambhookAPIClient, reconnectDelay: Duration = .seconds(2)) {
        eventTask?.cancel()
        eventTask = Task { [weak self] in
            while !Task.isCancelled {
                do {
                    for try await event in client.eventStream() {
                        await self?.apply(event: event)
                    }
                } catch {
                    await MainActor.run {
                        self?.errorText = "events: \(error.localizedDescription)"
                    }
                }
                try? await Task.sleep(for: reconnectDelay)
            }
        }
    }

    public func stopEventStream() {
        eventTask?.cancel()
        eventTask = nil
    }

    public func apply(event: DaemonEvent) async {
        switch event.type {
        case "connection.bytes":
            applyConnectionBytes(event)
        case "log.line":
            applyLogLine(event)
        default:
            break
        }
        await persistSnapshot()
    }

    private func performAction(_ action: () async throws -> Void) async {
        do {
            try await action()
            await refreshDashboard()
        } catch {
            apiOnline = false
            errorText = error.localizedDescription
            await persistSnapshot()
        }
    }

    private func applyConnectionBytes(_ event: DaemonEvent) {
        guard
            let rxDelta = event.data["rx_delta"]?.doubleValue,
            let txDelta = event.data["tx_delta"]?.doubleValue,
            let intervalNs = event.data["interval_ns"]?.doubleValue,
            intervalNs > 0
        else {
            return
        }
        let seconds = intervalNs / 1_000_000_000
        bandwidthSamples.append(BandwidthSample(rxBps: rxDelta / seconds, txBps: txDelta / seconds))
        if bandwidthSamples.count > bandwidthSampleLimit {
            bandwidthSamples.removeFirst(bandwidthSamples.count - bandwidthSampleLimit)
        }
    }

    private func applyLogLine(_ event: DaemonEvent) {
        guard let line = event.data["line"]?.stringValue else {
            return
        }
        logs.append(line)
        if logs.count > maxLogLines {
            logs.removeFirst(logs.count - maxLogLines)
        }
    }

    private func makeSnapshot() -> DashboardSnapshot {
        let activeConnections = status.listeners.reduce(0) { $0 + $1.activeConns }
        return DashboardSnapshot(
            updatedAt: Date(),
            apiOnline: apiOnline,
            running: status.running,
            profile: activeProfile,
            listenerCount: status.listeners.count,
            activeConnections: activeConnections,
            rxBps: currentBandwidth.rxBps,
            txBps: currentBandwidth.txBps,
            logs: Array(logs.suffix(10))
        )
    }

    private func persistSnapshot() async {
        try? await snapshotStore.save(makeSnapshot())
    }
}
