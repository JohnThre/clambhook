package openvpn

import (
	"bytes"
	"testing"
)

func TestOpByteRoundTrip(t *testing.T) {
	for opcode := byte(0); opcode < 32; opcode++ {
		for keyID := byte(0); keyID < 8; keyID++ {
			b := opByte(opcode, keyID)
			gotOp, gotKey := splitOpByte(b)
			if gotOp != opcode || gotKey != keyID {
				t.Fatalf("opcode=%d key=%d → byte %#x → op=%d key=%d", opcode, keyID, b, gotOp, gotKey)
			}
		}
	}
}

func TestControlPacketRoundTrip(t *testing.T) {
	var lsid sessionID
	for i := range lsid {
		lsid[i] = byte(0x10 + i)
	}
	var rsid sessionID
	for i := range rsid {
		rsid[i] = byte(0x20 + i)
	}

	tests := []struct {
		name string
		p    *controlPacket
	}{
		{
			name: "HARD_RESET_CLIENT_V2 no acks",
			p: &controlPacket{
				opcode:         OpcodeControlHardResetClientV2,
				keyID:          0,
				localSessionID: lsid,
				packetID:       1,
			},
		},
		{
			name: "CONTROL_V1 with ACKs",
			p: &controlPacket{
				opcode:          OpcodeControlV1,
				keyID:           0,
				localSessionID:  lsid,
				remoteSessionID: rsid,
				ackedIDs:        []packetID{1, 2, 3},
				packetID:        4,
				payload:         []byte("hello tls"),
			},
		},
		{
			name: "ACK-only",
			p: &controlPacket{
				opcode:          OpcodeAckV1,
				keyID:           0,
				localSessionID:  lsid,
				remoteSessionID: rsid,
				ackedIDs:        []packetID{42},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf, err := encodeControl(tc.p)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, err := decodeControl(buf)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.opcode != tc.p.opcode {
				t.Errorf("opcode: %d vs %d", got.opcode, tc.p.opcode)
			}
			if got.keyID != tc.p.keyID {
				t.Errorf("keyID: %d vs %d", got.keyID, tc.p.keyID)
			}
			if got.localSessionID != tc.p.localSessionID {
				t.Errorf("localSID mismatch")
			}
			if len(got.ackedIDs) != len(tc.p.ackedIDs) {
				t.Fatalf("ack count: %d vs %d", len(got.ackedIDs), len(tc.p.ackedIDs))
			}
			for i, id := range got.ackedIDs {
				if id != tc.p.ackedIDs[i] {
					t.Errorf("ackedIDs[%d]: %d vs %d", i, id, tc.p.ackedIDs[i])
				}
			}
			if len(tc.p.ackedIDs) > 0 && got.remoteSessionID != tc.p.remoteSessionID {
				t.Errorf("remoteSID mismatch")
			}
			if !tc.p.isAck() {
				if got.packetID != tc.p.packetID {
					t.Errorf("packetID: %d vs %d", got.packetID, tc.p.packetID)
				}
				if !bytes.Equal(got.payload, tc.p.payload) {
					t.Errorf("payload mismatch")
				}
			}
		})
	}
}

func TestDecodeControlRejectsNonControlOpcode(t *testing.T) {
	// opcode 9 = P_DATA_V2; decodeControl should reject it.
	bad := []byte{opByte(OpcodeDataV2, 0)}
	if _, err := decodeControl(bad); err == nil {
		t.Fatal("expected error for data opcode")
	}
}

func TestDataV2PrefixRoundTrip(t *testing.T) {
	cases := []struct {
		keyID  byte
		peerID uint32
	}{
		{0, 0},
		{3, 1234567},
		{7, 0xFFFFFF},
	}
	for _, tc := range cases {
		out := dataV2Prefix(tc.keyID, tc.peerID)
		k, p, err := parseDataV2Prefix(out[:])
		if err != nil {
			t.Fatalf("parseDataV2Prefix: %v", err)
		}
		if k != tc.keyID {
			t.Errorf("keyID: %d vs %d", k, tc.keyID)
		}
		if p != tc.peerID {
			t.Errorf("peerID: %d vs %d", p, tc.peerID)
		}
	}
}

func TestDecodeControlTruncated(t *testing.T) {
	// Empty, single-byte, and truncated-at-session-id should all error.
	for _, data := range [][]byte{
		nil,
		{},
		{opByte(OpcodeControlV1, 0)},
		append([]byte{opByte(OpcodeControlV1, 0)}, make([]byte, 5)...), // half a session id
	} {
		if _, err := decodeControl(data); err == nil {
			t.Errorf("expected error for truncated input %d bytes", len(data))
		}
	}
}
