import AppKit
import ClambhookShared
import SwiftUI

// MARK: - DNS

struct MacDNSSection: View {
    @ObservedObject var model: AppleAppModel
    @State private var showAddUpstreamSheet = false
    @State private var saveError = ""

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                dnsOverview
                Divider()
                upstreamsSection
                if !model.dashboard.dns.upstreamRoutes.isEmpty {
                    Divider()
                    routesTable
                }
            }
            .padding(20)
        }
        .sheet(isPresented: $showAddUpstreamSheet) {
            MacDNSUpstreamSheet { upstream in
                var upstreams = model.dashboard.dns.upstreams
                upstreams.append(upstream)
                Task {
                    await model.dashboard.updateDNS(
                        enabled: model.dashboard.dns.enabled,
                        timeout: model.dashboard.dns.timeout,
                        upstreams: upstreams
                    )
                }
            }
        }
    }

    private var dnsOverview: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("DNS Configuration")
                    .font(.headline)
                Spacer()
                Toggle("Encrypted DNS", isOn: Binding(
                    get: { model.dashboard.dns.enabled },
                    set: { enabled in
                        Task {
                            await model.dashboard.updateDNS(
                                enabled: enabled,
                                timeout: model.dashboard.dns.timeout,
                                upstreams: model.dashboard.dns.upstreams
                            )
                        }
                    }
                ))
                .toggleStyle(.switch)
                .controlSize(.small)
                .labelsHidden()
            }
            HStack(spacing: 16) {
                Label(model.dashboard.dns.enabled ? "Enabled" : "Disabled", systemImage: model.dashboard.dns.enabled ? "checkmark.circle.fill" : "xmark.circle")
                    .foregroundStyle(model.dashboard.dns.enabled ? .green : .secondary)
                Label("Strategy: \(model.dashboard.dns.strategy)", systemImage: "arrow.triangle.branch")
                    .foregroundStyle(.secondary)
                if !model.dashboard.dns.timeout.isEmpty {
                    Label("Timeout: \(model.dashboard.dns.timeout)", systemImage: "clock")
                        .foregroundStyle(.secondary)
                }
                if model.dashboard.dns.interceptsPort53 {
                    Label("Intercepts port 53", systemImage: "shield.lefthalf.filled")
                        .foregroundStyle(.blue)
                }
            }
            .font(.subheadline)
        }
    }

    private var upstreamsSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("Upstreams")
                    .font(.headline)
                Spacer()
                Button {
                    showAddUpstreamSheet = true
                } label: {
                    Label("Add", systemImage: "plus")
                }
                .buttonStyle(.borderless)
                .font(.caption)
            }
            if model.dashboard.dns.upstreams.isEmpty {
                Text("No upstreams configured. Add a DoH, DoT, or DoQ upstream.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else {
                ForEach(model.dashboard.dns.upstreams) { upstream in
                    HStack(spacing: 10) {
                        VStack(alignment: .leading, spacing: 3) {
                            Text(upstream.name.isEmpty ? upstream.id : upstream.name)
                                .font(.subheadline.weight(.medium))
                                .lineLimit(1)
                            HStack(spacing: 6) {
                                Text(upstream.protocol.uppercased())
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                Text(upstream.targetDescription)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                    .lineLimit(1)
                                    .truncationMode(.middle)
                            }
                        }
                        Spacer(minLength: 8)
                        Button(role: .destructive) {
                            let remaining = model.dashboard.dns.upstreams.filter { $0.id != upstream.id }
                            Task {
                                await model.dashboard.updateDNS(
                                    enabled: model.dashboard.dns.enabled,
                                    timeout: model.dashboard.dns.timeout,
                                    upstreams: remaining
                                )
                            }
                        } label: {
                            Image(systemName: "trash")
                                .foregroundStyle(.red)
                        }
                        .buttonStyle(.plain)
                        .help("Remove \(upstream.name.isEmpty ? upstream.id : upstream.name)")
                    }
                    .padding(.vertical, 2)
                    Divider()
                }
            }
            if !saveError.isEmpty {
                Text(saveError)
                    .font(.caption)
                    .foregroundStyle(.red)
            }
        }
    }

    private var upstreamsTable: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Upstreams")
                .font(.headline)
            Table(model.dashboard.dns.upstreams) {
                TableColumn("Name") { upstream in
                    Text(upstream.name.isEmpty ? upstream.id : upstream.name)
                }
                TableColumn("Protocol") { upstream in
                    Text(upstream.protocol.uppercased())
                }
                TableColumn("Address / URL") { upstream in
                    Text(upstream.targetDescription)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                TableColumn("Bootstrap IPs") { upstream in
                    Text(upstream.bootstrapIPs.isEmpty ? "--" : upstream.bootstrapIPs.joined(separator: ", "))
                        .font(.caption)
                        .lineLimit(1)
                }
            }
        }
    }

    private var routesTable: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("Upstream Routes")
                .font(.headline)
            Table(model.dashboard.dns.upstreamRoutes) {
                TableColumn("Name") { route in
                    Text(route.name.isEmpty ? route.id : route.name)
                }
                TableColumn("Network") { route in
                    Text(route.network.isEmpty ? "all" : route.network)
                }
                TableColumn("Action") { route in
                    Text(route.action)
                }
                TableColumn("Target") { route in
                    Text(route.target)
                        .lineLimit(1)
                }
                TableColumn("Chain") { route in
                    Text(route.chainName.isEmpty ? "--" : route.chainName)
                }
            }
        }
    }
}

// MARK: - DNS Upstream Add Sheet

struct MacDNSUpstreamSheet: View {
    var onAdd: (DNSUpstreamPayload) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var proto = "doh"
    @State private var url = ""
    @State private var address = ""
    @State private var serverName = ""
    @State private var bootstrapIPs = ""
    @State private var validationError = ""

    private let protocols = ["doh", "dot", "doq"]

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Add DNS Upstream")
                    .font(.headline)
                Spacer()
                Button("Cancel") { dismiss() }
            }
            .padding([.horizontal, .top], 16)
            .padding(.bottom, 8)
            Divider()
            Form {
                TextField("Name (optional)", text: $name)
                Picker("Protocol", selection: $proto) {
                    ForEach(protocols, id: \.self) { p in
                        Text(p.uppercased()).tag(p)
                    }
                }
                if proto == "doh" {
                    TextField("URL (https://...)", text: $url)
                } else {
                    TextField("Address (host:port)", text: $address)
                    TextField("Server Name (TLS SNI, optional)", text: $serverName)
                }
                TextField("Bootstrap IPs (comma-separated, optional)", text: $bootstrapIPs)
            }
            .padding(12)
            Divider()
            VStack(alignment: .leading, spacing: 6) {
                if !validationError.isEmpty {
                    Text(validationError)
                        .font(.caption)
                        .foregroundStyle(.red)
                }
                HStack {
                    Spacer()
                    Button("Add Upstream") {
                        guard validate() else { return }
                        let ips = bootstrapIPs.split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
                        let upstream = DNSUpstreamPayload(
                            name: name,
                            protocol: proto,
                            url: proto == "doh" ? url : "",
                            address: proto != "doh" ? address : "",
                            serverName: serverName,
                            bootstrapIPs: ips
                        )
                        onAdd(upstream)
                        dismiss()
                    }
                    .buttonStyle(.borderedProminent)
                }
            }
            .padding(12)
        }
        .frame(width: 440, height: 340)
    }

    private func validate() -> Bool {
        if proto == "doh" && url.isEmpty {
            validationError = "URL is required for DoH"
            return false
        }
        if proto != "doh" && address.isEmpty {
            validationError = "Address is required for \(proto.uppercased())"
            return false
        }
        validationError = ""
        return true
    }
}
