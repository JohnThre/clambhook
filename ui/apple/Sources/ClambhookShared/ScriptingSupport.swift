import Foundation

// MARK: - Scripting / Module payloads

public struct ModulePayload: Codable, Equatable, Identifiable, Sendable {
    public var id: String { name }
    public let name: String
    public let enabled: Bool
    public let scriptPath: String?
    public let hasRequestHook: Bool
    public let hasResponseHook: Bool
    public let hasCronHooks: Bool
    public let errors: [String]

    public init(name: String, enabled: Bool, scriptPath: String? = nil, hasRequestHook: Bool = false, hasResponseHook: Bool = false, hasCronHooks: Bool = false, errors: [String] = []) {
        self.name = name
        self.enabled = enabled
        self.scriptPath = scriptPath
        self.hasRequestHook = hasRequestHook
        self.hasResponseHook = hasResponseHook
        self.hasCronHooks = hasCronHooks
        self.errors = errors
    }
}

public struct ModulesPayload: Codable, Equatable, Sendable {
    public let modules: [ModulePayload]

    public init(modules: [ModulePayload]) {
        self.modules = modules
    }
}

public protocol ClambhookScriptingProviding: AnyObject {
    func modules() async throws -> ModulesPayload
    func replaceModules(_ modules: [ModulePayload]) async throws -> ModulesPayload
    func moduleLogs(id: String) async throws -> ModuleLogsPayload
}

public struct ModuleLogsPayload: Codable, Equatable, Sendable {
    public let module: String
    public let logs: [ModuleLogEntry]

    public init(module: String, logs: [ModuleLogEntry]) {
        self.module = module
        self.logs = logs
    }
}

public struct ModuleLogEntry: Codable, Equatable, Sendable {
    public let time: TimeInterval
    public let module: String
    public let message: String

    public init(time: TimeInterval, module: String, message: String) {
        self.time = time
        self.module = module
        self.message = message
    }
}
