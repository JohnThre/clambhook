package openvpn

import (
	"bytes"
	"testing"

	"github.com/JohnThre/clambhook/pkg/cnet"
)

// makeKeys builds symmetric keyMaterial where client and server share
// the same cipher key and implicit IV — so one dataChannel can seal and
// another can open the resulting packet. Real deployments have
// asymmetric keys; this fake just lets us test the framing.
func makeKeys() *keyMaterial {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	iv := make([]byte, 8)
	for i := range iv {
		iv[i] = byte(0xA0 | i)
	}
	return &keyMaterial{
		clientCipherKey:  key,
		clientImplicitIV: iv,
		serverCipherKey:  key, // symmetric so client's seal can be opened as server data
		serverImplicitIV: iv,
	}
}

func TestDataChannelSealOpenAES256GCM(t *testing.T) {
	if !cnet.AES256GCMAvailable() {
		t.Skip("AES-256-GCM not available on this host")
	}
	km := makeKeys()
	sender := newDataChannel("AES-256-GCM", 0, km)
	sender.setPeerID(42)
	receiver := newDataChannel("AES-256-GCM", 0, km)
	receiver.setPeerID(42)

	plaintext := []byte("hello openvpn data channel over AES-256-GCM")
	packet, err := sender.seal(plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	// Prefix byte should be opcode P_DATA_V2 with key_id 0.
	op, keyID := splitOpByte(packet[0])
	if op != OpcodeDataV2 || keyID != 0 {
		t.Fatalf("bad prefix: op=%d key=%d", op, keyID)
	}
	got, err := receiver.open(packet)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch: %q vs %q", got, plaintext)
	}
}

func TestDataChannelSealOpenChaCha(t *testing.T) {
	km := makeKeys()
	sender := newDataChannel("CHACHA20-POLY1305", 0, km)
	receiver := newDataChannel("CHACHA20-POLY1305", 0, km)

	plaintext := []byte("hello openvpn data channel over chacha")
	packet, err := sender.seal(plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	got, err := receiver.open(packet)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch")
	}
}

func TestDataChannelReplayRejectsDuplicate(t *testing.T) {
	km := makeKeys()
	sender := newDataChannel("CHACHA20-POLY1305", 0, km)
	receiver := newDataChannel("CHACHA20-POLY1305", 0, km)

	packet, err := sender.seal([]byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.open(packet); err != nil {
		t.Fatalf("first open: %v", err)
	}
	// Replay — same ciphertext, different receiver session state should
	// trip the sliding window.
	if _, err := receiver.open(packet); err == nil {
		t.Fatal("expected replay to be rejected")
	}
}

func TestDataChannelReplayWindowOld(t *testing.T) {
	km := makeKeys()
	sender := newDataChannel("CHACHA20-POLY1305", 0, km)
	receiver := newDataChannel("CHACHA20-POLY1305", 0, km)

	// Burn through enough packet IDs that the first is outside the 64-id
	// window by the time receiver sees the later ones.
	var first []byte
	for i := 0; i < 80; i++ {
		p, err := sender.seal([]byte{byte(i)})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = p
		}
		if i >= 20 { // only feed later ones to receiver, simulating loss of first 20
			if _, err := receiver.open(p); err != nil {
				t.Fatalf("open[%d]: %v", i, err)
			}
		}
	}
	// Now try to replay the very first one — should be below window.
	if _, err := receiver.open(first); err == nil {
		t.Fatal("expected old packet to be rejected")
	}
}

func TestDataChannelOpenRejectsTampered(t *testing.T) {
	km := makeKeys()
	sender := newDataChannel("CHACHA20-POLY1305", 0, km)
	receiver := newDataChannel("CHACHA20-POLY1305", 0, km)

	packet, err := sender.seal([]byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	// Flip a bit in the ciphertext — AEAD should reject.
	tampered := append([]byte(nil), packet...)
	tampered[dataV2PrefixLen+4] ^= 0x01
	if _, err := receiver.open(tampered); err == nil {
		t.Fatal("expected AEAD failure on tampered ciphertext")
	}
	// Crucially, the replay window should NOT have advanced — feeding
	// the original (unmodified) packet next must still succeed.
	if _, err := receiver.open(packet); err != nil {
		t.Fatalf("legitimate packet rejected after tampered attempt: %v", err)
	}
}
