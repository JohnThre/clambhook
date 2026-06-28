//go:build !linux && !darwin

package listener

func platformDefaultTUNName() string { return "clambhook0" }
