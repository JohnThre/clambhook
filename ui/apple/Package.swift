// swift-tools-version: 5.9

import PackageDescription

let package = Package(
    name: "ClambhookApple",
    platforms: [
        .macOS(.v14),
        .iOS(.v17),
        .visionOS(.v1),
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
    ]
)
