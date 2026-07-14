import Foundation

struct MacCommandResult: Equatable {
    var stdout: String
    var stderr: String
    var status: Int32
}

protocol MacCommandRunning {
    @discardableResult
    func run(_ executable: String, arguments: [String]) throws -> MacCommandResult
}

struct MacCommandRunner: MacCommandRunning {
    @discardableResult
    func run(_ executable: String, arguments: [String]) throws -> MacCommandResult {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = arguments

        let stdout = Pipe()
        let stderr = Pipe()
        process.standardOutput = stdout
        process.standardError = stderr

        try process.run()
        process.waitUntilExit()

        let outData = stdout.fileHandleForReading.readDataToEndOfFile()
        let errData = stderr.fileHandleForReading.readDataToEndOfFile()
        let result = MacCommandResult(
            stdout: String(data: outData, encoding: .utf8) ?? "",
            stderr: String(data: errData, encoding: .utf8) ?? "",
            status: process.terminationStatus
        )
        guard result.status == 0 else {
            let message = result.stderr.trimmingCharacters(in: .whitespacesAndNewlines)
            throw MacCommandError.failed(message.isEmpty ? result.stdout : message)
        }
        return result
    }
}

enum MacCommandError: Error, LocalizedError, Equatable {
    case failed(String)

    var errorDescription: String? {
        switch self {
        case .failed(let message):
            return message
        }
    }
}
