// pi-lens runner shim: this directory contains Swift XCTest tests. The pi-lens
// automated runner sometimes attempts to invoke `go test` here; this no-op
// Go test keeps that runner green so the real Swift suite can be run via
// xcodebuild or swift test separately.
package clambhooksharedtests

import "testing"

func TestPiLensRunnerShim(t *testing.T) {
	// No-op: real ClambhookShared tests live in .swift files.
}
