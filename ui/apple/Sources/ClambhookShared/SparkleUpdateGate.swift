import Foundation

/// Thread-safe snapshot of the Sparkle feed URL and the license-based update
/// gate.
///
/// Sparkle's `SPUUpdaterDelegate` callbacks are not guaranteed to be delivered
/// on the main thread, so reaching back into `@MainActor` state via
/// `MainActor.assumeIsolated` from those callbacks is unsound. Instead the
/// `@MainActor` owner pushes the current feed URL and license decision into this
/// lock-protected holder, and the delegate reads a consistent snapshot from any
/// thread.
public final class SparkleUpdateGate: @unchecked Sendable {
    private let lock = NSLock()
    private var storedFeedURLString: String
    private var storedDecision: MobileLicenseDecision?

    public init(
        feedURLString: String = defaultStableAppcastURL.absoluteString,
        decision: MobileLicenseDecision? = nil
    ) {
        self.storedFeedURLString = feedURLString
        self.storedDecision = decision
    }

    /// Atomically replace the feed URL and license decision.
    public func update(feedURLString: String, decision: MobileLicenseDecision) {
        lock.lock()
        defer { lock.unlock() }
        storedFeedURLString = feedURLString
        storedDecision = decision
    }

    /// The current appcast feed URL string.
    public func feedURLString() -> String {
        lock.lock()
        defer { lock.unlock() }
        return storedFeedURLString
    }

    /// Whether an update published at `publishedAt` may be installed under the
    /// current license decision. Returns `false` until a decision is set.
    public func allowsUpdate(publishedAt: Date?, now: Date = Date()) -> Bool {
        lock.lock()
        let decision = storedDecision
        lock.unlock()
        guard let decision else {
            return false
        }
        return MobileLicenseUpdatePolicy.canInstallUpdate(
            decision: decision,
            publishedAt: publishedAt,
            now: now
        )
    }
}
