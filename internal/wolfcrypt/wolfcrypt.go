// Package wolfcrypt is the bridge to wolfSSL's wolfCrypt primitives.
// Every cryptographic operation in wolfCI MUST come from this
// package; the rest of the tree is forbidden from importing
// crypto/rand, crypto/ecdsa, crypto/x509, golang.org/x/crypto/*, or
// any other library that performs cryptography.
//
// Phase 10.1a covers:
//
//	RandBytes        wc_RNG_GenerateBlock
//	HMACSHA256       wc_Hmac*  with WC_SHA256
//	PBKDF2HMACSHA256 wc_PBKDF2 with WC_SHA256
//
// Later sub-checkpoints add signature verification (10.1b) and
// X.509 cert minting (10.1c).
package wolfcrypt

/*
#cgo CFLAGS: -I${SRCDIR}/../../build/wolfssl-install/include
#cgo LDFLAGS: ${SRCDIR}/../../build/wolfssl-install/lib/libwolfssl.a
#cgo darwin LDFLAGS: -framework Security -framework CoreFoundation

#include <wolfssl/options.h>
#include <wolfssl/wolfcrypt/random.h>
#include <wolfssl/wolfcrypt/hmac.h>
#include <wolfssl/wolfcrypt/pwdbased.h>
#include <wolfssl/wolfcrypt/types.h>
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// sha256DigestSize matches WC_SHA256_DIGEST_SIZE from
// wolfssl/wolfcrypt/sha256.h. Hard-coded to avoid an extra CGO
// indirection on every HMAC call.
const sha256DigestSize = 32

// RandBytes returns n bytes drawn from wolfCrypt's CSPRNG.
func RandBytes(n int) ([]byte, error) {
	if n < 0 {
		return nil, errors.New("wolfcrypt.RandBytes: negative size")
	}
	if n == 0 {
		return []byte{}, nil
	}
	var rng C.WC_RNG
	if rc := C.wc_InitRng(&rng); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.RandBytes: wc_InitRng: %d", int(rc))
	}
	defer C.wc_FreeRng(&rng)

	out := make([]byte, n)
	rc := C.wc_RNG_GenerateBlock(
		&rng,
		(*C.byte)(unsafe.Pointer(&out[0])),
		C.word32(n),
	)
	if rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.RandBytes: wc_RNG_GenerateBlock: %d", int(rc))
	}
	return out, nil
}

// HMACSHA256 returns HMAC-SHA-256(key, data). The output is always
// 32 bytes.
func HMACSHA256(key, data []byte) ([]byte, error) {
	var h C.Hmac
	if rc := C.wc_HmacInit(&h, nil, C.INVALID_DEVID); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.HMACSHA256: wc_HmacInit: %d", int(rc))
	}
	defer C.wc_HmacFree(&h)

	var keyPtr *C.byte
	if len(key) > 0 {
		keyPtr = (*C.byte)(unsafe.Pointer(&key[0]))
	}
	if rc := C.wc_HmacSetKey(&h, C.int(C.WC_SHA256), keyPtr, C.word32(len(key))); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.HMACSHA256: wc_HmacSetKey: %d", int(rc))
	}

	if len(data) > 0 {
		rc := C.wc_HmacUpdate(
			&h,
			(*C.byte)(unsafe.Pointer(&data[0])),
			C.word32(len(data)),
		)
		if rc != 0 {
			return nil, fmt.Errorf("wolfcrypt.HMACSHA256: wc_HmacUpdate: %d", int(rc))
		}
	}

	out := make([]byte, sha256DigestSize)
	rc := C.wc_HmacFinal(&h, (*C.byte)(unsafe.Pointer(&out[0])))
	if rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.HMACSHA256: wc_HmacFinal: %d", int(rc))
	}
	return out, nil
}

// PBKDF2HMACSHA256 derives a keyLen-byte key from password and salt
// using PBKDF2-HMAC-SHA-256 with the given iteration count.
func PBKDF2HMACSHA256(password, salt []byte, iterations, keyLen int) ([]byte, error) {
	if keyLen <= 0 {
		return nil, errors.New("wolfcrypt.PBKDF2HMACSHA256: keyLen must be positive")
	}
	if iterations <= 0 {
		return nil, errors.New("wolfcrypt.PBKDF2HMACSHA256: iterations must be positive")
	}

	out := make([]byte, keyLen)
	var pwdPtr, saltPtr *C.byte
	if len(password) > 0 {
		pwdPtr = (*C.byte)(unsafe.Pointer(&password[0]))
	}
	if len(salt) > 0 {
		saltPtr = (*C.byte)(unsafe.Pointer(&salt[0]))
	}
	rc := C.wc_PBKDF2(
		(*C.byte)(unsafe.Pointer(&out[0])),
		pwdPtr, C.int(len(password)),
		saltPtr, C.int(len(salt)),
		C.int(iterations),
		C.int(keyLen),
		C.int(C.WC_SHA256),
	)
	if rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.PBKDF2HMACSHA256: wc_PBKDF2: %d", int(rc))
	}
	return out, nil
}
