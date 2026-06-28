import ClambhookShared
import Foundation

private let onboardingCompletedKey = "clambhook.mac.onboarding.completed"

enum OnboardingStep: Int, CaseIterable {
    case welcome
    case routingMode
    case profileImport
    case httpsCA
    case done
}

@MainActor
final class OnboardingManager: ObservableObject {
    @Published private(set) var currentStep: OnboardingStep = .welcome
    @Published private(set) var isComplete: Bool

    private let defaults: UserDefaults

    init(defaults: UserDefaults = UserDefaults(suiteName: defaultAppGroupIdentifier) ?? .standard) {
        self.defaults = defaults
        self.isComplete = defaults.bool(forKey: onboardingCompletedKey)
    }

    func advance() {
        let allSteps = OnboardingStep.allCases
        guard let idx = allSteps.firstIndex(of: currentStep) else { return }
        let next = idx + 1
        if next < allSteps.count {
            currentStep = allSteps[next]
        }
    }

    func back() {
        let allSteps = OnboardingStep.allCases
        guard let idx = allSteps.firstIndex(of: currentStep), idx > 0 else { return }
        currentStep = allSteps[idx - 1]
    }

    func complete() {
        defaults.set(true, forKey: onboardingCompletedKey)
        isComplete = true
    }

    func reset() {
        defaults.removeObject(forKey: onboardingCompletedKey)
        currentStep = .welcome
        isComplete = false
    }
}
