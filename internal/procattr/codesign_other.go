//go:build !darwin

package procattr

// enrichCodeSign is a no-op on non-darwin platforms: code-signing identity is a
// macOS-only enrichment surfaced in the interactive-prompt alert detail. The
// fields stay empty, so the alert simply omits the code-sign row.
func enrichCodeSign(p *Process) {}
