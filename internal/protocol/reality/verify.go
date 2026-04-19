package reality

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha512"
	"crypto/x509"
	"errors"
	"fmt"
)

// verifyServerCert confirms the server we completed a TLS handshake with
// is a genuine Reality server and not the decoy site it fronts.
//
// Reality servers synthesize a self-signed ed25519 leaf cert whose
// Signature field is not a real signature — it's HMAC-SHA512(authKey,
// ed25519_pub_bytes). Only a party who derived the same authKey (i.e.
// shared an X25519 ECDH with our ephemeral) can compute this tag, so a
// match proves we hit the Reality server and its auth_key matched ours.
// A mismatch means we were silently proxied to the decoy — in that case
// the inner VLESS layer would fail with an opaque error, so we bail
// loudly here instead.
//
// This differs from xray's behavior, which on mismatch falls into a
// SpiderX decoy-fetch loop to blend in further. Tearing down immediately
// is a v1 limitation — see the package README.
func verifyServerCert(authKey []byte, peer *x509.Certificate) error {
	if peer == nil {
		return errors.New("reality: no peer certificate")
	}
	pub, ok := peer.PublicKey.(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("reality: peer cert has non-ed25519 key %T (server likely not Reality-capable)", peer.PublicKey)
	}
	mac := hmac.New(sha512.New, authKey)
	mac.Write(pub)
	expect := mac.Sum(nil)
	if !hmac.Equal(expect, peer.Signature) {
		return errors.New("reality: server auth failed (hit decoy or wrong public_key)")
	}
	return nil
}
