import ClambhookShared
import Foundation
import Sparkle

@MainActor
final class MacSparkleUpdater: NSObject, ObservableObject {
    @Published private(set) var canCheckForUpdates = false
    @Published var automaticallyChecksForUpdates: Bool {
        didSet { controller.updater.automaticallyChecksForUpdates = automaticallyChecksForUpdates }
    }

    private let controller: SPUStandardUpdaterController
    private let delegate: SparkleDelegate
    private var canCheckObservation: NSKeyValueObservation?

    var feedURLProvider: @MainActor () -> String = { defaultStableAppcastURL.absoluteString }
    var canInstallUpdate: @MainActor (Date?) -> Bool = { _ in false }

    override init() {
        delegate = SparkleDelegate()
        controller = SPUStandardUpdaterController(startingUpdater: true, updaterDelegate: delegate, userDriverDelegate: nil)
        automaticallyChecksForUpdates = controller.updater.automaticallyChecksForUpdates
        super.init()
        delegate.owner = self
        canCheckForUpdates = controller.updater.canCheckForUpdates
        canCheckObservation = controller.updater.observe(\.canCheckForUpdates, options: [.initial, .new]) { [weak self] updater, _ in
            Task { @MainActor in self?.canCheckForUpdates = updater.canCheckForUpdates }
        }
    }

    func checkForUpdates() {
        controller.updater.checkForUpdates()
    }

    fileprivate func currentFeedURLString() -> String {
        feedURLProvider()
    }

    fileprivate func allowsUpdate(publishedAt: Date?) -> Bool {
        canInstallUpdate(publishedAt)
    }
}

private final class SparkleDelegate: NSObject, SPUUpdaterDelegate {
    weak var owner: MacSparkleUpdater?

    func feedURLString(for updater: SPUUpdater) -> String? {
        MainActor.assumeIsolated { owner?.currentFeedURLString() }
    }

    func updater(_ updater: SPUUpdater, shouldProceedWithUpdate updateItem: SUAppcastItem, updateCheck: SPUUpdateCheck) throws {
        let allowed = MainActor.assumeIsolated {
            owner?.allowsUpdate(publishedAt: updateItem.date) ?? false
        }
        guard !allowed else { return }
        throw NSError(
            domain: "org.jpfchang.clambhook.sparkle",
            code: 1,
            userInfo: [NSLocalizedDescriptionKey: "This update was released after your included update window. Renew updates for USD 9.99 at store.swiphtgroup.com to install it. Updates after the cutoff are not included, including critical, bug, and security updates."]
        )
    }
}
