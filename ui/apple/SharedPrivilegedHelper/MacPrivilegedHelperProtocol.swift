import Foundation

let macPrivilegedHelperMachServiceName = "org.jpfchang.clambhook.mac.helper"
let macPrivilegedHelperPlistName = "org.jpfchang.clambhook.mac.helper.plist"

enum MacPrivilegedHelperReplyKey {
    static let ok = "ok"
    static let running = "running"
    static let message = "message"
    static let executablePath = "executable_path"
    static let pid = "pid"
}

@objc protocol ClambhookPrivilegedHelperProtocol {
    func status(withReply reply: @escaping (NSDictionary) -> Void)
    func startDaemon(
        configPath: String,
        apiAddress: String,
        apiToken: String,
        withReply reply: @escaping (NSDictionary) -> Void
    )
    func stopDaemon(withReply reply: @escaping (NSDictionary) -> Void)
}
