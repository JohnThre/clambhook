import ClambhookShared
import Combine
import Foundation

#if os(iOS)
import StoreKit

@MainActor
final class StoreKitEntitlementManager: ObservableObject {
    @Published private(set) var products: [Product] = []
    @Published private(set) var snapshot: MobileLicenseSnapshot
    @Published private(set) var decision: MobileLicenseDecision
    @Published private(set) var isLoading = false
    @Published private(set) var statusMessage = ""
    @Published private(set) var purchasingProductIDs: Set<String> = []
    @Published private(set) var purchaseAvailability: StoreKitAvailability = .unknown

    private let defaults: UserDefaults
    private let credentialStore: CredentialStoring
    private let licenseClient: LicenseValidationClient
    private var transactionUpdatesTask: Task<Void, Never>?
    private var started = false

    init(
        defaults: UserDefaults = UserDefaults(suiteName: defaultAppGroupIdentifier) ?? .standard,
        credentialStore: CredentialStoring = KeychainCredentialStore(service: "org.jpfchang.clambhook.license"),
        licenseValidationEndpoint: URL = defaultLicenseValidationURL
    ) {
        self.defaults = defaults
        self.credentialStore = credentialStore
        self.licenseClient = LicenseValidationClient(
            endpoint: licenseValidationEndpoint,
            defaults: defaults,
            credentialStore: credentialStore
        )
        let initialSnapshot = Self.initialSnapshot(defaults: defaults)
        self.snapshot = initialSnapshot
        self.decision = MobileLicenseEvaluator.evaluate(snapshot: initialSnapshot)
    }

    deinit {
        transactionUpdatesTask?.cancel()
    }

    func start() {
        guard !started else {
            refreshDecision()
            return
        }
        started = true
        ensureLocalTrialStartedForDebugBuilds()
        observeTransactionUpdates()
        Task {
            await refreshProducts()
            await refreshCurrentEntitlements()
        }
    }

    func refreshProducts() async {
        isLoading = true
        purchaseAvailability = .loading
        defer { isLoading = false }
        do {
            let fetched = try await Product.products(for: MobilePurchaseCatalog.productIDs)
            products = MobilePurchaseCatalog.orderedIDs(fetched.map(\.id)).compactMap { id in
                fetched.first { $0.id == id }
            }
            if products.isEmpty {
                let message = "Purchases are not available yet."
                statusMessage = message
                purchaseAvailability = .unavailable(message)
            } else {
                purchaseAvailability = .available
            }
        } catch {
            let message = error.localizedDescription
            markVerificationFailure(error)
            purchaseAvailability = .unavailable(message)
        }
    }

    func refreshCurrentEntitlements() async {
        do {
            var transactions: [MobileLicenseTransaction] = []
            var transactionJWS: [String] = []
            for await result in Transaction.currentEntitlements {
                if let transaction = verifiedTransaction(from: result) {
                    transactions.append(licenseTransaction(from: transaction))
                    transactionJWS.append(result.jwsRepresentation)
                }
            }
            await applyServerValidation(localTransactions: transactions, transactionJWS: transactionJWS, message: "Purchases refreshed.")
        } catch {
            markVerificationFailure(error)
        }
    }

    func purchase(_ product: Product) async {
        purchasingProductIDs.insert(product.id)
        statusMessage = ""
        defer { purchasingProductIDs.remove(product.id) }
        do {
            let result = try await product.purchase(options: licenseClient.purchaseOptions())
            switch result {
            case .success(let verification):
                guard let transaction = verifiedTransaction(from: verification) else {
                    statusMessage = "The purchase could not be verified."
                    return
                }
                await transaction.finish()
                await repairPurchaseHistory()
            case .pending:
                statusMessage = "Purchase pending approval."
            case .userCancelled:
                statusMessage = "Purchase cancelled."
            @unknown default:
                statusMessage = "Purchase state is not supported by this app version."
            }
        } catch {
            statusMessage = error.localizedDescription
        }
    }

    func restorePurchases() async {
        isLoading = true
        defer { isLoading = false }
        do {
            try await AppStore.sync()
            await refreshCurrentEntitlements()
            statusMessage = decision.hasLifetimeUnlock ? "Purchases restored." : "No lifetime unlock was found."
        } catch {
            markVerificationFailure(error)
        }
    }

    func repairPurchaseHistory() async {
        isLoading = true
        defer { isLoading = false }
        do {
            var transactions: [MobileLicenseTransaction] = []
            var transactionJWS: [String] = []
            for await result in Transaction.all {
                if let transaction = verifiedTransaction(from: result),
                   MobilePurchaseCatalog.productKind(for: transaction.productID) != .unknown {
                    transactions.append(licenseTransaction(from: transaction))
                    transactionJWS.append(result.jwsRepresentation)
                }
            }
            await applyServerValidation(localTransactions: transactions, transactionJWS: transactionJWS, message: "Purchase history repaired.")
        } catch {
            markVerificationFailure(error)
        }
    }

    func refreshDecision(now: Date = Date()) {
        decision = MobileLicenseEvaluator.evaluate(snapshot: snapshot, now: now)
    }

    private func observeTransactionUpdates() {
        transactionUpdatesTask?.cancel()
        transactionUpdatesTask = Task { [weak self] in
            for await result in Transaction.updates {
                guard let self else { return }
                await self.handle(transactionUpdate: result)
            }
        }
    }

    private func handle(transactionUpdate result: VerificationResult<Transaction>) async {
        guard let transaction = verifiedTransaction(from: result) else {
            statusMessage = "A purchase update could not be verified."
            return
        }
        await transaction.finish()
        await repairPurchaseHistory()
    }

    private func ensureLocalTrialStartedForDebugBuilds(now: Date = Date()) {
        #if DEBUG
        save(MobileLicenseTrialStore.resolvedSnapshot(snapshot: snapshot, credentialStore: credentialStore, now: now))
        #endif
    }

    private func applyServerValidation(localTransactions transactions: [MobileLicenseTransaction], transactionJWS: [String], message: String) async {
        do {
            let response = try await licenseClient.refreshGrant(transactionJWS: transactionJWS)
            MobileServerLicenseGrantStore.save(response.grant, defaults: defaults)
            var next = response.snapshot.licenseSnapshot
            if next.transactions.isEmpty, !transactions.isEmpty {
                next.transactions = transactions
            }
            let now = Date()
            next.lastVerifiedAt = now
            next.lastVerificationFailedAt = nil
            next.cachedAt = now
            save(next)
            statusMessage = message
            return
        } catch {
            #if DEBUG
            applyVerifiedTransactions(transactions, message: "\(message) Local debug license fallback is active.")
            #else
            markVerificationFailure(error)
            #endif
        }
    }

    private func applyVerifiedTransactions(_ transactions: [MobileLicenseTransaction], message: String) {
        var deduped: [String: MobileLicenseTransaction] = [:]
        for transaction in transactions where transaction.productKind != .unknown {
            if let existing = deduped[transaction.productID] {
                deduped[transaction.productID] = transaction.purchaseDate >= existing.purchaseDate ? transaction : existing
            } else {
                deduped[transaction.productID] = transaction
            }
        }
        var next = snapshot
        next.transactions = MobilePurchaseCatalog.orderedIDs(deduped.keys).compactMap { deduped[$0] }
        let now = Date()
        next.lastVerifiedAt = now
        next.lastVerificationFailedAt = nil
        next.cachedAt = now
        save(next)
        statusMessage = message
    }

    private func markVerificationFailure(_ error: Error) {
        var next = snapshot
        next.lastVerificationFailedAt = Date()
        next.cachedAt = Date()
        save(next)
        statusMessage = error.localizedDescription
    }

    private func save(_ next: MobileLicenseSnapshot) {
        snapshot = next
        MobileLicenseSnapshotStore.save(next, defaults: defaults)
        refreshDecision()
    }

    private func verifiedTransaction(from result: VerificationResult<Transaction>) -> Transaction? {
        switch result {
        case .verified(let transaction):
            return transaction
        case .unverified:
            return nil
        }
    }

    private func licenseTransaction(from transaction: Transaction) -> MobileLicenseTransaction {
        MobileLicenseTransaction(
            productID: transaction.productID,
            purchaseDate: transaction.purchaseDate,
            revocationDate: transaction.revocationDate,
            ownershipType: transaction.ownershipType == .familyShared ? .familyShared : .purchased
        )
    }

    private static func initialSnapshot(defaults: UserDefaults) -> MobileLicenseSnapshot {
        if let grant = MobileServerLicenseGrantStore.load(defaults: defaults), grant.expiresAt > Date() {
            return MobileLicenseSnapshot(
                trialStartDate: grant.trialStartDate,
                transactions: grant.transactions,
                lastVerifiedAt: grant.issuedAt,
                lastVerificationFailedAt: nil,
                cachedAt: grant.issuedAt
            )
        }
        return MobileLicenseSnapshotStore.load(defaults: defaults)
    }
}
#endif
