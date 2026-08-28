// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

package vmess

import (
	"fmt"
	"net"
	"strconv"
)

// VMESS request-header address encoding differs from the SOCKS5 triple used by
// trojan/shadowsocks: the port comes first (2 bytes, big-endian), then a 1-byte
// address type, then the address. The ATYP values are also VMESS-specific:
//
//	0x01 IPv4  (4 bytes)
//	0x02 domain (1-byte length prefix + bytes)
//	0x03 IPv6  (16 bytes)
const (
	atypIPv4   byte = 0x01
	atypDomain byte = 0x02
	atypIPv6   byte = 0x03
)

// encodeAddr encodes "host:port" as PORT(2 BE) || ATYP(1) || ADDR. Domains are
// preferred over client-side DNS resolution to avoid leaking the destination
// to the local resolver, matching the socks package convention.
func encodeAddr(address string) ([]byte, error) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("vmess: split host/port %q: %w", address, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return nil, fmt.Errorf("vmess: invalid port %q", portStr)
	}

	out := make([]byte, 0, 2+1+len(host)+1)
	out = append(out, byte(port>>8), byte(port))

	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			out = append(out, atypIPv4)
			out = append(out, v4...)
		} else {
			out = append(out, atypIPv6)
			out = append(out, ip.To16()...)
		}
	} else {
		if len(host) == 0 || len(host) > 255 {
			return nil, fmt.Errorf("vmess: domain length %d out of range", len(host))
		}
		out = append(out, atypDomain, byte(len(host)))
		out = append(out, host...)
	}

	return out, nil
}
