package listener

import (
	"os"
	"regexp"
)

// ifaceNamePattern accepts only conservative interface-name characters. Values
// parsed from kernel/OS output (e.g. `ip route get`, `route -n get`) are
// validated against it before reaching exec, so an unexpected token can never
// be interpreted as an option or shell metacharacter.
var ifaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9._][A-Za-z0-9._-]*$`)

// validIfaceName reports whether name is a plausible network-interface name.
func validIfaceName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	return ifaceNamePattern.MatchString(name)
}

// resolveCommandPath returns the first existing candidate absolute path, or the
// last candidate as a fallback. Privileged network commands are invoked by
// absolute path so an attacker-influenced $PATH cannot redirect them to a
// planted binary.
func resolveCommandPath(candidates ...string) string {
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	return candidates[len(candidates)-1]
}
