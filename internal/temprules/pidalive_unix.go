//go:build darwin || linux

package temprules

import "syscall"

// defaultPIDAlive reports whether pid is still running on darwin/linux via
// kill(pid, 0). A nil error means the process exists and is signalable; EPERM
// means it exists but belongs to another user (treated as alive); ESRCH means
// it has exited. This is the portable seam behind the "until quit" temporary
// rule's PID-exit watcher.
func defaultPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
