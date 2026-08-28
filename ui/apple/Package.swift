// swift-tools-version: 6.1
// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

import PackageDescription

let package = Package(
    name: "ClambhookApple",
    platforms: [
        .macOS(.v14),
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
