package reality

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/JohnThre/clambhook/internal/protocol"
)

// 32-byte sample key — purely arbitrary for testing.
var samplePubHex = "0102030405060708091011121314151617181920212223242526272829303132"

func sampleServer(settings map[string]any) protocol.Server {
	return protocol.Server{
		Name:     "t",
		Address:  "reality.example.com:443",
		Protocol: "reality",
		Settings: settings,
	}
}

func TestParseOptions_HappyPath_Hex(t *testing.T) {
	opts, err := ParseOptions(sampleServer(map[string]any{
		"public_key":  samplePubHex,
		"short_id":    "a1b2c3d4",
		"server_name": "www.microsoft.com",
		"fingerprint": "chrome",
		"alpn":        []any{"h2", "http/1.1"},
	}))
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.ServerName != "www.microsoft.com" {
		t.Errorf("ServerName: got %q", opts.ServerName)
	}
	want, _ := hex.DecodeString(samplePubHex)
	if string(opts.PublicKey[:]) != string(want) {
		t.Errorf("PublicKey mismatch")
	}
	// short_id is right-zero-padded
	wantSID, _ := hex.DecodeString("a1b2c3d400000000")
	if string(opts.ShortID[:]) != string(wantSID) {
		t.Errorf("ShortID: got %x want %x", opts.ShortID, wantSID)
	}
	if len(opts.ALPN) != 2 || opts.ALPN[0] != "h2" {
		t.Errorf("ALPN: %v", opts.ALPN)
	}
}

func TestParseOptions_HappyPath_Base64URL(t *testing.T) {
	raw, _ := hex.DecodeString(samplePubHex)
	b64 := base64.RawURLEncoding.EncodeToString(raw)
	opts, err := ParseOptions(sampleServer(map[string]any{
		"public_key":  b64,
		"server_name": "example.com",
	}))
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if string(opts.PublicKey[:]) != string(raw) {
		t.Errorf("PublicKey mismatch from base64-url")
	}
}

func TestParseOptions_ServerNameFallsBackToHost(t *testing.T) {
	opts, err := ParseOptions(sampleServer(map[string]any{
		"public_key": samplePubHex,
	}))
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.ServerName != "reality.example.com" {
		t.Errorf("ServerName fallback: got %q", opts.ServerName)
	}
}

func TestParseOptions_EmptyShortIDIsValid(t *testing.T) {
	opts, err := ParseOptions(sampleServer(map[string]any{
		"public_key": samplePubHex,
	}))
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	var zero [8]byte
	if opts.ShortID != zero {
		t.Errorf("empty short_id should yield zero array, got %x", opts.ShortID)
	}
}

func TestParseOptions_ShortIDTooLong(t *testing.T) {
	_, err := ParseOptions(sampleServer(map[string]any{
		"public_key": samplePubHex,
		"short_id":   "0102030405060708aa", // 9 bytes
	}))
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("want too-long error, got %v", err)
	}
}

func TestParseOptions_MissingPublicKey(t *testing.T) {
	_, err := ParseOptions(sampleServer(map[string]any{}))
	if err == nil || !strings.Contains(err.Error(), "public_key") {
		t.Errorf("want public_key required error, got %v", err)
	}
}

func TestParseOptions_BadPublicKey(t *testing.T) {
	_, err := ParseOptions(sampleServer(map[string]any{
		"public_key": "not-hex-or-b64",
	}))
	if err == nil {
		t.Fatalf("expected error for malformed public_key")
	}
}

func TestParseOptions_PublicKeyWrongLength(t *testing.T) {
	// 16 bytes hex → should reject (expected 32)
	_, err := ParseOptions(sampleServer(map[string]any{
		"public_key": "0102030405060708091011121314151617",
	}))
	if err == nil {
		t.Fatalf("expected error for wrong-length public_key")
	}
}

func TestParseOptions_UnknownFingerprint(t *testing.T) {
	_, err := ParseOptions(sampleServer(map[string]any{
		"public_key":  samplePubHex,
		"fingerprint": "netscape_navigator",
	}))
	if err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Errorf("want fingerprint error, got %v", err)
	}
}

func TestResolveFingerprint_Known(t *testing.T) {
	for _, name := range []string{
		"", "chrome", "firefox", "safari", "ios", "android",
		"edge", "360", "qq", "random", "randomized",
		"randomizednoalpn", "randomizedalpn",
	} {
		if _, err := resolveFingerprint(name); err != nil {
			t.Errorf("resolveFingerprint(%q): %v", name, err)
		}
	}
}
