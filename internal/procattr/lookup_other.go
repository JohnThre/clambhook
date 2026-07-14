//go:build !darwin && !linux

package procattr

// lookup is unsupported on platforms without a process/socket attribution
// backend; attribution is silently disabled.
func lookup(network, source string) (Process, bool) {
	return Process{}, false
}
