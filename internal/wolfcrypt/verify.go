package wolfcrypt

/*
#cgo CFLAGS: -I${SRCDIR}/../../build/wolfssl-install/include
#cgo LDFLAGS: ${SRCDIR}/../../build/wolfssl-install/lib/libwolfssl.a
#cgo darwin LDFLAGS: -framework Security -framework CoreFoundation

#include <wolfssl/options.h>
#include <wolfssl/wolfcrypt/ed25519.h>
#include <wolfssl/wolfcrypt/ecc.h>
#include <wolfssl/wolfcrypt/rsa.h>
#include <wolfssl/wolfcrypt/signature.h>
#include <wolfssl/wolfcrypt/hash.h>
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"fmt"

	gowolf "github.com/wolfssl/go-wolfssl"
	"unsafe"
)

// ed25519PublicKeySize is the wire size of an Ed25519 public key
// (RFC 8032: 32 bytes).
const ed25519PublicKeySize = 32

// Ed25519Verify returns (true, nil) iff signature is a valid
// Ed25519 signature over message under the 32-byte public key.
// A structural problem (wrong key length, init failure) returns an
// error; a well-formed but non-matching signature returns
// (false, nil) so the caller can distinguish "rejected" from
// "could not even try".
func Ed25519Verify(publicKey, message, signature []byte) (bool, error) {
	if len(publicKey) != ed25519PublicKeySize {
		return false, fmt.Errorf("wolfcrypt.Ed25519Verify: public key must be %d bytes (got %d)",
			ed25519PublicKeySize, len(publicKey))
	}

	var key C.ed25519_key
	if rc := C.wc_ed25519_init(&key); rc != 0 {
		return false, fmt.Errorf("wolfcrypt.Ed25519Verify: wc_ed25519_init: %d", int(rc))
	}
	defer C.wc_ed25519_free(&key)

	rc := C.wc_ed25519_import_public(
		(*C.byte)(unsafe.Pointer(&publicKey[0])),
		C.word32(len(publicKey)),
		&key,
	)
	if rc != 0 {
		return false, fmt.Errorf("wolfcrypt.Ed25519Verify: wc_ed25519_import_public: %d", int(rc))
	}

	var msgPtr, sigPtr *C.byte
	if len(message) > 0 {
		msgPtr = (*C.byte)(unsafe.Pointer(&message[0]))
	}
	if len(signature) > 0 {
		sigPtr = (*C.byte)(unsafe.Pointer(&signature[0]))
	}

	var result C.int
	rc = C.wc_ed25519_verify_msg(
		sigPtr, C.word32(len(signature)),
		msgPtr, C.word32(len(message)),
		&result, &key,
	)
	if rc != 0 {
		// wolfCrypt returns SIG_VERIFY_E (or similar) on a clean
		// reject; treat any non-zero rc as "rejected, no panic".
		return false, nil
	}
	return result == 1, nil
}

// ECCVerifyP256 returns (true, nil) iff signature is a valid
// ECDSA-P256 signature over hash under publicKey.
//   publicKey: SEC1 uncompressed point (0x04 || X(32) || Y(32))
//   hash:      pre-computed message digest (32 bytes for SHA-256)
//   signature: ASN.1 DER SEQUENCE { INTEGER r, INTEGER s }
//
// Unlike Ed25519, the caller hashes the message; this matches the
// SSH ecdsa-sha2-nistp256 protocol where the signature covers a
// SHA-256 of a constructed signed-data blob.
func ECCVerifyP256(publicKey, hash, signature []byte) (bool, error) {
	if len(publicKey) == 0 || publicKey[0] != 0x04 {
		return false, errors.New("wolfcrypt.ECCVerifyP256: publicKey must be SEC1 uncompressed (0x04 prefix)")
	}

	var key gowolf.Ecc_key
	if rc := gowolf.Wc_ecc_init(&key); rc != 0 {
		return false, fmt.Errorf("wolfcrypt.ECCVerifyP256: Wc_ecc_init: %d", rc)
	}
	defer gowolf.Wc_ecc_free(&key)

	if rc := gowolf.Wc_ecc_import_x963_ex(publicKey, len(publicKey), &key, gowolf.ECC_SECP256R1); rc != 0 {
		return false, fmt.Errorf("wolfcrypt.ECCVerifyP256: Wc_ecc_import_x963_ex: %d", rc)
	}

	var result int
	if rc := gowolf.Wc_ecc_verify_hash(signature, len(signature), hash, len(hash), &result, &key); rc != 0 {
		return false, nil
	}
	return result == 1, nil
}

// RSAVerifyPKCS1v15SHA256 returns (true, nil) iff signature is a
// valid RSA PKCS#1 v1.5 signature with the DigestInfo prefix over
// SHA-256(message), under the RSA public key (modulus, exponent).
//   modulus:   big-endian bytes of N, leading zeros stripped is fine
//   exponent:  big-endian bytes of e (typically 0x01 0x00 0x01 for 65537)
//   message:   the raw message bytes (NOT pre-hashed); this routine
//              hashes internally via wc_SignatureVerify
//   signature: the raw signature blob (k bytes where k = ceil(bitlen(N) / 8))
func RSAVerifyPKCS1v15SHA256(modulus, exponent, message, signature []byte) (bool, error) {
	if len(modulus) == 0 {
		return false, errors.New("wolfcrypt.RSAVerifyPKCS1v15SHA256: empty modulus")
	}
	if len(exponent) == 0 {
		return false, errors.New("wolfcrypt.RSAVerifyPKCS1v15SHA256: empty exponent")
	}
	if len(signature) == 0 {
		return false, errors.New("wolfcrypt.RSAVerifyPKCS1v15SHA256: empty signature")
	}

	var key C.RsaKey
	if rc := C.wc_InitRsaKey(&key, nil); rc != 0 {
		return false, fmt.Errorf("wolfcrypt.RSAVerifyPKCS1v15SHA256: wc_InitRsaKey: %d", int(rc))
	}
	defer C.wc_FreeRsaKey(&key)

	rc := C.wc_RsaPublicKeyDecodeRaw(
		(*C.byte)(unsafe.Pointer(&modulus[0])),
		C.word32(len(modulus)),
		(*C.byte)(unsafe.Pointer(&exponent[0])),
		C.word32(len(exponent)),
		&key,
	)
	if rc != 0 {
		return false, fmt.Errorf("wolfcrypt.RSAVerifyPKCS1v15SHA256: wc_RsaPublicKeyDecodeRaw: %d", int(rc))
	}

	var msgPtr *C.byte
	if len(message) > 0 {
		msgPtr = (*C.byte)(unsafe.Pointer(&message[0]))
	}

	rc = C.wc_SignatureVerify(
		C.WC_HASH_TYPE_SHA256,
		C.WC_SIGNATURE_TYPE_RSA_W_ENC,
		msgPtr, C.word32(len(message)),
		(*C.byte)(unsafe.Pointer(&signature[0])), C.word32(len(signature)),
		unsafe.Pointer(&key), C.word32(unsafe.Sizeof(key)),
	)
	if rc != 0 {
		return false, nil
	}
	return true, nil
}
