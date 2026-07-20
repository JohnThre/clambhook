import Foundation
#if canImport(Security)
import Security
#endif

/// Pure helpers for validating a privileged-helper XPC peer.
///
/// The peer is pinned by its kernel-provided audit token rather than its PID.
/// A PID can be recycled or spoofed in a time-of-check/time-of-use window, so
/// `kSecGuestAttributeAudit` (which carries the full audit token) is the
/// hardened way to identify the calling code.
public enum PrivilegedHelperClientAuthorization {
    /// Designated-requirement string an XPC peer's code signature must satisfy.
    public static func clientCodeRequirement(teamIdentifier: String, clientIdentifier: String) -> String {
        "anchor apple generic and certificate leaf[subject.OU] = \"\(teamIdentifier)\" and identifier \"\(clientIdentifier)\""
    }

    #if canImport(Darwin)
    /// Serializes an audit token into the raw byte representation expected by
    /// `kSecGuestAttributeAudit` (8 × `UInt32` = 32 bytes).
    public static func auditTokenData(_ token: audit_token_t) -> Data {
        withUnsafeBytes(of: token) { Data($0) }
    }
    #endif

    #if canImport(Security) && canImport(Darwin)
    /// Attributes for `SecCodeCopyGuestWithAttributes` that pin the peer by its
    /// audit token instead of a spoofable PID.
    public static func auditGuestAttributes(_ token: audit_token_t) -> [String: Any] {
        [kSecGuestAttributeAudit as String: auditTokenData(token)]
    }
    #endif
}
