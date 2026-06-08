import Foundation

public let bandwidthSampleLimit = 60
public let maxLogLines = 200

@MainActor
public final class DashboardStore: ObservableObject {
    @Published public private(set) var status = StatusPayload()
    @Published public private(set) var profiles = ProfilesPayload()
    @Published public private(set) var servers = ServersPayload()
    @Published public private(set) var rules = RulesPayload()
    @Published public private(set) var policyGroups = PolicyGroupsPayload()
    @Published public private(set) var ruleSets = RuleSetsPayload()
    @Published public private(set) var ruleSubscriptions = RuleSubscriptionsPayload()
    @Published public private(set) var traffic = TrafficSnapshotPayload()
    @Published public private(set) var networkSettings = TunnelNetworkSettingsPayload()
    @Published public private(set) var bandwidthSamples: [BandwidthSample] = []
    @Published public private(set) var logs: [String] = []
    @Published public private(set) var apiOnline = false
    @Published public private(set) var errorText = ""
    @Published public private(set) var recoveryIssue: TunnelRecoveryIssue?

    private let api: ClambhookAPIProviding
    private let snapshotStore: FileSnapshotStore
    private var eventTask: Task<Void, Never>?
    private var logRetention: Int

    public init(api: ClambhookAPIProviding, snapshotStore: FileSnapshotStore, logRetention: Int = maxLogLines) {
        self.api = api
        self.snapshotStore = snapshotStore
        self.logRetention = min(max(logRetention, minLogRetention), maxLogRetention)
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
            if let dashboardAPI = api as? ClambhookDashboardProviding {
                apply(dashboard: try await dashboardAPI.dashboard())
            } else {
                let status = try await api.status()
                let profiles = try await api.profiles()
                let servers = try await api.servers()
                let policyGroups = try await api.policyGroups()
                let rules = try await api.rules()
                let traffic = try await api.traffic()
                apply(dashboard: TunnelDashboardPayload(
                    status: status,
                    profiles: profiles,
                    servers: servers,
                    rules: rules,
                    policyGroups: policyGroups,
                    ruleSets: RuleSetsPayload(profile: rules.profile, statuses: rules.ruleSets),
                    traffic: traffic
                ))
            }
            self.apiOnline = true
            updateRecoveryIssueFromTraffic()
            await persistSnapshot()
        } catch {
            self.apiOnline = false
            setRecoveryIssue(TunnelRecoveryClassifier.issue(for: error))
            await persistSnapshot()
        }
    }

    private func apply(dashboard: TunnelDashboardPayload) {
        status = dashboard.status
        profiles = dashboard.profiles
        servers = dashboard.servers
        rules = dashboard.rules
        policyGroups = dashboard.policyGroups
        ruleSets = dashboard.ruleSets
        ruleSubscriptions = dashboard.ruleSubscriptions
        traffic = dashboard.traffic
        networkSettings = dashboard.networkSettings
    }

    public func refreshStatus() async {
        do {
            status = try await api.status()
            policyGroups = try await api.policyGroups()
            traffic = try await api.traffic()
            apiOnline = true
            updateRecoveryIssueFromTraffic()
            await persistSnapshot()
        } catch {
            apiOnline = false
            setRecoveryIssue(TunnelRecoveryClassifier.issue(for: error))
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
                        self?.setRecoveryIssue(TunnelRecoveryClassifier.issue(forRawError: "events: \(error.localizedDescription)"))
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

    public func updateLogRetention(_ value: Int) {
        logRetention = min(max(value, minLogRetention), maxLogRetention)
        trimLogs()
    }

    public func apply(event: DaemonEvent) async {
        switch event.type {
        case "connection.bytes":
            applyConnectionBytes(event)
        case "log.line":
            applyLogLine(event)
        default:
            if event.type.hasPrefix("connection.") || event.type.hasPrefix("rule.") || event.type.hasPrefix("hop.") {
                await refreshStatus()
                return
            }
        }
        await persistSnapshot()
    }

    public func setRecoveryIssue(_ issue: TunnelRecoveryIssue?) {
        recoveryIssue = issue
        errorText = issue?.message ?? ""
    }

    public func clearRecoveryIssue(kind: TunnelRecoveryKind? = nil) {
        guard let kind else {
            setRecoveryIssue(nil)
            return
        }
        if recoveryIssue?.kind == kind {
            setRecoveryIssue(nil)
        }
    }

    private func performAction(_ action: () async throws -> Void) async {
        do {
            try await action()
            await refreshDashboard()
        } catch {
            apiOnline = false
            setRecoveryIssue(TunnelRecoveryClassifier.issue(for: error))
            await persistSnapshot()
        }
    }

    private func updateRecoveryIssueFromTraffic() {
        if let issue = trafficRecoveryIssue() {
            setRecoveryIssue(issue)
        } else {
            setRecoveryIssue(nil)
        }
    }

    private func trafficRecoveryIssue() -> TunnelRecoveryIssue? {
        traffic.connections
            .sorted { $0.updatedTsNs > $1.updatedTsNs }
            .lazy
            .flatMap(\.hops)
            .compactMap { hop -> TunnelRecoveryIssue? in
                guard !hop.error.isEmpty else { return nil }
                let issue = TunnelRecoveryClassifier.issue(forRawError: hop.error)
                return issue.kind == .generic ? nil : issue
            }
            .first
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
        let sample = BandwidthSample(rxBps: rxDelta / seconds, txBps: txDelta / seconds)
        bandwidthSamples.append(sample)
        if bandwidthSamples.count > bandwidthSampleLimit {
            bandwidthSamples.removeFirst(bandwidthSamples.count - bandwidthSampleLimit)
        }
        applyTrafficBytes(event, sample: sample, rxDelta: rxDelta, txDelta: txDelta)
    }

    private func applyTrafficBytes(_ event: DaemonEvent, sample: BandwidthSample, rxDelta: Double, txDelta: Double) {
        guard let connID = event.data["conn_id"]?.stringValue,
              let index = traffic.connections.firstIndex(where: { $0.connID == connID }) else {
            return
        }
        let oldRxBps = traffic.connections[index].rxBps
        let oldTxBps = traffic.connections[index].txBps
        traffic.connections[index].rxBps = sample.rxBps
        traffic.connections[index].txBps = sample.txBps
        traffic.connections[index].rxTotal += UInt64(rxDelta)
        traffic.connections[index].txTotal += UInt64(txDelta)
        traffic.summary.rxBps += sample.rxBps - oldRxBps
        traffic.summary.txBps += sample.txBps - oldTxBps
        traffic.summary.rxTotal += UInt64(rxDelta)
        traffic.summary.txTotal += UInt64(txDelta)
    }

    private func applyLogLine(_ event: DaemonEvent) {
        guard let line = event.data["line"]?.stringValue else {
            return
        }
        logs.append(line)
        trimLogs()
    }

    private func trimLogs() {
        if logs.count > logRetention {
            logs.removeFirst(logs.count - logRetention)
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
