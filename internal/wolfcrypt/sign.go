package wolfcrypt

/*
#cgo CFLAGS: -I${SRCDIR}/../../build/wolfssl-install/include
#cgo LDFLAGS: ${SRCDIR}/../../build/wolfssl-install/lib/libwolfssl.a
#cgo darwin LDFLAGS: -framework Security -framework CoreFoundation

#include <wolfssl/options.h>
#include <wolfssl/wolfcrypt/ed25519.h>
#include <wolfssl/wolfcrypt/random.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// Ed25519GenKey generates a fresh Ed25519 keypair. Returns the
// 32-byte public key and a 64-byte private key in the convention
// the standard library uses: seed (32) || public (32). Ed25519Sign
// accepts the 64-byte form directly.
func Ed25519GenKey() (publicKey, privateKey []byte, err error) {
	var rng C.WC_RNG
	if rc := C.wc_InitRng(&rng); rc != 0 {
		return nil, nil, fmt.Errorf("wolfcrypt.Ed25519GenKey: wc_InitRng: %d", int(rc))
	}
	defer C.wc_FreeRng(&rng)

	var key C.ed25519_key
	if rc := C.wc_ed25519_init(&key); rc != 0 {
		return nil, nil, fmt.Errorf("wolfcrypt.Ed25519GenKey: wc_ed25519_init: %d", int(rc))
	}
	defer C.wc_ed25519_free(&key)

	if rc := C.wc_ed25519_make_key(&rng, 32, &key); rc != 0 {
		return nil, nil, fmt.Errorf("wolfcrypt.Ed25519GenKey: wc_ed25519_make_key: %d", int(rc))
	}

	pub := make([]byte, ed25519PublicKeySize)
	pubLen := C.word32(len(pub))
	rc := C.wc_ed25519_export_public(
		&key,
		(*C.byte)(unsafe.Pointer(&pub[0])),
		&pubLen,
	)
	if rc != 0 {
		return nil, nil, fmt.Errorf("wolfcrypt.Ed25519GenKey: wc_ed25519_export_public: %d", int(rc))
	}

	priv := make([]byte, 64)
	privSeedLen := C.word32(32)
	rc = C.wc_ed25519_export_private_only(
		&key,
		(*C.byte)(unsafe.Pointer(&priv[0])),
		&privSeedLen,
	)
	if rc != 0 {
		return nil, nil, fmt.Errorf("wolfcrypt.Ed25519GenKey: wc_ed25519_export_private_only: %d", int(rc))
	}
	copy(priv[32:], pub)
	return pub, priv, nil
}

// Ed25519Sign signs message with the 64-byte private key returned
// by Ed25519GenKey (seed || public). Output is the 64-byte
// Ed25519 signature.
func Ed25519Sign(privateKey, message []byte) ([]byte, error) {
	if len(privateKey) != 64 {
		return nil, errors.New("wolfcrypt.Ed25519Sign: private key must be 64 bytes (seed || pub)")
	}
	var key C.ed25519_key
	if rc := C.wc_ed25519_init(&key); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.Ed25519Sign: wc_ed25519_init: %d", int(rc))
	}
	defer C.wc_ed25519_free(&key)

	seed := privateKey[:32]
	pub := privateKey[32:]
	rc := C.wc_ed25519_import_private_key(
		(*C.byte)(unsafe.Pointer(&seed[0])), 32,
		(*C.byte)(unsafe.Pointer(&pub[0])), 32,
		&key,
	)
	if rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.Ed25519Sign: wc_ed25519_import_private_key: %d", int(rc))
	}

	sig := make([]byte, 64)
	sigLen := C.word32(len(sig))
	var msgPtr *C.byte
	if len(message) > 0 {
		msgPtr = (*C.byte)(unsafe.Pointer(&message[0]))
	}
	rc = C.wc_ed25519_sign_msg(
		msgPtr, C.word32(len(message)),
		(*C.byte)(unsafe.Pointer(&sig[0])), &sigLen,
		&key,
	)
	if rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.Ed25519Sign: wc_ed25519_sign_msg: %d", int(rc))
	}
	return sig[:sigLen], nil
}
