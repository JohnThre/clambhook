// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

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
    private let gate = SparkleUpdateGate()
    private var canCheckObservation: NSKeyValueObservation?

    override init() {
        delegate = SparkleDelegate(gate: gate)
        controller = SPUStandardUpdaterController(startingUpdater: true, updaterDelegate: delegate, userDriverDelegate: nil)
        automaticallyChecksForUpdates = controller.updater.automaticallyChecksForUpdates
        super.init()
        canCheckForUpdates = controller.updater.canCheckForUpdates
        canCheckObservation = controller.updater.observe(\.canCheckForUpdates, options: [.initial, .new]) { [weak self] updater, _ in
            Task { @MainActor in self?.canCheckForUpdates = updater.canCheckForUpdates }
        }
    }

    func checkForUpdates() {
        controller.updater.checkForUpdates()
    }

    /// Push the current feed URL and license decision into the thread-safe gate
    /// read by Sparkle's (possibly off-main) delegate callbacks.
    func refreshGate(feedURLString: String, decision: MobileLicenseDecision) {
        gate.update(feedURLString: feedURLString, decision: decision)
    }
}

private final class SparkleDelegate: NSObject, SPUUpdaterDelegate {
    private let gate: SparkleUpdateGate

    init(gate: SparkleUpdateGate) {
        self.gate = gate
    }

    func feedURLString(for updater: SPUUpdater) -> String? {
        gate.feedURLString()
    }

    func updater(_ updater: SPUUpdater, shouldProceedWithUpdate updateItem: SUAppcastItem, updateCheck: SPUUpdateCheck) throws {
        guard !gate.allowsUpdate(publishedAt: updateItem.date) else { return }
        throw NSError(
            domain: "org.jpfchang.clambhook.sparkle",
            code: 1,
            userInfo: [NSLocalizedDescriptionKey: "This update was released after your paid-through cutoff. Your compatible fallback remains usable; resubscribe for USD 79.99/year at store.swiphtgroup.com to install later releases."]
        )
    }
}
