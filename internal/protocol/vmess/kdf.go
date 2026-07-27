package vmess

import (
	"crypto/hmac"
	"crypto/sha256"
	"hash"
)

// VMESS uses a recursive HMAC construction as its key-derivation function.
// KDF(key, path...) nests one HMAC-SHA256 per path element, each keyed by the
// previous layer, seeded from a fixed label ("VMess AEAD KDF"). The outermost
// HMAC is finally fed the input key material. This is the exact construction
// used by v2ray/xray and sing-box, so digests match those implementations.
var kdfSaltConst = []byte("VMess AEAD KDF")

// hmacCreator lets KDF build the nested HMAC tree. Go's crypto/hmac accepts any
// func() hash.Hash, so each layer is itself a valid hash constructor keyed by
// the layer beneath it.
type hmacCreator struct {
	parent *hmacCreator
	value  []byte
}

func (h *hmacCreator) create() hash.Hash {
	if h.parent == nil {
		return hmac.New(sha256.New, h.value)
	}
	return hmac.New(h.parent.create, h.value)
}

// kdf derives a 32-byte key from key and the ordered path labels.
func kdf(key []byte, path ...[]byte) []byte {
	creator := &hmacCreator{value: kdfSaltConst}
	for _, p := range path {
		creator = &hmacCreator{parent: creator, value: p}
	}
	mac := creator.create()
	mac.Write(key)
	return mac.Sum(nil)
}

// kdf16 derives a 16-byte key (AES-128) from key and path labels.
func kdf16(key []byte, path ...[]byte) []byte {
	return kdf(key, path...)[:16]
}

// KDF path labels defined by the VMESS-AEAD spec.
var (
	kdfSaltAuthIDEncryptionKey            = []byte("AES Auth ID Encryption")
	kdfSaltAEADRespHeaderLenKey           = []byte("AEAD Resp Header Len Key")
	kdfSaltAEADRespHeaderLenIV            = []byte("AEAD Resp Header Len IV")
	kdfSaltAEADRespHeaderPayloadKey       = []byte("AEAD Resp Header Key")
	kdfSaltAEADRespHeaderPayloadIV        = []byte("AEAD Resp Header IV")
	kdfSaltVMessHeaderPayloadAEADKey      = []byte("VMess Header AEAD Key")
	kdfSaltVMessHeaderPayloadAEADIV       = []byte("VMess Header AEAD Nonce")
	kdfSaltVMessHeaderPayloadLengthAEADKey = []byte("VMess Header AEAD Key_Length")
	kdfSaltVMessHeaderPayloadLengthAEADIV  = []byte("VMess Header AEAD Nonce_Length")
)
