// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

// Package e2e contains ClambHook's opt-in real-server end-to-end tests.
//
// Build and run with the `e2e` tag: `go test -tags e2e ./test/e2e`. The tests
// require real backends (sing-box, Tor, ClambBack) and the CLAMBHOOK_E2E=1
// environment variable; without them they skip. This file has no e2e build
// constraint and carries no tests, so a tag-less `go test ./test/e2e` reports
// "no test files" rather than a build-constraint setup failure.
// See test/e2e/README.md and docs/release-validation.md.
package e2e
