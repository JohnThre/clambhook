//go:build darwin

package listener

func platformDefaultTUNName() string { return "utun" }
