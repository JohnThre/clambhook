package openvpn

import (
	"crypto/tls"
	"fmt"
)

// Key material slot sizes. OpenVPN 2.6 + `tls-ekm` exports a 256-byte
// block via RFC 5705 TLS-EKM and splits it into four 64-byte sections:
//
//	[0..64)      client cipher key (we only use the first 32 bytes for AES/ChaCha)
//	[64..128)    client HMAC key   (for AEAD: first 8 bytes become implicit IV)
//	[128..192)   server cipher key
//	[192..256)   server HMAC key   (first 8 bytes → server implicit IV)
//
// We don't use the HMAC halves for MAC (AEAD's tag replaces the HMAC
// role), but the first 8 bytes of each HMAC slot are read as the
// "implicit IV" that forms the high 8 bytes of every AEAD nonce. This
// scheme is identical to what OpenVPN's reference C client does with
// `key_method=2` and TLS-EKM.
const (
	keyBlockSize    = 256
	slotSize        = 64
	cipherKeySize   = 32 // both AES-256-GCM and ChaCha20-Poly1305 use 32-byte keys
	implicitIVSize  = 8  // first 8 bytes of each HMAC-slot
)

// tlsEKMLabel is the RFC 5705 exporter label OpenVPN 2.6 standardised
// for data-channel key derivation. Servers configured with `tls-ekm`
// (default in 2.6+) export with this exact label.
const tlsEKMLabel = "EXPORTER-OpenVPN-datakeys"

// keyMaterial holds the per-direction symmetric state for one data-channel
// generation. AEAD ciphers take the cipher key directly; the implicit IV
// is XORed (conceptually — actually concatenated) with the packet ID to
// form the 12-byte nonce on every packet.
type keyMaterial struct {
	clientCipherKey []byte
	clientImplicitIV []byte // 8 bytes
	serverCipherKey []byte
	serverImplicitIV []byte // 8 bytes
}

// deriveKeys exports 256 bytes of key material from a post-handshake
// tls.ConnectionState and slices it into per-direction cipher keys and
// implicit IVs. Uses RFC 5705 TLS-EKM via (*tls.Conn).ExportKeyingMaterial.
//
// cipher names the negotiated AEAD (after NCP). It determines how many
// bytes of each cipher slot we actually use — always 32 for the two we
// support, but defined explicitly so the wire format stays
// self-documenting.
func deriveKeys(state *tls.ConnectionState, cipher string) (*keyMaterial, error) {
	// ExportKeyingMaterial wants label, context, and length. OpenVPN uses
	// no context (nil), which is the common "just derive from PRF" case.
	block, err := state.ExportKeyingMaterial(tlsEKMLabel, nil, keyBlockSize)
	if err != nil {
		return nil, fmt.Errorf("openvpn: TLS EKM export: %w (server likely missing tls-ekm)", err)
	}
	return splitKeyBlock(block, cipher)
}

// splitKeyBlock is the pure slicing logic, factored out so tests can
// drive it with synthetic inputs.
func splitKeyBlock(block []byte, cipher string) (*keyMaterial, error) {
	if len(block) != keyBlockSize {
		return nil, fmt.Errorf("openvpn: key block wrong size: %d (want %d)", len(block), keyBlockSize)
	}
	ks := cipherKeySize
	switch cipher {
	case "AES-256-GCM", "CHACHA20-POLY1305":
		// both use 32-byte keys
	default:
		return nil, fmt.Errorf("openvpn: unsupported cipher for key split: %q", cipher)
	}
	return &keyMaterial{
		clientCipherKey:  append([]byte(nil), block[0:ks]...),
		clientImplicitIV: append([]byte(nil), block[slotSize:slotSize+implicitIVSize]...),
		serverCipherKey:  append([]byte(nil), block[2*slotSize:2*slotSize+ks]...),
		serverImplicitIV: append([]byte(nil), block[3*slotSize:3*slotSize+implicitIVSize]...),
	}, nil
}
