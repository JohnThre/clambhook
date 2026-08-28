// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package vmess

import (
	"bytes"
	"net"
	"testing"
)

func TestEncodeAddr(t *testing.T) {
	cases := []struct {
		name string
		addr string
		want []byte
	}{
		{
			name: "ipv4",
			addr: "1.2.3.4:80",
			want: []byte{0x00, 0x50, atypIPv4, 1, 2, 3, 4},
		},
		{
			name: "domain",
			addr: "example.com:443",
			want: append([]byte{0x01, 0xbb, atypDomain, byte(len("example.com"))}, []byte("example.com")...),
		},
		{
			name: "ipv6",
			addr: "[::1]:53",
			want: append([]byte{0x00, 0x35, atypIPv6}, net16(t, "::1")...),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := encodeAddr(tc.addr)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Errorf("encodeAddr(%q) = %x, want %x", tc.addr, got, tc.want)
			}
		})
	}
}

func TestEncodeAddrErrors(t *testing.T) {
	for _, addr := range []string{"noport", "host:99999", "host:abc"} {
		if _, err := encodeAddr(addr); err == nil {
			t.Errorf("encodeAddr(%q) expected error", addr)
		}
	}
}

func net16(t *testing.T, s string) []byte {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("parse ip %q", s)
	}
	return ip.To16()
}
