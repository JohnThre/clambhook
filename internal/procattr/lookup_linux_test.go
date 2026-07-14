//go:build linux

package procattr

import (
	"strings"
	"testing"
)

const procNetSample = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 987654 1 0000000000000000 100 0 0 10 0
   1: 0100007F:D431 5DB8D8AD:01BB 01 00000000:00000000 02:00000000 00000000  1000        0 123456 2 0000000000000000 20 4 30 10 -1
`

func TestParseProcNet(t *testing.T) {
	// 0x1F90 = 8080 (listening), 0xD431 = 54321 (established client socket).
	if inode, ok := parseProcNet(strings.NewReader(procNetSample), 8080); !ok || inode != "987654" {
		t.Errorf("parseProcNet(8080) = (%q, %v), want (987654, true)", inode, ok)
	}
	if inode, ok := parseProcNet(strings.NewReader(procNetSample), 54321); !ok || inode != "123456" {
		t.Errorf("parseProcNet(54321) = (%q, %v), want (123456, true)", inode, ok)
	}
	if _, ok := parseProcNet(strings.NewReader(procNetSample), 9999); ok {
		t.Errorf("parseProcNet(9999) matched unexpectedly")
	}
}
