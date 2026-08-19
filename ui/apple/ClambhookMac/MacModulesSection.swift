import ClambhookShared
import SwiftUI

struct MacModulesSection: View {
    @ObservedObject var model: AppleAppModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                Text("Modules")
                    .font(.title2.weight(.semibold))

                if model.modules.modules.isEmpty {
                    Text("No modules configured.")
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(model.modules.modules) { module in
                        ModuleRow(module: module)
                    }
                }

                Spacer()
            }
            .padding(20)
        }
        .task {
            model.refreshModules()
        }
    }
}

private struct ModuleRow: View {
    let module: ModulePayload

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(module.name)
                    .font(.headline)
                Spacer()
                Text(module.enabled ? "Enabled" : "Disabled")
                    .font(.caption)
                    .foregroundStyle(module.enabled ? .green : .secondary)
            }
            HStack(spacing: 12) {
                HookBadge(label: "request", active: module.hasRequestHook)
                HookBadge(label: "response", active: module.hasResponseHook)
                HookBadge(label: "cron", active: module.hasCronHooks)
            }
            if let path = module.scriptPath, !path.isEmpty {
                Text(path)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if !module.errors.isEmpty {
                Text(module.errors.joined(separator: "; "))
                    .font(.caption)
                    .foregroundStyle(.red)
            }
        }
        .padding(12)
        .background(Color(nsColor: .controlBackgroundColor))
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
    }
}

private struct HookBadge: View {
    let label: String
    let active: Bool

    var body: some View {
        HStack(spacing: 4) {
            Image(systemName: active ? "checkmark.circle.fill" : "circle")
                .foregroundStyle(active ? Color.accentColor : .secondary)
                .font(.caption2)
            Text(label)
                .font(.caption)
                .foregroundStyle(.secondary)
        }
    }
}
