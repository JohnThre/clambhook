//go:build !ios

package cnet

/*
#cgo CFLAGS: -I${SRCDIR}/../../clib/include
#cgo LDFLAGS: -L${SRCDIR}/../../clib -lcnet
#cgo pkg-config: libsodium
#include "cnet.h"
#include <stdlib.h>
*/
import "C"
import (
	"fmt"
	"unsafe"
)

// SHA224 computes the SHA-224 hash of data.
func SHA224(data []byte) []byte {
	hash := make([]byte, 28)
	if len(data) == 0 {
		C.cnet_sha224(nil, 0, (*C.uint8_t)(unsafe.Pointer(&hash[0])))
		return hash
	}
	C.cnet_sha224(
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
		(*C.uint8_t)(unsafe.Pointer(&hash[0])),
	)
	return hash
}

// AES256GCMEncrypt encrypts plaintext using AES-256-GCM.
func AES256GCMEncrypt(key, nonce, plaintext, aad []byte) (ciphertext, tag []byte, err error) {
	ct := make([]byte, len(plaintext))
	t := make([]byte, 16)

	var ptPtr, aadPtr *C.uint8_t
	if len(plaintext) > 0 {
		ptPtr = (*C.uint8_t)(unsafe.Pointer(&plaintext[0]))
	}
	if len(aad) > 0 {
		aadPtr = (*C.uint8_t)(unsafe.Pointer(&aad[0]))
	}

	rc := C.cnet_aes256gcm_encrypt(
		(*C.uint8_t)(unsafe.Pointer(&key[0])),
		(*C.uint8_t)(unsafe.Pointer(&nonce[0])),
		ptPtr,
		C.size_t(len(plaintext)),
		aadPtr,
		C.size_t(len(aad)),
		(*C.uint8_t)(unsafe.Pointer(&ct[0])),
		(*C.uint8_t)(unsafe.Pointer(&t[0])),
	)
	if rc != 0 {
		return nil, nil, fmt.Errorf("aes256gcm encrypt failed: %d", rc)
	}
	return ct, t, nil
}

// AES256GCMDecrypt decrypts ciphertext using AES-256-GCM.
func AES256GCMDecrypt(key, nonce, ciphertext, aad, tag []byte) (plaintext []byte, err error) {
	pt := make([]byte, len(ciphertext))

	var ctPtr, aadPtr *C.uint8_t
	if len(ciphertext) > 0 {
		ctPtr = (*C.uint8_t)(unsafe.Pointer(&ciphertext[0]))
	}
	if len(aad) > 0 {
		aadPtr = (*C.uint8_t)(unsafe.Pointer(&aad[0]))
	}

	rc := C.cnet_aes256gcm_decrypt(
		(*C.uint8_t)(unsafe.Pointer(&key[0])),
		(*C.uint8_t)(unsafe.Pointer(&nonce[0])),
		ctPtr,
		C.size_t(len(ciphertext)),
		aadPtr,
		C.size_t(len(aad)),
		(*C.uint8_t)(unsafe.Pointer(&tag[0])),
		(*C.uint8_t)(unsafe.Pointer(&pt[0])),
	)
	if rc != 0 {
		return nil, fmt.Errorf("aes256gcm decrypt failed: %d", rc)
	}
	return pt, nil
}

// AES256GCMAvailable reports whether AES-256-GCM can run on this host.
// libsodium's AES-GCM requires hardware AES support (AES-NI on x86_64
// or ARM Crypto Extensions); on hosts without it, encrypt/decrypt will
// return an error. Callers can use this to pick ChaCha20-Poly1305 as a
// fallback cipher.
func AES256GCMAvailable() bool {
	return C.cnet_aes256gcm_available() == 1
}

// ChaCha20Poly1305Encrypt encrypts plaintext using ChaCha20-Poly1305-IETF.
func ChaCha20Poly1305Encrypt(key, nonce, plaintext, aad []byte) (ciphertext, tag []byte, err error) {
	ct := make([]byte, len(plaintext))
	t := make([]byte, 16)

	var ptPtr, aadPtr *C.uint8_t
	if len(plaintext) > 0 {
		ptPtr = (*C.uint8_t)(unsafe.Pointer(&plaintext[0]))
	}
	if len(aad) > 0 {
		aadPtr = (*C.uint8_t)(unsafe.Pointer(&aad[0]))
	}

	rc := C.cnet_chacha20poly1305_encrypt(
		(*C.uint8_t)(unsafe.Pointer(&key[0])),
		(*C.uint8_t)(unsafe.Pointer(&nonce[0])),
		ptPtr,
		C.size_t(len(plaintext)),
		aadPtr,
		C.size_t(len(aad)),
		(*C.uint8_t)(unsafe.Pointer(&ct[0])),
		(*C.uint8_t)(unsafe.Pointer(&t[0])),
	)
	if rc != 0 {
		return nil, nil, fmt.Errorf("chacha20poly1305 encrypt failed: %d", rc)
	}
	return ct, t, nil
}

// ChaCha20Poly1305Decrypt decrypts ciphertext using ChaCha20-Poly1305-IETF.
func ChaCha20Poly1305Decrypt(key, nonce, ciphertext, aad, tag []byte) (plaintext []byte, err error) {
	pt := make([]byte, len(ciphertext))

	var ctPtr, aadPtr *C.uint8_t
	if len(ciphertext) > 0 {
		ctPtr = (*C.uint8_t)(unsafe.Pointer(&ciphertext[0]))
	}
	if len(aad) > 0 {
		aadPtr = (*C.uint8_t)(unsafe.Pointer(&aad[0]))
	}

	rc := C.cnet_chacha20poly1305_decrypt(
		(*C.uint8_t)(unsafe.Pointer(&key[0])),
		(*C.uint8_t)(unsafe.Pointer(&nonce[0])),
		ctPtr,
		C.size_t(len(ciphertext)),
		aadPtr,
		C.size_t(len(aad)),
		(*C.uint8_t)(unsafe.Pointer(&tag[0])),
		(*C.uint8_t)(unsafe.Pointer(&pt[0])),
	)
	if rc != 0 {
		return nil, fmt.Errorf("chacha20poly1305 decrypt failed: %d", rc)
	}
	return pt, nil
}

// ProcessPacket processes a network packet through the C layer.
func ProcessPacket(in []byte) ([]byte, error) {
	out := make([]byte, len(in)*2)
	var outLen C.size_t

	var inPtr *C.uint8_t
	if len(in) > 0 {
		inPtr = (*C.uint8_t)(unsafe.Pointer(&in[0]))
	}

	rc := C.cnet_process_packet(
		inPtr,
		C.size_t(len(in)),
		(*C.uint8_t)(unsafe.Pointer(&out[0])),
		C.size_t(len(out)),
		&outLen,
	)
	if rc != 0 {
		return nil, fmt.Errorf("process packet failed: %d", rc)
	}
	return out[:outLen], nil
}
