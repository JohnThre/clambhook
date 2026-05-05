// socks5_udp is a self-contained end-to-end probe that exercises the
// SOCKS5 UDP ASSOCIATE path: it opens a SOCKS5 session, binds a local UDP
// socket, sends a DNS A-query through the relay to a user-specified DNS
// server, and prints the resolved addresses.
//
// Deliberately duplicates the SOCKS5 UDP codec (~30 lines) instead of
// importing from internal/listener so test/e2e stays free of reaching
// into package-private APIs.
package main

import (
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"
)

func main() {
	proxy := flag.String("proxy", "127.0.0.1:1080", "SOCKS5 proxy TCP addr")
	target := flag.String("target", "8.8.8.8:53", "UDP target (IPv4:port)")
	host := flag.String("host", "example.com", "hostname to resolve")
	flag.Parse()

	if err := run(*proxy, *target, *host); err != nil {
		log.Fatal(err)
	}
}

func run(proxy, target, host string) error {
	ctrl, err := net.DialTimeout("tcp", proxy, 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial proxy: %w", err)
	}
	defer ctrl.Close()
	_ = ctrl.SetDeadline(time.Now().Add(5 * time.Second))

	if _, err := ctrl.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return fmt.Errorf("write greeting: %w", err)
	}
	gr := make([]byte, 2)
	if _, err := io.ReadFull(ctrl, gr); err != nil {
		return fmt.Errorf("read greeting reply: %w", err)
	}
	if gr[0] != 0x05 || gr[1] != 0x00 {
		return fmt.Errorf("bad greeting reply: %x", gr)
	}

	if _, err := ctrl.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return fmt.Errorf("write UDP ASSOCIATE: %w", err)
	}
	rp := make([]byte, 10)
	if _, err := io.ReadFull(ctrl, rp); err != nil {
		return fmt.Errorf("read UDP ASSOCIATE reply: %w", err)
	}
	if rp[1] != 0x00 {
		return fmt.Errorf("UDP ASSOCIATE rejected: rep=%#x", rp[1])
	}
	bndPort := binary.BigEndian.Uint16(rp[8:10])
	relay := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(bndPort)}

	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		return fmt.Errorf("listen udp: %w", err)
	}
	defer udp.Close()
	_ = udp.SetDeadline(time.Now().Add(5 * time.Second))

	targetHost, targetPortStr, err := net.SplitHostPort(target)
	if err != nil {
		return fmt.Errorf("bad target: %w", err)
	}
	targetIP := net.ParseIP(targetHost).To4()
	if targetIP == nil {
		return fmt.Errorf("only IPv4 targets supported: %q", targetHost)
	}
	targetPort, err := strconv.Atoi(targetPortStr)
	if err != nil {
		return fmt.Errorf("bad target port: %w", err)
	}

	dnsQ, err := buildDNSQuery(host)
	if err != nil {
		return fmt.Errorf("build dns query: %w", err)
	}
	dg := encodeSOCKS5UDP(targetIP, uint16(targetPort), dnsQ)

	if _, err := udp.WriteTo(dg, relay); err != nil {
		return fmt.Errorf("udp write: %w", err)
	}

	buf := make([]byte, 4096)
	n, _, err := udp.ReadFromUDP(buf)
	if err != nil {
		return fmt.Errorf("udp read: %w", err)
	}
	payload, err := decodeSOCKS5UDP(buf[:n])
	if err != nil {
		return fmt.Errorf("decode reply: %w", err)
	}
	ips, err := parseDNSResponse(payload)
	if err != nil {
		return fmt.Errorf("parse dns: %w", err)
	}
	fmt.Printf("%s →", host)
	for _, ip := range ips {
		fmt.Printf(" %s", ip)
	}
	fmt.Println()
	return nil
}

func buildDNSQuery(host string) ([]byte, error) {
	buf := []byte{0x12, 0x34, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			continue
		}
		if len(label) > 63 {
			return nil, fmt.Errorf("label too long: %q", label)
		}
		buf = append(buf, byte(len(label)))
		buf = append(buf, []byte(label)...)
	}
	buf = append(buf, 0x00, 0, 1, 0, 1)
	return buf, nil
}

func encodeSOCKS5UDP(ip net.IP, port uint16, payload []byte) []byte {
	out := []byte{0, 0, 0, 0x01}
	out = append(out, ip...)
	out = append(out, byte(port>>8), byte(port))
	out = append(out, payload...)
	return out
}

func decodeSOCKS5UDP(buf []byte) ([]byte, error) {
	if len(buf) < 10 {
		return nil, errors.New("short datagram")
	}
	if buf[2] != 0 {
		return nil, fmt.Errorf("FRAG=%d not supported", buf[2])
	}
	idx := 4
	switch buf[3] {
	case 0x01:
		idx += 4
	case 0x04:
		idx += 16
	case 0x03:
		if idx >= len(buf) {
			return nil, errors.New("short domain")
		}
		idx += 1 + int(buf[idx])
	default:
		return nil, fmt.Errorf("bad ATYP %#x", buf[3])
	}
	idx += 2
	if idx > len(buf) {
		return nil, errors.New("truncated header")
	}
	return buf[idx:], nil
}

func parseDNSResponse(msg []byte) ([]string, error) {
	if len(msg) < 12 {
		return nil, errors.New("dns too short")
	}
	qdCount := binary.BigEndian.Uint16(msg[4:6])
	anCount := binary.BigEndian.Uint16(msg[6:8])
	idx := 12
	for i := 0; i < int(qdCount); i++ {
		for {
			if idx >= len(msg) {
				return nil, errors.New("truncated question")
			}
			l := int(msg[idx])
			if l == 0 {
				idx++
				break
			}
			if l&0xc0 == 0xc0 {
				idx += 2
				break
			}
			idx += 1 + l
		}
		idx += 4
	}
	var ips []string
	for i := 0; i < int(anCount); i++ {
		if idx >= len(msg) {
			return nil, errors.New("truncated answer name")
		}
		if msg[idx]&0xc0 == 0xc0 {
			idx += 2
		} else {
			for {
				if idx >= len(msg) {
					return nil, errors.New("bad name")
				}
				l := int(msg[idx])
				idx++
				if l == 0 {
					break
				}
				idx += l
			}
		}
		if idx+10 > len(msg) {
			return nil, errors.New("truncated rr header")
		}
		typ := binary.BigEndian.Uint16(msg[idx : idx+2])
		rdlen := binary.BigEndian.Uint16(msg[idx+8 : idx+10])
		idx += 10
		if idx+int(rdlen) > len(msg) {
			return nil, errors.New("truncated rdata")
		}
		if typ == 1 && rdlen == 4 {
			ips = append(ips, net.IPv4(msg[idx], msg[idx+1], msg[idx+2], msg[idx+3]).String())
		}
		idx += int(rdlen)
	}
	if len(ips) == 0 {
		return nil, errors.New("no A records in response")
	}
	return ips, nil
}
