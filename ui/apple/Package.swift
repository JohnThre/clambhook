// swift-tools-version: 6.2

import PackageDescription

let package = Package(
    name: "ClambhookApple",
    platforms: [
        .macOS(.v26),
        .iOS(.v17),
        .tvOS(.v26),
        .visionOS(.v26),
    ],
    products: [
        .library(name: "ClambhookShared", targets: ["ClambhookShared"]),
    ],
    targets: [
        .target(
            name: "ClambhookShared",
            path: "Sources/ClambhookShared"
        ),
        .testTarget(
            name: "ClambhookSharedTests",
            dependencies: ["ClambhookShared"],
            path: "Tests/ClambhookSharedTests"
        ),
    ],
    swiftLanguageModes: [.v5]
)
