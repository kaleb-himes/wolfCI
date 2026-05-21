// Package wolfcrypt is the bridge to wolfSSL's wolfCrypt primitives.
// Every cryptographic operation in wolfCI MUST come from this
// package; the rest of the tree is forbidden from importing
// crypto/rand, crypto/ecdsa, crypto/x509, golang.org/x/crypto/*, or
// any other library that performs cryptography.
//
// Phase 10.6b moved the primitives below from hand-rolled CGO
// (Phase 10.1a) to thin wrappers over github.com/wolfssl/go-wolfssl
// (the vendored copy under third_party/go-wolfssl). Phase 10.6c
// adds the wrappers go-wolfssl is missing today (Ed25519, RSA
// verify, cert minting) as patches under
// third_party/go-wolfssl-patches/; 10.6d removes the remaining
// hand-rolled CGO in this package.
//
// Current state (post-10.6b):
//
//   RandBytes        -> go-wolfssl Wc_RNG_*
//   HMACSHA256       -> go-wolfssl Wc_Hmac*  with WC_SHA256
//   PBKDF2HMACSHA256 -> go-wolfssl Wc_PBKDF2 with WC_SHA256
//   SHA256           -> go-wolfssl Wc_Sha256Hash (see cert.go)
//   ECCVerifyP256    -> go-wolfssl Wc_ecc_* (see verify.go)
//   Ed25519*         -> hand-rolled CGO (sign.go, verify.go)
//   RSAVerify*       -> hand-rolled CGO (verify.go)
//   MintCert         -> hand-rolled CGO (cert.go)
package wolfcrypt

import (
	"errors"
	"fmt"

	gowolf "github.com/wolfssl/go-wolfssl"
)

// sha256DigestSize matches WC_SHA256_DIGEST_SIZE from go-wolfssl.
// Hard-coded so callers do not pay an extra package crossing on
// every HMAC call.
const sha256DigestSize = 32

// RandBytes returns n bytes drawn from wolfCrypt's CSPRNG via
// go-wolfssl.
func RandBytes(n int) ([]byte, error) {
	if n < 0 {
		return nil, errors.New("wolfcrypt.RandBytes: negative size")
	}
	if n == 0 {
		return []byte{}, nil
	}
	var rng gowolf.WC_RNG
	if rc := gowolf.Wc_InitRng(&rng); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.RandBytes: Wc_InitRng: %d", rc)
	}
	defer gowolf.Wc_FreeRng(&rng)

	out := make([]byte, n)
	if rc := gowolf.Wc_RNG_GenerateBlock(&rng, out, n); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.RandBytes: Wc_RNG_GenerateBlock: %d", rc)
	}
	return out, nil
}

// HMACSHA256 returns HMAC-SHA-256(key, data). The output is always
// 32 bytes. Implemented via go-wolfssl's Wc_Hmac* API.
func HMACSHA256(key, data []byte) ([]byte, error) {
	var h gowolf.Hmac
	if rc := gowolf.Wc_HmacInit(&h, nil, gowolf.INVALID_DEVID); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.HMACSHA256: Wc_HmacInit: %d", rc)
	}
	defer gowolf.Wc_HmacFree(&h)

	if rc := gowolf.Wc_HmacSetKey(&h, gowolf.WC_SHA256, key, len(key)); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.HMACSHA256: Wc_HmacSetKey: %d", rc)
	}
	if len(data) > 0 {
		if rc := gowolf.Wc_HmacUpdate(&h, data, len(data)); rc != 0 {
			return nil, fmt.Errorf("wolfcrypt.HMACSHA256: Wc_HmacUpdate: %d", rc)
		}
	}
	out := make([]byte, sha256DigestSize)
	if rc := gowolf.Wc_HmacFinal(&h, out); rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.HMACSHA256: Wc_HmacFinal: %d", rc)
	}
	return out, nil
}

// PBKDF2HMACSHA256 derives a keyLen-byte key from password and salt
// using PBKDF2-HMAC-SHA-256 via go-wolfssl's Wc_PBKDF2.
func PBKDF2HMACSHA256(password, salt []byte, iterations, keyLen int) ([]byte, error) {
	if keyLen <= 0 {
		return nil, errors.New("wolfcrypt.PBKDF2HMACSHA256: keyLen must be positive")
	}
	if iterations <= 0 {
		return nil, errors.New("wolfcrypt.PBKDF2HMACSHA256: iterations must be positive")
	}
	out := make([]byte, keyLen)
	rc := gowolf.Wc_PBKDF2(out, password, len(password), salt, len(salt), iterations, keyLen, gowolf.WC_SHA256)
	if rc != 0 {
		return nil, fmt.Errorf("wolfcrypt.PBKDF2HMACSHA256: Wc_PBKDF2: %d", rc)
	}
	return out, nil
}
