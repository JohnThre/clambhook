package reality

import (
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/crypto/hkdf"
)

// sessionIDOffset is the byte offset of the 32-byte session_id field
// within a marshaled ClientHello:
//
//	1   handshake type
//	3   handshake length
//	2   legacy_version
//	32  random
//	1   session_id length (must be 32 — enforced by choosing uTLS
//	    fingerprints that emit a 32-byte session_id, and disabling
//	    session tickets so uTLS doesn't shorten it)
//	                                     = 39
//	32  session_id
const sessionIDOffset = 39

// Client performs a Reality TLS handshake over raw and returns the
// authenticated connection. On success the returned net.Conn is a
// *utls.UConn, usable for TLS reads/writes; on failure raw is closed by
// the caller chain (we do not close raw here — the caller owns it, in
// the same spirit as tls.Client).
//
// Reality is strict TLS 1.3 only: if the chosen fingerprint does not
// produce a TLS 1.3 ClientHello (old-Chrome presets, etc.) the handshake
// is rejected before any bytes leave the socket. This matches xray's
// behavior — a non-1.3 Reality attempt would be silently forwarded to
// the decoy.
func Client(ctx context.Context, raw net.Conn, opts Options) (net.Conn, error) {
	fp, err := resolveFingerprint(opts.Fingerprint)
	if err != nil {
		return nil, err
	}

	utlsConfig := &utls.Config{
		ServerName:             opts.ServerName,
		NextProtos:             opts.ALPN,
		InsecureSkipVerify:     true, // Reality validates via its own HMAC-SHA512 check below.
		SessionTicketsDisabled: true, // Keeps session_id a fixed 32 bytes so the sessionIDOffset above holds.
	}

	uConn := utls.UClient(raw, utlsConfig, fp)

	if err := uConn.BuildHandshakeState(); err != nil {
		return nil, fmt.Errorf("reality: build handshake state: %w", err)
	}

	hello := uConn.HandshakeState.Hello
	if hello == nil || len(hello.Raw) < sessionIDOffset+32 {
		return nil, errors.New("reality: ClientHello too short for session_id stuffing")
	}
	if len(hello.Random) != 32 {
		return nil, fmt.Errorf("reality: unexpected client_random length %d", len(hello.Random))
	}

	// 1) Reset the session_id field both in the Hello struct AND in the
	//    marshaled Raw to 32 zeros. The zeroed Raw is what the AEAD will
	//    take as AAD — the server does the same (after stripping the
	//    ciphertext) so the two sides agree on AAD bytes.
	hello.SessionId = make([]byte, 32)
	copy(hello.Raw[sessionIDOffset:], hello.SessionId)

	// 2) Lay out the 16-byte plaintext (version + ts + short_id) in the
	//    first half of the session_id slot. The back half remains zero;
	//    Seal will overwrite it with the 16-byte GCM tag.
	plaintext := buildSessionIDPlaintext(uint32(time.Now().Unix()), opts.ShortID)
	copy(hello.SessionId[:16], plaintext[:])

	// 3) Perform X25519 with the server's static pub and our ephemeral,
	//    then HKDF-SHA256 (salt = Random[:20], info = "REALITY") in place
	//    to produce the 32-byte auth_key.
	ecdhe := uConn.HandshakeState.State13.KeyShareKeys.Ecdhe
	if ecdhe == nil {
		// xray falls back to MlkemEcdhe for post-quantum hybrid key shares.
		// We support the same fallback for compatibility with servers that
		// negotiate X25519MLKEM768 key shares (Chrome≥124 fingerprint).
		ecdhe = uConn.HandshakeState.State13.KeyShareKeys.MlkemEcdhe
	}
	if ecdhe == nil {
		return nil, errors.New("reality: fingerprint produced no TLS 1.3 ECDH key share (need a modern Chrome/Firefox/Safari profile)")
	}

	serverPub, err := ecdh.X25519().NewPublicKey(opts.PublicKey[:])
	if err != nil {
		return nil, fmt.Errorf("reality: parse server public key: %w", err)
	}
	authKey, err := ecdhe.ECDH(serverPub)
	if err != nil {
		return nil, fmt.Errorf("reality: ECDH: %w", err)
	}
	if _, err := hkdf.New(sha256.New, authKey, hello.Random[:20], []byte("REALITY")).Read(authKey); err != nil {
		return nil, fmt.Errorf("reality: HKDF: %w", err)
	}

	// 4) Seal: AAD is hello.Raw with the session_id slot still zeros,
	//    nonce is hello.Random[20:32], output overwrites hello.SessionId.
	sealed, err := sealSessionID(authKey, hello.Random[20:32], plaintext, hello.Raw)
	if err != nil {
		return nil, fmt.Errorf("reality: seal session_id: %w", err)
	}
	copy(hello.SessionId, sealed[:])

	// 5) Write the sealed session_id into the Raw buffer that utls will
	//    send on the wire. Do this AFTER Seal so AAD stays zero-slotted.
	copy(hello.Raw[sessionIDOffset:], hello.SessionId)

	// 6) Drive the TLS handshake. utls will ship hello.Raw verbatim.
	if err := uConn.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("reality: tls handshake: %w", err)
	}

	// 7) Verify the server proved knowledge of auth_key by tagging its
	//    ed25519 leaf cert. Without this, a probe could steer us to the
	//    real decoy site and the inner VLESS layer would fail cryptically.
	state := uConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		_ = uConn.Close()
		return nil, errors.New("reality: server sent no certificate")
	}
	if err := verifyServerCert(authKey, state.PeerCertificates[0]); err != nil {
		_ = uConn.Close()
		return nil, err
	}

	return uConn, nil
}
