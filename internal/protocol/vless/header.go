package vless

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/JohnThre/clambhook/internal/protocol/v2ray"
	"github.com/google/uuid"
)

const (
	version byte = 0x00
	cmdTCP  byte = 0x01
	cmdUDP  byte = 0x02
)

// encodeRequest builds the VLESS client request header:
//
//	ver(1) | uuid(16) | addon_len(1)=0 | cmd(1) | port(2 BE) | atyp(1) | addr(...)
//
// Byte order differs from the V2Ray address codec: the VLESS header puts
// PORT before ATYP||ADDR, so we build the port field manually and then
// append the codec's ATYP||ADDR bytes (stripping the trailing port that
// v2ray.EncodeAddr would have emitted).
//
// We chose not to add a "port-less" variant to v2ray.EncodeAddr because
// VMess uses the same quirk and both sites are small — keeping the shared
// codec focused on the full triple is simpler than sprouting a second API.
func encodeRequest(id uuid.UUID, cmd byte, address string) ([]byte, error) {
	_, portStr, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("vless: split host/port %q: %w", address, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return nil, fmt.Errorf("vless: invalid port %q", portStr)
	}

	// We need ATYP||ADDR without the trailing port. Encode the full triple
	// via v2ray.EncodeAddr and drop its last two bytes — this keeps a single
	// source of truth for ATYP values and domain-length validation.
	triple, err := v2ray.EncodeAddr(address)
	if err != nil {
		return nil, err
	}
	atypAddr := triple[:len(triple)-2]

	out := make([]byte, 0, 1+16+1+1+2+len(atypAddr))
	out = append(out, version)
	out = append(out, id[:]...)
	out = append(out, 0x00) // no addons (flow=none)
	out = append(out, cmd)
	out = binary.BigEndian.AppendUint16(out, uint16(port))
	out = append(out, atypAddr...)
	return out, nil
}

// readResponse consumes the fixed VLESS response prefix:
//
//	ver(1) | addon_len(1) | addons(N)
//
// Version is not checked beyond being read (the reference implementations
// echo the request version but the spec allows future servers to diverge).
// Addons are skipped — we don't advertise any, and the protocol requires us
// to tolerate server-supplied metadata even when we didn't ask for it.
func readResponse(r io.Reader) error {
	var buf [2]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return fmt.Errorf("vless: read response header: %w", err)
	}
	addonLen := int(buf[1])
	if addonLen > 0 {
		addons := make([]byte, addonLen)
		if _, err := io.ReadFull(r, addons); err != nil {
			return fmt.Errorf("vless: read response addons: %w", err)
		}
	}
	return nil
}
