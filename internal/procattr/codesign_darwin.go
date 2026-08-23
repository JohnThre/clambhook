//go:build darwin

package procattr

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// codeSignCache memoizes the codesign Authority + status per executable path so
// repeated connections from the same process do not re-shell-out to codesign.
var codeSignCache sync.Map // map[string]codeSignResult

type codeSignResult struct {
	id     string
	status string
}

// codeSignTimeout bounds the codesign lookup. The enrichment is display-only
// (alert detail), so a miss is preferable to blocking the route decision long.
const codeSignTimeout = 2 * time.Second

// enrichCodeSign resolves the owning executable's code-signing identity into
// p.CodeSignID / p.CodeSignStatus on darwin, using a per-path cache. It is
// best-effort: on any error it leaves the fields empty so the prompt still
// surfaces. It must not be called with p.Path empty.
func enrichCodeSign(p *Process) {
	if p == nil || p.Path == "" {
		return
	}
	if cached, ok := codeSignCache.Load(p.Path); ok {
		r := cached.(codeSignResult)
		p.CodeSignID = r.id
		p.CodeSignStatus = r.status
		return
	}
	r := resolveCodeSign(p.Path)
	codeSignCache.Store(p.Path, r)
	p.CodeSignID = r.id
	p.CodeSignStatus = r.status
}

// resolveCodeSign shells out to `codesign -dv --verbose=4 <path>`. codesign
// prints verification info (including the Authority= line) to stderr and exits
// 0 when the signature is valid. A non-zero exit with a "not signed" message
// yields status "unsigned"; any other failure yields a short status string.
func resolveCodeSign(path string) codeSignResult {
	ctx, cancel := context.WithTimeout(context.Background(), codeSignTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/codesign", "-dv", "--verbose=4", path)
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err == nil {
		return codeSignResult{id: parseCodeSignAuthority(text), status: "valid"}
	}
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "not signed") || strings.Contains(lower, "code object is not signed at all"):
		return codeSignResult{status: "unsigned"}
	case ctx.Err() != nil:
		return codeSignResult{status: "timeout"}
	default:
		return codeSignResult{status: "error"}
	}
}

// parseCodeSignAuthority extracts the Authority= line from codesign output,
// which carries the signing identity shown in the alert detail.
func parseCodeSignAuthority(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Authority=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Authority="))
		}
	}
	return ""
}
