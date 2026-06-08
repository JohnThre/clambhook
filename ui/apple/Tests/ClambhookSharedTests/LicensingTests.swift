import XCTest
@testable import ClambhookShared

final class LicensingTests: XCTestCase {
    func testTrialLastsTwoCalendarMonths() {
        let start = mobileLicenseUTCDate(year: 2026, month: 1, day: 31)
        let snapshot = MobileLicenseSnapshot(trialStartDate: start)

        let beforeExpiry = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 3, day: 30)
        )
        XCTAssertEqual(beforeExpiry.reason, .trial)
        XCTAssertTrue(beforeExpiry.canUseFeature(.tunnelRouting))

        let afterExpiry = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 4, day: 1)
        )
        XCTAssertEqual(afterExpiry.reason, .locked)
        XCTAssertFalse(afterExpiry.canUseApp)
    }

    func testExpiredTrialLocksPremiumFeaturesWithoutPurchase() {
        let snapshot = MobileLicenseSnapshot(trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3))

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 8, day: 4)
        )

        XCTAssertEqual(decision.reason, .locked)
        XCTAssertFalse(decision.canUseApp)
        XCTAssertFalse(decision.canUseFeature(.tunnelRouting))
        XCTAssertFalse(decision.canUseFeature(.routingRules))
    }

    func testLifetimeUnlockRemainsUsableWithoutRecentVerification() {
        let purchaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.lifetimeUnlockID, purchaseDate: purchaseDate),
            ],
            lastVerifiedAt: mobileLicenseUTCDate(year: 2026, month: 6, day: 10)
        )

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2028, month: 6, day: 18)
        )
        XCTAssertEqual(decision.reason, .lifetime)
        XCTAssertTrue(decision.canUseApp)
        XCTAssertEqual(decision.updateCutoffDate, mobileLicenseUTCDate(year: 2027, month: 6, day: 3))
        XCTAssertTrue(decision.canUseFeature(.tunnelRouting))
    }

    func testRecentVerificationFailureUsesOfflineGraceForCachedLifetime() {
        let purchaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let failedAt = mobileLicenseUTCDate(year: 2026, month: 7, day: 2)
        let includedFeature = MobileLicenseFeature(
            id: .widgets,
            displayName: "Included Widgets",
            releaseDate: mobileLicenseUTCDate(year: 2027, month: 6, day: 3)
        )
        let laterFeature = MobileLicenseFeature(
            id: .activityInspection,
            displayName: "Later Inspection",
            releaseDate: mobileLicenseUTCDate(year: 2027, month: 6, day: 4)
        )
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.lifetimeUnlockID, purchaseDate: purchaseDate),
            ],
            lastVerifiedAt: mobileLicenseUTCDate(year: 2026, month: 7, day: 1),
            lastVerificationFailedAt: failedAt
        )

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            features: [includedFeature, laterFeature],
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 5)
        )

        XCTAssertEqual(decision.reason, .offlineGrace)
        XCTAssertTrue(decision.canUseApp)
        XCTAssertTrue(decision.isOfflineGraceActive)
        XCTAssertEqual(decision.offlineGraceEndsAt, mobileLicenseUTCDate(year: 2026, month: 7, day: 9))
        XCTAssertEqual(decision.updateCutoffDate, mobileLicenseUTCDate(year: 2027, month: 6, day: 3))
        XCTAssertTrue(decision.canUseFeature(.widgets))
        XCTAssertFalse(decision.canUseFeature(.activityInspection))
    }

    func testOfflinePaidUseKeepsPurchasedFeatureReleasesEnabled() {
        let purchaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let includedFeature = MobileLicenseFeature(
            id: .widgets,
            displayName: "Included Widgets",
            releaseDate: mobileLicenseUTCDate(year: 2027, month: 6, day: 3)
        )
        let laterFeature = MobileLicenseFeature(
            id: .activityInspection,
            displayName: "Later Inspection",
            releaseDate: mobileLicenseUTCDate(year: 2027, month: 6, day: 4)
        )
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.lifetimeUnlockID, purchaseDate: purchaseDate),
            ],
            lastVerifiedAt: mobileLicenseUTCDate(year: 2026, month: 6, day: 10),
            lastVerificationFailedAt: mobileLicenseUTCDate(year: 2026, month: 6, day: 12)
        )

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            features: [includedFeature, laterFeature],
            now: mobileLicenseUTCDate(year: 2028, month: 6, day: 14)
        )
        XCTAssertEqual(decision.reason, .lifetime)
        XCTAssertTrue(decision.canUseApp)
        XCTAssertFalse(decision.isOfflineGraceActive)
        XCTAssertNil(decision.offlineGraceEndsAt)
        XCTAssertTrue(decision.canUseFeature(.widgets))
        XCTAssertFalse(decision.canUseFeature(.activityInspection))
    }

    func testRevokedLifetimeDoesNotUnlock() {
        let purchaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.lifetimeUnlockID,
                    purchaseDate: purchaseDate,
                    revocationDate: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
                ),
            ],
            lastVerifiedAt: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
        )

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 2)
        )
        XCTAssertEqual(decision.reason, .locked)
        XCTAssertFalse(decision.hasLifetimeUnlock)
    }

    func testActiveFamilySharingTransactionUnlocksLifetimeWindow() {
        let purchaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.lifetimeUnlockID,
                    purchaseDate: purchaseDate,
                    ownershipType: .familyShared
                ),
            ],
            lastVerifiedAt: mobileLicenseUTCDate(year: 2026, month: 6, day: 10)
        )

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 6, day: 11)
        )

        XCTAssertEqual(decision.reason, .lifetime)
        XCTAssertTrue(decision.hasLifetimeUnlock)
        XCTAssertEqual(decision.updateCutoffDate, mobileLicenseUTCDate(year: 2027, month: 6, day: 3))
        XCTAssertTrue(decision.canUseFeature(.tunnelRouting))
    }

    func testRevokedFamilyEntitlementDoesNotUnlock() {
        let purchaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.lifetimeUnlockID,
                    purchaseDate: purchaseDate,
                    revocationDate: mobileLicenseUTCDate(year: 2026, month: 9, day: 1),
                    ownershipType: .familyShared
                ),
            ],
            lastVerifiedAt: mobileLicenseUTCDate(year: 2026, month: 9, day: 1)
        )

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 9, day: 2)
        )

        XCTAssertEqual(decision.reason, .locked)
        XCTAssertFalse(decision.hasLifetimeUnlock)
        XCTAssertFalse(decision.canUseFeature(.tunnelRouting))
    }

    func testPaidUpdatesExtendFeatureWindow() throws {
        let lifetimeDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.lifetimeUnlockID, purchaseDate: lifetimeDate),
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.featureUpdate2027ID, purchaseDate: mobileLicenseUTCDate(year: 2027, month: 8, day: 1)),
            ],
            lastVerifiedAt: mobileLicenseUTCDate(year: 2027, month: 8, day: 1)
        )
        let futureFeature = MobileLicenseFeature(
            id: .widgets,
            displayName: "Future Widgets",
            releaseDate: mobileLicenseUTCDate(year: 2028, month: 7, day: 31)
        )

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            features: [futureFeature],
            now: mobileLicenseUTCDate(year: 2027, month: 8, day: 2)
        )
        XCTAssertEqual(decision.reason, .lifetime)
        XCTAssertEqual(decision.updateCutoffDate, mobileLicenseUTCDate(year: 2028, month: 8, day: 1))
        XCTAssertTrue(decision.canUseFeature(.widgets))
    }

    func testRefundedPaidUpdateDoesNotExtendFeatureWindow() {
        let lifetimeDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.lifetimeUnlockID, purchaseDate: lifetimeDate),
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.featureUpdate2027ID,
                    purchaseDate: mobileLicenseUTCDate(year: 2027, month: 8, day: 1),
                    revocationDate: mobileLicenseUTCDate(year: 2027, month: 8, day: 10)
                ),
            ],
            lastVerifiedAt: mobileLicenseUTCDate(year: 2027, month: 8, day: 10)
        )
        let futureFeature = MobileLicenseFeature(
            id: .widgets,
            displayName: "Future Widgets",
            releaseDate: mobileLicenseUTCDate(year: 2028, month: 7, day: 31)
        )

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            features: [futureFeature],
            now: mobileLicenseUTCDate(year: 2027, month: 8, day: 11)
        )

        XCTAssertEqual(decision.reason, .lifetime)
        XCTAssertEqual(decision.updateCutoffDate, mobileLicenseUTCDate(year: 2027, month: 6, day: 3))
        XCTAssertFalse(decision.canUseFeature(.widgets))
    }

    func testPaidUpdateWithoutLifetimeDoesNotUnlock() {
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.featureUpdate2027ID, purchaseDate: mobileLicenseUTCDate(year: 2027, month: 8, day: 1)),
            ],
            lastVerifiedAt: mobileLicenseUTCDate(year: 2027, month: 8, day: 1)
        )

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2027, month: 8, day: 2)
        )
        XCTAssertEqual(decision.reason, .locked)
        XCTAssertFalse(decision.canUseApp)
    }

    func testAppReinstallKeepsOriginalTrialStartFromCredentialStore() {
        let credentialStore = InMemoryCredentialStore()
        let originalStart = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let reinstallDate = mobileLicenseUTCDate(year: 2026, month: 7, day: 15)
        try? credentialStore.saveToken(
            MobileLicenseTrialStore.formattedTrialStartDate(originalStart),
            account: MobileLicenseTrialStore.trialAccount
        )

        let snapshot = MobileLicenseTrialStore.resolvedSnapshot(
            snapshot: MobileLicenseSnapshot(),
            credentialStore: credentialStore,
            now: reinstallDate
        )
        let decision = MobileLicenseEvaluator.evaluate(snapshot: snapshot, now: reinstallDate)

        XCTAssertEqual(snapshot.trialStartDate, originalStart)
        XCTAssertEqual(decision.trialEndsAt, mobileLicenseUTCDate(year: 2026, month: 8, day: 3))
    }

    func testPaidUpdatePolicyCopyIncludesCutoffAndBugFixLanguage() {
        let copy = MobileLicenseCopy.paidUpdatePolicy(cutoffDate: mobileLicenseUTCDate(year: 2027, month: 6, day: 3))

        XCTAssertTrue(copy.hasPrefix("One-time unlock includes features released through "))
        XCTAssertTrue(copy.contains("Paid updates unlock later feature releases."))
        XCTAssertTrue(copy.contains("Bug fixes/security fixes remain included."))
    }

    func testProductStatesShowActiveTrial() throws {
        let snapshot = MobileLicenseSnapshot(trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3))
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
        )

        let states = MobileLicenseProductStateBuilder.states(for: decision)
        let trial = try XCTUnwrap(states.first { $0.kind == .trial })

        XCTAssertEqual(trial.title, "Trial")
        XCTAssertTrue(trial.isActive)
        XCTAssertTrue(trial.detail.contains("Free use ends"))
        XCTAssertTrue(trial.detail.contains("2026"))
    }

    func testProductStatesShowLifetimeDuringActiveTrial() throws {
        let snapshot = MobileLicenseSnapshot(
            trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3),
            transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.lifetimeUnlockID,
                    purchaseDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 10)
                ),
            ]
        )
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
        )

        let states = MobileLicenseProductStateBuilder.states(for: decision)
        let trial = try XCTUnwrap(states.first { $0.kind == .trial })
        let lifetime = try XCTUnwrap(states.first { $0.kind == .lifetimeUnlocked })

        XCTAssertEqual(decision.reason, .trial)
        XCTAssertTrue(trial.isActive)
        XCTAssertTrue(lifetime.isActive)
        XCTAssertEqual(lifetime.title, "Lifetime unlocked")
    }

    func testProductStatesShowPaidUpdateWindowDate() throws {
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.lifetimeUnlockID,
                    purchaseDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
                ),
            ]
        )
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
        )

        let states = MobileLicenseProductStateBuilder.states(for: decision)
        let paidUpdateWindow = try XCTUnwrap(states.first { $0.kind == .paidUpdateWindow })

        XCTAssertTrue(paidUpdateWindow.isActive)
        XCTAssertTrue(paidUpdateWindow.title.hasPrefix("Paid-update window through "))
        XCTAssertTrue(paidUpdateWindow.title.contains("2027"))
        XCTAssertTrue(paidUpdateWindow.detail.contains("Features released on or before this date are included."))
    }

    func testProductStatesAlwaysShowNewFeaturesLockedPolicyRow() throws {
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.lifetimeUnlockID,
                    purchaseDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
                ),
            ]
        )
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
        )

        let states = MobileLicenseProductStateBuilder.states(for: decision)
        let locked = try XCTUnwrap(states.first { $0.kind == .newFeaturesLocked })

        XCTAssertEqual(locked.title, "New features locked until update")
        XCTAssertFalse(locked.isActive)
        XCTAssertTrue(locked.detail.contains("Feature releases after the paid-update window require a paid update."))
        XCTAssertTrue(locked.detail.contains("Bug fixes/security fixes remain included."))
    }

    func testProductStatesMarkFutureFeaturesLockedAfterPaidWindow() throws {
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.lifetimeUnlockID,
                    purchaseDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
                ),
            ]
        )
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
        )
        let futureFeature = MobileLicenseFeature(
            id: .widgets,
            displayName: "Future Widgets",
            releaseDate: mobileLicenseUTCDate(year: 2027, month: 6, day: 4)
        )

        let states = MobileLicenseProductStateBuilder.states(for: decision, features: [futureFeature])
        let locked = try XCTUnwrap(states.first { $0.kind == .newFeaturesLocked })

        XCTAssertTrue(locked.isActive)
        XCTAssertTrue(locked.detail.contains("Future Widgets"))
    }

    func testCachedTransactionsDecodeWithPurchasedOwnershipDefault() throws {
        let json = """
        {
          "trialStartDate": null,
          "transactions": [
            {
              "productID": "org.jpfchang.clambhook.unlock.lifetime",
              "purchaseDate": "2026-06-03T00:00:00Z"
            }
          ],
          "lastVerifiedAt": null,
          "lastVerificationFailedAt": null,
          "cachedAt": "2026-06-03T00:00:00Z"
        }
        """

        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let snapshot = try decoder.decode(MobileLicenseSnapshot.self, from: Data(json.utf8))

        XCTAssertEqual(snapshot.transactions.first?.ownershipType, .purchased)
    }
}
