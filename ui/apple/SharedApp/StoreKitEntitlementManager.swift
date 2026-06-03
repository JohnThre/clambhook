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

    private let defaults: UserDefaults
    private let credentialStore: CredentialStoring
    private let trialAccount = "trial-start-date"
    private var transactionUpdatesTask: Task<Void, Never>?
    private var started = false

    init(
        defaults: UserDefaults = UserDefaults(suiteName: defaultAppGroupIdentifier) ?? .standard,
        credentialStore: CredentialStoring = KeychainCredentialStore(service: "org.jpfchang.clambhook.license")
    ) {
        self.defaults = defaults
        self.credentialStore = credentialStore
        let initialSnapshot = MobileLicenseSnapshotStore.load(defaults: defaults)
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
        ensureTrialStarted()
        observeTransactionUpdates()
        Task {
            await refreshProducts()
            await refreshCurrentEntitlements()
        }
    }

    func refreshProducts() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let fetched = try await Product.products(for: MobilePurchaseCatalog.productIDs)
            products = MobilePurchaseCatalog.orderedIDs(fetched.map(\.id)).compactMap { id in
                fetched.first { $0.id == id }
            }
            if products.isEmpty {
                statusMessage = "Purchases are not available yet."
            }
        } catch {
            markVerificationFailure(error)
        }
    }

    func refreshCurrentEntitlements() async {
        do {
            var transactions: [MobileLicenseTransaction] = []
            for await result in Transaction.currentEntitlements {
                if let transaction = verifiedTransaction(from: result) {
                    transactions.append(licenseTransaction(from: transaction))
                }
            }
            applyVerifiedTransactions(transactions, message: "Purchases refreshed.")
        } catch {
            markVerificationFailure(error)
        }
    }

    func purchase(_ product: Product) async {
        purchasingProductIDs.insert(product.id)
        statusMessage = ""
        defer { purchasingProductIDs.remove(product.id) }
        do {
            let result = try await product.purchase()
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
            for await result in Transaction.all {
                if let transaction = verifiedTransaction(from: result),
                   MobilePurchaseCatalog.productKind(for: transaction.productID) != .unknown {
                    transactions.append(licenseTransaction(from: transaction))
                }
            }
            applyVerifiedTransactions(transactions, message: "Purchase history repaired.")
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

    private func ensureTrialStarted(now: Date = Date()) {
        var next = snapshot
        if let stored = try? credentialStore.readToken(account: trialAccount),
           let date = Self.dateFormatter.date(from: stored) {
            next.trialStartDate = date
        } else if let existing = snapshot.trialStartDate {
            try? credentialStore.saveToken(Self.dateFormatter.string(from: existing), account: trialAccount)
            next.trialStartDate = existing
        } else {
            try? credentialStore.saveToken(Self.dateFormatter.string(from: now), account: trialAccount)
            next.trialStartDate = now
        }
        next.cachedAt = now
        save(next)
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
            revocationDate: transaction.revocationDate
        )
    }

    private static let dateFormatter: ISO8601DateFormatter = {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return formatter
    }()
}
#endif
