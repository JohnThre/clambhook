//go:build linux

package netwatch

import (
	"bufio"
	"net"
	"os"
	"strings"
)

func current() (NetworkInfo, error) {
	// Try /proc/net/wireless first for WiFi interface name.
	if info, ok := fromProcWireless(); ok {
		return info, nil
	}
	// Fall back to first up non-loopback interface.
	ifaces, err := net.Interfaces()
	if err != nil {
		return NetworkInfo{}, err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		return NetworkInfo{InterfaceName: iface.Name}, nil
	}
	return NetworkInfo{}, nil
}

func fromProcWireless() (NetworkInfo, bool) {
	f, err := os.Open("/proc/net/wireless")
	if err != nil {
		return NetworkInfo{}, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= 2 {
			continue // skip header lines
		}
		parts := strings.Fields(scanner.Text())
		if len(parts) < 2 {
			continue
		}
		iface := strings.TrimSuffix(parts[0], ":")
		// SSID is not exposed by /proc/net/wireless; use interface name only.
		return NetworkInfo{InterfaceName: iface, IsWiFi: true}, true
	}
	return NetworkInfo{}, false
}
