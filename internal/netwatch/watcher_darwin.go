//go:build darwin

package netwatch

import (
	"log"
	"os/exec"
	"strings"
	"sync"
)

func current() (NetworkInfo, error) {
	iface, err := primaryInterface()
	if err != nil || iface == "" {
		return NetworkInfo{}, err
	}
	ssid, isWiFi := resolveSSID(iface)
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

// resolveSSID determines the SSID for iface using supported macOS sources,
// degrading gracefully so interface-only triggers keep working when the SSID
// cannot be read. It returns the SSID (empty if unavailable) and whether iface
// is a Wi-Fi interface.
//
// macOS 14 (Sonoma) withholds the SSID from `networksetup -getairportnetwork`
// and `ipconfig getsummary` unless the calling process holds Location Services
// authorization, and it removed the private `airport` tool. When every source
// withholds the SSID we log an explicit warning naming the missing
// authorization instead of silently reporting "no SSID".
func resolveSSID(iface string) (string, bool) {
	airportText, _ := runAirport(iface)
	ssid, isWiFi, associated := parseAirportSSID(airportText)
	if !isWiFi {
		return "", false
	}
	if associated && ssid != "" {
		noteSSIDResolved(iface)
		return ssid, true
	}
	// networksetup withheld the SSID: try the ipconfig summary, which exposes
	// an "SSID :" line when the process is permitted to read it.
	if s, ok := parseIPConfigSSID(runIPConfigSummary(iface)); ok {
		noteSSIDResolved(iface)
		return s, true
	}
	warnSSIDUnavailable(iface)
	return "", true
}

func runAirport(iface string) (string, error) {
	out, err := exec.Command("networksetup", "-getairportnetwork", iface).CombinedOutput()
	return string(out), err
}

func runIPConfigSummary(iface string) string {
	out, err := exec.Command("ipconfig", "getsummary", iface).CombinedOutput()
	if err != nil {
		return ""
	}
	return string(out)
}

// parseAirportSSID interprets `networksetup -getairportnetwork` output. It
// reports the SSID, whether the interface is a Wi-Fi interface at all, and
// whether it is currently associated with a network.
func parseAirportSSID(text string) (ssid string, isWiFi bool, associated bool) {
	text = strings.TrimSpace(text)
	const prefix = "Current Wi-Fi Network: "
	if strings.HasPrefix(text, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(text, prefix)), true, true
	}
	// e.g. "en5 is not a Wi-Fi interface." — a wired/other interface.
	if strings.Contains(text, "not a Wi-Fi interface") {
		return "", false, false
	}
	// Wi-Fi interface, but SSID unavailable: not associated, or macOS 14+
	// withholding it ("You are not associated with an AirPort network.").
	return "", true, false
}

// parseIPConfigSSID extracts the SSID from `ipconfig getsummary` output. macOS
// prints the SSID as "<redacted>" when Location authorization is missing, which
// is treated as unavailable.
func parseIPConfigSSID(text string) (string, bool) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "SSID") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "SSID"))
		if !strings.HasPrefix(rest, ":") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(rest, ":"))
		if value == "" || value == "<redacted>" {
			return "", false
		}
		return value, true
	}
	return "", false
}

// ssidWarnMu guards ssidUnavailable so the SSID-unavailable warning is emitted
// once per unavailability transition per interface rather than on every poll.
var (
	ssidWarnMu     sync.Mutex
	ssidUnavailable = map[string]bool{}
)

func warnSSIDUnavailable(iface string) {
	ssidWarnMu.Lock()
	defer ssidWarnMu.Unlock()
	if ssidUnavailable[iface] {
		return
	}
	ssidUnavailable[iface] = true
	log.Printf("netwatch: SSID unavailable for Wi-Fi interface %q; "+
		"macOS 14+ withholds the SSID without Location Services authorization "+
		"(grant the daemon Location access to enable SSID triggers). "+
		"Interface-only triggers still apply.", iface)
}

func noteSSIDResolved(iface string) {
	ssidWarnMu.Lock()
	defer ssidWarnMu.Unlock()
	delete(ssidUnavailable, iface)
}
