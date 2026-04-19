package vmess

import (
	"crypto/hmac"
	"crypto/sha256"
	"hash"
)

// KDF labels used by the VMess AEAD header scheme (V2Fly spec).
//
// Each label identifies a distinct HMAC-SHA256 chain rooted at the shared
// command key. The chain derivation is "use `label` as the HMAC key, with
// SHA-256 as the inner hash; the resulting HMAC becomes the inner hash for
// the next layer".
var (
	kdfLabelAuthIDEncKey     = []byte("AES Auth ID Encryption")
	kdfLabelHeaderPayloadKey = []byte("VMess Header AEAD Key")
	kdfLabelHeaderPayloadIV  = []byte("VMess Header AEAD Nonce")
	kdfLabelHeaderLenKey     = []byte("VMess Header AEAD Key_Length")
	kdfLabelHeaderLenIV      = []byte("VMess Header AEAD Nonce_Length")
)

// kdfSeed is the outermost HMAC key in the V2Fly KDF chain. Not a secret —
// every compliant implementation uses the same 14-byte string — but clients
// and servers must match it bit-for-bit.
var kdfSeed = []byte("VMess AEAD KDF")

// KDF derives 32 bytes from key + any number of path labels using the V2Fly
// chained-HMAC construction:
//
//	factory_0()  = hmac.New(sha256, kdfSeed)
//	factory_i()  = hmac.New(factory_{i-1}, path[i-1])  // i ≥ 1
//	out          = factory_n().Write(key).Sum(nil)
//
// Each HMAC layer uses the PREVIOUS layer *as its underlying hash function*
// — not its digest. Go's `hmac.New` takes a `func() hash.Hash` factory, so
// we pass the previous layer's factory directly; Go will instantiate a
// fresh inner-hash instance each time it needs to reset state.
//
// The first naive implementation (factory := func(){ return h }) panics at
// runtime with "hash generation function does not produce unique values"
// because hmac internally demands that successive calls to the factory
// return independent states. The per-layer closure below preserves that.
func KDF(key []byte, paths ...[]byte) []byte {
	factory := func() hash.Hash { return hmac.New(sha256.New, kdfSeed) }
	for _, p := range paths {
		prev := factory
		label := p
		factory = func() hash.Hash { return hmac.New(prev, label) }
	}
	h := factory()
	h.Write(key)
	return h.Sum(nil)
}
