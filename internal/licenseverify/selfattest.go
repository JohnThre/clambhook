package licenseverify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// SelfAttestation returns the running binary's SHA-256 hash and, on macOS, the
// code-signing authority identity. The backend checks these against the
// clambhook_genuine_hashes allowlist for the reported (platform, app_version):
// a non-empty hash not in the allowlist for a known version triggers a
// "cracked" ban. The result is cached per process.
//
// This is the strongest attestation layer the plan calls for: it flags a
// patched/modified binary (different hash) or a re-signed macOS build (different
// code-sign identity). A determined cracker can lie about these, but it stops
// casual cracks and feeds the real-time ban decision for the legitimate build.
var (
	selfOnce     sync.Once
	selfHash     string
	selfCodeSign *string
)

func SelfAttestation() (binaryHash string, codeSignIdentity *string) {
	selfOnce.Do(func() {
		selfHash = computeBinaryHash()
		if runtime.GOOS == "darwin" {
			if id, ok := darwinCodeSignAuthority(); ok {
				selfCodeSign = &id
			}
		}
	})
	return selfHash, selfCodeSign
}

func computeBinaryHash() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	f, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	const chunk = 1 << 20
	buf := make([]byte, chunk)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// darwinCodeSignAuthority runs `codesign -dv --verbose=4 <exe>` with a timeout
// and parses the first "Authority=..." line from its combined output. Returns
// false if the binary is not signed or codesign is unavailable (non-darwin
// callers never reach here).
func darwinCodeSignAuthority() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codesign", "-dv", "--verbose=4", exe)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "Authority="); ok {
			return strings.TrimSpace(after), true
		}
	}
	return "", false
}

// resetSelfAttestForTest clears the cached result so a test can re-run the
// attestation (e.g. with a patched binary). For tests only.
func resetSelfAttestForTest() {
	selfOnce = sync.Once{}
	selfHash = ""
	selfCodeSign = nil
}
