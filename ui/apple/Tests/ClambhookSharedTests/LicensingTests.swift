import XCTest
@testable import ClambhookShared

final class LicensingTests: XCTestCase {
    func testTrialUsesOneCalendarMonth() {
        let start = mobileLicenseUTCDate(year: 2026, month: 1, day: 31)
        let snapshot = MobileLicenseSnapshot(trialStartDate: start)

        let beforeExpiry = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 2, day: 27)
        )
        XCTAssertEqual(beforeExpiry.reason, .trial)
        XCTAssertEqual(beforeExpiry.trialEndsAt, mobileLicenseUTCDate(year: 2026, month: 2, day: 28))
        XCTAssertTrue(beforeExpiry.canUseFeature(.tunnelRouting))

        let atExpiry = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 2, day: 28)
        )
        XCTAssertEqual(atExpiry.reason, .locked)
        XCTAssertFalse(atExpiry.canUseApp)
    }

    func testTrialEndDateClampsToTargetMonthLastDay() {
        XCTAssertEqual(
            mobileLicenseTrialEndDate(start: mobileLicenseUTCDate(year: 2025, month: 12, day: 31)),
            mobileLicenseUTCDate(year: 2026, month: 1, day: 31)
        )
        XCTAssertEqual(
            mobileLicenseTrialEndDate(start: mobileLicenseUTCDate(year: 2023, month: 12, day: 31)),
            mobileLicenseUTCDate(year: 2024, month: 1, day: 31)
        )
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

    func testLockedWidgetActionsCannotStartOrSwitchRoutingButCanDisconnect() {
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)),
            now: mobileLicenseUTCDate(year: 2026, month: 8, day: 4)
        )

        XCTAssertFalse(WidgetLicenseActionPolicy.isAllowed(.connect, decision: decision))
        XCTAssertFalse(WidgetLicenseActionPolicy.isAllowed(.nextProfile, decision: decision))
        XCTAssertTrue(WidgetLicenseActionPolicy.isAllowed(.disconnect, decision: decision))
    }

    func testUnlockedWidgetActionsCanStartSwitchAndDisconnect() {
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)),
            now: mobileLicenseUTCDate(year: 2026, month: 6, day: 4)
        )

        XCTAssertTrue(WidgetLicenseActionPolicy.isAllowed(.connect, decision: decision))
        XCTAssertTrue(WidgetLicenseActionPolicy.isAllowed(.nextProfile, decision: decision))
        XCTAssertTrue(WidgetLicenseActionPolicy.isAllowed(.disconnect, decision: decision))
    }

    func testMacLicenseRemainsUsableWithoutRecentVerification() {
        let purchaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.macLicenseProductID, purchaseDate: purchaseDate),
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

    func testRecentVerificationFailureUsesOfflineGraceForCachedMacLicense() {
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
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.macLicenseProductID, purchaseDate: purchaseDate),
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
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.macLicenseProductID, purchaseDate: purchaseDate),
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

    func testRevokedMacLicenseDoesNotUnlock() {
        let purchaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.macLicenseProductID,
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

    func testPaidUpdatesExtendFeatureWindow() throws {
        let licensePurchaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.macLicenseProductID, purchaseDate: licensePurchaseDate),
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.featureUpdateProductID, purchaseDate: mobileLicenseUTCDate(year: 2027, month: 8, day: 1)),
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

    func testPaidUpdateBeforeCurrentCutoffExtendsFromCurrentCutoff() {
        let licensePurchaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let transactions = [
            MobileLicenseTransaction(
                productID: MobilePurchaseCatalog.macLicenseProductID,
                purchaseDate: licensePurchaseDate
            ),
            MobileLicenseTransaction(
                productID: MobilePurchaseCatalog.featureUpdateProductID,
                purchaseDate: mobileLicenseUTCDate(year: 2027, month: 1, day: 15)
            ),
        ]

        XCTAssertEqual(
            MobileLicenseEvaluator.updateCutoffDate(
                lifetimePurchaseDate: licensePurchaseDate,
                transactions: transactions
            ),
            mobileLicenseUTCDate(year: 2028, month: 6, day: 3)
        )
    }

    func testMultiplePaidUpdateYearsGateFeaturesByReleaseDate() {
        let licensePurchaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let featureUpdate2028ID = MobilePurchaseCatalog.featureUpdateProductID
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.macLicenseProductID, purchaseDate: licensePurchaseDate),
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.featureUpdateProductID, purchaseDate: mobileLicenseUTCDate(year: 2027, month: 8, day: 1)),
                MobileLicenseTransaction(productID: featureUpdate2028ID, purchaseDate: mobileLicenseUTCDate(year: 2028, month: 8, day: 15)),
            ],
            lastVerifiedAt: mobileLicenseUTCDate(year: 2028, month: 8, day: 15)
        )
        let firstPaidYearFeature = MobileLicenseFeature(
            id: .widgets,
            displayName: "First Paid Year Widgets",
            releaseDate: mobileLicenseUTCDate(year: 2028, month: 8, day: 1)
        )
        let finalCutoffFeature = MobileLicenseFeature(
            id: .routingRules,
            displayName: "Final Cutoff Rules",
            releaseDate: mobileLicenseUTCDate(year: 2029, month: 8, day: 15)
        )
        let laterFeature = MobileLicenseFeature(
            id: .activityInspection,
            displayName: "Later Inspection",
            releaseDate: mobileLicenseUTCDate(year: 2029, month: 8, day: 16)
        )

        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            features: [firstPaidYearFeature, finalCutoffFeature, laterFeature],
            now: mobileLicenseUTCDate(year: 2028, month: 8, day: 16)
        )

        XCTAssertEqual(decision.reason, .lifetime)
        XCTAssertEqual(decision.updateCutoffDate, mobileLicenseUTCDate(year: 2029, month: 8, day: 15))
        XCTAssertTrue(decision.canUseFeature(.widgets))
        XCTAssertTrue(decision.canUseFeature(.routingRules))
        XCTAssertFalse(decision.canUseFeature(.activityInspection))
    }

    func testRefundedPaidUpdateDoesNotExtendFeatureWindow() {
        let licensePurchaseDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.macLicenseProductID, purchaseDate: licensePurchaseDate),
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.featureUpdateProductID,
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

    func testPaidUpdateWithoutMacLicenseDoesNotUnlock() {
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.featureUpdateProductID, purchaseDate: mobileLicenseUTCDate(year: 2027, month: 8, day: 1)),
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

    func testUpdatePolicyAllowsOnlyDatedReleasesThroughLicensedCutoff() {
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.macLicenseProductID,
                    purchaseDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
                ),
            ]),
            now: mobileLicenseUTCDate(year: 2028, month: 1, day: 1)
        )

        XCTAssertTrue(MobileLicenseUpdatePolicy.canInstallUpdate(
            decision: decision,
            publishedAt: mobileLicenseUTCDate(year: 2027, month: 6, day: 3)
        ))
        XCTAssertFalse(MobileLicenseUpdatePolicy.canInstallUpdate(
            decision: decision,
            publishedAt: mobileLicenseUTCDate(year: 2027, month: 6, day: 4)
        ))
    }

    func testUpdatePolicyFailsClosedForUndatedReleaseAfterLicensedCutoff() {
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.macLicenseProductID,
                    purchaseDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
                ),
            ]),
            now: mobileLicenseUTCDate(year: 2028, month: 1, day: 1)
        )

        XCTAssertTrue(MobileLicenseUpdatePolicy.canInstallUpdate(
            decision: decision,
            publishedAt: nil,
            now: mobileLicenseUTCDate(year: 2027, month: 6, day: 3)
        ))
        XCTAssertFalse(MobileLicenseUpdatePolicy.canInstallUpdate(
            decision: decision,
            publishedAt: nil,
            now: mobileLicenseUTCDate(year: 2027, month: 6, day: 4)
        ))
    }

    func testUpdatePolicyAllowsTrialAndDeniesLockedTrial() {
        let activeTrial = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(
                trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
            ),
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 2)
        )
        let expiredTrial = MobileLicenseEvaluator.evaluate(
            snapshot: MobileLicenseSnapshot(
                trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
            ),
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 3)
        )

        XCTAssertTrue(MobileLicenseUpdatePolicy.canInstallUpdate(
            decision: activeTrial,
            publishedAt: nil
        ))
        XCTAssertFalse(MobileLicenseUpdatePolicy.canInstallUpdate(
            decision: expiredTrial,
            publishedAt: mobileLicenseUTCDate(year: 2026, month: 6, day: 4)
        ))
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
        XCTAssertEqual(decision.trialEndsAt, mobileLicenseUTCDate(year: 2026, month: 7, day: 3))
    }

    func testPaidUpdatePolicyCopyIncludesStrictCutoffLanguage() {
        let copy = MobileLicenseCopy.paidUpdatePolicy(cutoffDate: mobileLicenseUTCDate(year: 2027, month: 6, day: 3))

        XCTAssertTrue(copy.hasPrefix("The ClambHook license includes all updates released through "))
        XCTAssertTrue(copy.contains("Versions released during that window remain usable."))
        XCTAssertTrue(copy.contains("including critical, bug, and security updates"))
        XCTAssertTrue(copy.contains("USD 9.99 update-year renewal"))
    }

    func testProductStatesShowActiveTrial() throws {
        let snapshot = MobileLicenseSnapshot(trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3))
        let decision = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
        )

        let states = MobileLicenseProductStateBuilder.states(for: decision)
        let trial = try XCTUnwrap(states.first { $0.kind == .trial })

        XCTAssertEqual(trial.title, "One-calendar-month trial")
        XCTAssertTrue(trial.isActive)
        XCTAssertTrue(trial.detail.contains("Trial ends"))
        XCTAssertTrue(trial.detail.contains("2026"))
    }

    func testProductStatesShowMacLicenseDuringActiveTrial() throws {
        let snapshot = MobileLicenseSnapshot(
            trialStartDate: mobileLicenseUTCDate(year: 2026, month: 6, day: 3),
            transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.macLicenseProductID,
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
        let macLicense = try XCTUnwrap(states.first { $0.kind == .lifetimeUnlocked })

        XCTAssertEqual(decision.reason, .trial)
        XCTAssertTrue(trial.isActive)
        XCTAssertTrue(macLicense.isActive)
        XCTAssertEqual(macLicense.title, "ClambHook license")
    }

    func testProductStatesShowPaidUpdateWindowDate() throws {
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.macLicenseProductID,
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
        XCTAssertTrue(paidUpdateWindow.title.hasPrefix("Included updates through "))
        XCTAssertTrue(paidUpdateWindow.title.contains("2027"))
        XCTAssertTrue(paidUpdateWindow.detail.contains("All updates released on or before this date are included"))
    }

    func testProductStatesAlwaysShowNewFeaturesLockedPolicyRow() throws {
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.macLicenseProductID,
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

        XCTAssertEqual(locked.title, "Later updates require renewal")
        XCTAssertFalse(locked.isActive)
        XCTAssertTrue(locked.detail.contains("All updates released after the cutoff"))
        XCTAssertTrue(locked.detail.contains("including critical, bug, and security updates"))
    }

    func testProductStatesMarkFutureFeaturesLockedAfterPaidWindow() throws {
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(
                    productID: MobilePurchaseCatalog.macLicenseProductID,
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

    func testCachedTransactionsDecodeDirectSaleFields() throws {
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

        XCTAssertEqual(snapshot.transactions.first?.productID, MobilePurchaseCatalog.macLicenseProductID)
    }

    func testDeviceStateHonorsTenActiveDeviceLimit() {
        let state = MobileLicenseDeviceState(
            currentInstallID: "install-11",
            devices: (1...10).map { licenseDevice(id: "device-\($0)", installID: "install-\($0)") }
        )

        XCTAssertEqual(state.maxActiveDevices, 10)
        XCTAssertEqual(state.activeDeviceCount, 10)
        XCTAssertEqual(state.remainingActivations, 0)
        XCTAssertFalse(state.canActivateCurrentDevice)
        XCTAssertFalse(state.canReactivateCurrentDevice)
    }

    func testDeviceStateCannotRaiseConcurrentDeviceLimitAboveTen() throws {
        let initialized = MobileLicenseDeviceState(
            currentInstallID: "install-11",
            maxActiveDevices: 25
        )
        let decoded = try JSONDecoder().decode(
            MobileLicenseDeviceState.self,
            from: Data("""
            {
              "current_install_id": "install-11",
              "max_active_devices": 25,
              "devices": []
            }
            """.utf8)
        )

        XCTAssertEqual(initialized.maxActiveDevices, 10)
        XCTAssertEqual(decoded.maxActiveDevices, 10)
    }

    func testCommercialTermsMatchMacLicensePolicy() {
        XCTAssertEqual(MobileLicenseCommercialTerms.licensePriceUSD, "99.99")
        XCTAssertEqual(MobileLicenseCommercialTerms.paidFeatureUpdatePriceUSD, "9.99")
        XCTAssertEqual(MobileLicenseCommercialTerms.includedFeatureUpdateYears, 1)
        XCTAssertEqual(MobileLicenseCommercialTerms.maxActiveDevices, 10)
    }

    func testAcceptedPaymentProvidersAreExactlyCreemAndNOWPayments() {
        XCTAssertEqual(
            MobileLicensePaymentProvider.acceptedPurchaseProviders,
            [.creem, .nowPayments]
        )
        XCTAssertEqual(
            MobileLicensePaymentProvider.acceptedPurchaseProviders.map(\.displayName),
            ["Creem", "NOWPayments"]
        )
        XCTAssertTrue(MobileLicensePaymentProvider.creem.isAcceptedPurchaseProvider)
        XCTAssertTrue(MobileLicensePaymentProvider.nowPayments.isAcceptedPurchaseProvider)
    }

    func testUnsupportedPaymentProvidersDecodeSafelyButAreNotAccepted() throws {
        let decoder = JSONDecoder()

        let legacyManual = try decoder.decode(
            MobileLicensePaymentProvider.self,
            from: Data(#""manual""#.utf8)
        )
        let paypal = try decoder.decode(
            MobileLicensePaymentProvider.self,
            from: Data(#""paypal""#.utf8)
        )
        let futureProvider = try decoder.decode(
            MobileLicensePaymentProvider.self,
            from: Data(#""future-provider""#.utf8)
        )

        XCTAssertEqual(legacyManual, .unsupported("manual"))
        XCTAssertEqual(paypal, .unsupported("paypal"))
        XCTAssertEqual(futureProvider, .unsupported("future-provider"))
        XCTAssertFalse(legacyManual.isAcceptedPurchaseProvider)
        XCTAssertFalse(paypal.isAcceptedPurchaseProvider)
        XCTAssertFalse(futureProvider.isAcceptedPurchaseProvider)
    }

    func testActiveCurrentDeviceCanRemainActiveAtDeviceLimit() {
        let state = MobileLicenseDeviceState(
            currentInstallID: "install-1",
            currentDeviceID: "device-1",
            devices: [
                licenseDevice(id: "device-1", installID: "install-1"),
                licenseDevice(id: "device-2", installID: "install-2"),
                licenseDevice(id: "device-3", installID: "install-3"),
                licenseDevice(id: "device-4", installID: "install-4"),
            ]
        )

        XCTAssertTrue(state.isCurrentDeviceActive)
        XCTAssertTrue(state.canActivateCurrentDevice)
        XCTAssertTrue(state.canTransferCurrentDevice)
    }

    func testReactivationRequiresAvailableSeat() {
        let deactivatedCurrent = licenseDevice(
            id: "device-1",
            installID: "install-1",
            deactivatedAt: mobileLicenseUTCDate(year: 2026, month: 7, day: 1)
        )
        let fullState = MobileLicenseDeviceState(
            currentInstallID: "install-1",
            currentDeviceID: "device-1",
            devices: [deactivatedCurrent] + (2...11).map { licenseDevice(id: "device-\($0)", installID: "install-\($0)") }
        )
        let availableState = MobileLicenseDeviceState(
            currentInstallID: "install-1",
            currentDeviceID: "device-1",
            devices: [deactivatedCurrent] + (2...10).map { licenseDevice(id: "device-\($0)", installID: "install-\($0)") }
        )

        XCTAssertFalse(fullState.canReactivateCurrentDevice)
        XCTAssertTrue(availableState.canReactivateCurrentDevice)
    }

    func testDeviceStateStoreRoundTripsCurrentDeviceAndProvider() throws {
        let defaultsName = "LicensingTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: defaultsName))
        defer { defaults.removePersistentDomain(forName: defaultsName) }
        let state = MobileLicenseDeviceState(
            currentInstallID: "install-1",
            currentDeviceID: "device-1",
            devices: [licenseDevice(id: "device-1", installID: "install-1")],
            paymentProvider: .creem
        )

        MobileLicenseDeviceStateStore.save(state, defaults: defaults)
        let loaded = MobileLicenseDeviceStateStore.load(defaults: defaults)

        XCTAssertEqual(loaded, state)
        XCTAssertEqual(loaded.currentDevice?.deviceID, "device-1")
        XCTAssertEqual(loaded.paymentProvider, .creem)
    }

    private func licenseDevice(
        id: String,
        installID: String,
        deactivatedAt: Date? = nil
    ) -> MobileLicenseDevice {
        MobileLicenseDevice(
            deviceID: id,
            installID: installID,
            displayName: id,
            platform: "macOS",
            architecture: "arm64",
            activatedAt: mobileLicenseUTCDate(year: 2026, month: 6, day: 3),
            lastSeenAt: mobileLicenseUTCDate(year: 2026, month: 6, day: 4),
            deactivatedAt: deactivatedAt
        )
    }
}
