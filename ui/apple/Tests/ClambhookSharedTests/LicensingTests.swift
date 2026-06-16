import XCTest
@testable import ClambhookShared

final class LicensingTests: XCTestCase {
    func testTrialUsesTwoCalendarMonths() {
        let start = mobileLicenseUTCDate(year: 2026, month: 1, day: 31)
        let snapshot = MobileLicenseSnapshot(trialStartDate: start)

        let beforeExpiry = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 3, day: 30)
        )
        XCTAssertEqual(beforeExpiry.reason, .trial)
        XCTAssertEqual(beforeExpiry.trialEndsAt, mobileLicenseUTCDate(year: 2026, month: 3, day: 31))
        XCTAssertTrue(beforeExpiry.canUseFeature(.tunnelRouting))

        let atExpiry = MobileLicenseEvaluator.evaluate(
            snapshot: snapshot,
            now: mobileLicenseUTCDate(year: 2026, month: 3, day: 31)
        )
        XCTAssertEqual(atExpiry.reason, .locked)
        XCTAssertFalse(atExpiry.canUseApp)
    }

    func testTrialEndDateClampsToTargetMonthLastDay() {
        XCTAssertEqual(
            mobileLicenseTrialEndDate(start: mobileLicenseUTCDate(year: 2025, month: 12, day: 31)),
            mobileLicenseUTCDate(year: 2026, month: 2, day: 28)
        )
        XCTAssertEqual(
            mobileLicenseTrialEndDate(start: mobileLicenseUTCDate(year: 2023, month: 12, day: 31)),
            mobileLicenseUTCDate(year: 2024, month: 2, day: 29)
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

    func testMultiplePaidUpdateYearsGateFeaturesByReleaseDate() {
        let lifetimeDate = mobileLicenseUTCDate(year: 2026, month: 6, day: 3)
        let featureUpdate2028ID = "\(MobilePurchaseCatalog.featureUpdatePrefix)2028"
        let snapshot = MobileLicenseSnapshot(
            transactions: [
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.lifetimeUnlockID, purchaseDate: lifetimeDate),
                MobileLicenseTransaction(productID: MobilePurchaseCatalog.featureUpdate2027ID, purchaseDate: mobileLicenseUTCDate(year: 2027, month: 8, day: 1)),
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

        XCTAssertEqual(trial.title, "Free access")
        XCTAssertTrue(trial.isActive)
        XCTAssertTrue(trial.detail.contains("Server-controlled free access ends"))
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

    func testDeviceStateHonorsFourActiveDeviceLimit() {
        let state = MobileLicenseDeviceState(
            currentInstallID: "install-5",
            devices: [
                licenseDevice(id: "device-1", installID: "install-1"),
                licenseDevice(id: "device-2", installID: "install-2"),
                licenseDevice(id: "device-3", installID: "install-3"),
                licenseDevice(id: "device-4", installID: "install-4"),
            ]
        )

        XCTAssertEqual(state.maxActiveDevices, 4)
        XCTAssertEqual(state.activeDeviceCount, 4)
        XCTAssertEqual(state.remainingActivations, 0)
        XCTAssertFalse(state.canActivateCurrentDevice)
        XCTAssertFalse(state.canReactivateCurrentDevice)
    }

    func testCommercialTermsMatchMacLicensePolicy() {
        XCTAssertEqual(MobileLicenseCommercialTerms.lifetimePriceUSD, "99.99")
        XCTAssertEqual(MobileLicenseCommercialTerms.paidFeatureUpdatePriceUSD, "8.99")
        XCTAssertEqual(MobileLicenseCommercialTerms.includedFeatureUpdateYears, 1)
        XCTAssertEqual(MobileLicenseCommercialTerms.maxActiveDevices, 4)
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
            devices: [
                deactivatedCurrent,
                licenseDevice(id: "device-2", installID: "install-2"),
                licenseDevice(id: "device-3", installID: "install-3"),
                licenseDevice(id: "device-4", installID: "install-4"),
                licenseDevice(id: "device-5", installID: "install-5"),
            ]
        )
        let availableState = MobileLicenseDeviceState(
            currentInstallID: "install-1",
            currentDeviceID: "device-1",
            devices: [
                deactivatedCurrent,
                licenseDevice(id: "device-2", installID: "install-2"),
                licenseDevice(id: "device-3", installID: "install-3"),
                licenseDevice(id: "device-4", installID: "install-4"),
            ]
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
