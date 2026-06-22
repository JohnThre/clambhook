//go:build darwin

package netwatch

import (
	"os/exec"
	"strings"
)

func current() (NetworkInfo, error) {
	iface, err := primaryInterface()
	if err != nil || iface == "" {
		return NetworkInfo{}, err
	}
	ssid, isWiFi := wifiSSID(iface)
	return NetworkInfo{
		InterfaceName: iface,
		SSID:          ssid,
		IsWiFi:        isWiFi,
	}, nil
}

// primaryInterface returns the primary network interface from scutil --nwi.
func primaryInterface() (string, error) {
	out, err := exec.Command("scutil", "--nwi").Output()
	if err != nil {
		return "", nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// Lines like "   interface[0] : en0"
		if strings.HasPrefix(line, "interface") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]), nil
			}
		}
	}
	return "", nil
}

// wifiSSID returns the SSID for a WiFi interface using networksetup.
func wifiSSID(iface string) (string, bool) {
	out, err := exec.Command("networksetup", "-getairportnetwork", iface).Output()
	if err != nil {
		return "", false
	}
	// Output: "Current Wi-Fi Network: MySSID" or not-associated message.
	text := strings.TrimSpace(string(out))
	const prefix = "Current Wi-Fi Network: "
	if strings.HasPrefix(text, prefix) {
		return strings.TrimPrefix(text, prefix), true
	}
	return "", false
}
