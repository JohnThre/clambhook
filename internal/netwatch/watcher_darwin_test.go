//go:build darwin

package netwatch

import "testing"

func TestParseAirportSSID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		text          string
		wantSSID      string
		wantIsWiFi    bool
		wantAssociate bool
	}{
		{
			name:          "associated",
			text:          "Current Wi-Fi Network: HomeNet\n",
			wantSSID:      "HomeNet",
			wantIsWiFi:    true,
			wantAssociate: true,
		},
		{
			name:       "not a wifi interface",
			text:       "en5 is not a Wi-Fi interface.\n",
			wantIsWiFi: false,
		},
		{
			// macOS 14+ withholds the SSID without Location authorization.
			name:       "withheld sonoma",
			text:       "You are not associated with an AirPort network.\n",
			wantIsWiFi: true,
		},
		{
			name:       "empty output",
			text:       "",
			wantIsWiFi: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ssid, isWiFi, associated := parseAirportSSID(tc.text)
			if ssid != tc.wantSSID || isWiFi != tc.wantIsWiFi || associated != tc.wantAssociate {
				t.Fatalf("parseAirportSSID(%q) = (%q, %v, %v), want (%q, %v, %v)",
					tc.text, ssid, isWiFi, associated,
					tc.wantSSID, tc.wantIsWiFi, tc.wantAssociate)
			}
		})
	}
}

func TestParseIPConfigSSID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
		want string
		ok   bool
	}{
		{
			name: "present",
			text: "  Wi-Fi:\n    SSID : HomeNet\n    BSSID : aa:bb\n",
			want: "HomeNet",
			ok:   true,
		},
		{
			name: "redacted without permission",
			text: "    SSID : <redacted>\n",
		},
		{
			name: "absent",
			text: "    IPv4 : 10.0.0.2\n",
		},
		{
			// "SSIDs" must not be mistaken for the "SSID :" field.
			name: "similar prefix ignored",
			text: "    SSIDsSeen : 3\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseIPConfigSSID(tc.text)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("parseIPConfigSSID(%q) = (%q, %v), want (%q, %v)", tc.text, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// The unavailability warning is emitted once per unavailability transition and
// re-armed after the SSID is resolved again.
func TestWarnSSIDUnavailableDedup(t *testing.T) {
	iface := "test-en-dedup"
	ssidWarnMu.Lock()
	delete(ssidUnavailable, iface)
	ssidWarnMu.Unlock()

	warnSSIDUnavailable(iface)
	ssidWarnMu.Lock()
	armed := ssidUnavailable[iface]
	ssidWarnMu.Unlock()
	if !armed {
		t.Fatal("expected iface to be marked unavailable after first warning")
	}

	noteSSIDResolved(iface)
	ssidWarnMu.Lock()
	_, stillTracked := ssidUnavailable[iface]
	ssidWarnMu.Unlock()
	if stillTracked {
		t.Fatal("expected iface warning state cleared after SSID resolved")
	}
}
