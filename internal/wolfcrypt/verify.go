package wolfcrypt

import (
	"errors"
	"fmt"

	gowolf "github.com/wolfssl/go-wolfssl"
)

// ed25519PublicKeySize is the wire size of an Ed25519 public key
// (RFC 8032: 32 bytes).
const ed25519PublicKeySize = 32

// Ed25519Verify returns (true, nil) iff signature is a valid
// Ed25519 signature over message under the 32-byte public key.
// A structural problem (wrong key length, init failure) returns
// an error; a well-formed but non-matching signature returns
// (false, nil) so the caller can distinguish "rejected" from
// "could not even try".
func Ed25519Verify(publicKey, message, signature []byte) (bool, error) {
	if len(publicKey) != ed25519PublicKeySize {
		return false, fmt.Errorf("wolfcrypt.Ed25519Verify: public key must be %d bytes (got %d)",
			ed25519PublicKeySize, len(publicKey))
	}

	var key gowolf.Ed25519_key
	if rc := gowolf.Wc_ed25519_init(&key); rc != 0 {
		return false, fmt.Errorf("wolfcrypt.Ed25519Verify: Wc_ed25519_init: %d", rc)
	}
	defer gowolf.Wc_ed25519_free(&key)

	if rc := gowolf.Wc_ed25519_import_public(publicKey, len(publicKey), &key); rc != 0 {
		return false, fmt.Errorf("wolfcrypt.Ed25519Verify: Wc_ed25519_import_public: %d", rc)
	}

	var result int
	if rc := gowolf.Wc_ed25519_verify_msg(signature, len(signature), message, len(message), &result, &key); rc != 0 {
		// Treat any wolfCrypt error as a clean reject (the
		// caller distinguishes "rejected" from "could not try"
		// via the bool, not the int).
		return false, nil
	}
	return result == 1, nil
}

// ECCVerifyP256 returns (true, nil) iff signature is a valid
// ECDSA-P256 signature over hash under publicKey.
//   publicKey: SEC1 uncompressed point (0x04 || X(32) || Y(32))
//   hash:      pre-computed message digest (32 bytes for SHA-256)
//   signature: ASN.1 DER SEQUENCE { INTEGER r, INTEGER s }
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
//   exponent:  big-endian bytes of e
//   message:   the raw message bytes (NOT pre-hashed); wolfCrypt
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

	var key gowolf.RsaKey
	if rc := gowolf.Wc_InitRsaKey(&key, nil); rc != 0 {
		return false, fmt.Errorf("wolfcrypt.RSAVerifyPKCS1v15SHA256: Wc_InitRsaKey: %d", rc)
	}
	defer gowolf.Wc_FreeRsaKey(&key)

	if rc := gowolf.Wc_RsaPublicKeyDecodeRaw(modulus, len(modulus), exponent, len(exponent), &key); rc != 0 {
		return false, fmt.Errorf("wolfcrypt.RSAVerifyPKCS1v15SHA256: Wc_RsaPublicKeyDecodeRaw: %d", rc)
	}

	rc := gowolf.Wc_SignatureVerify(
		gowolf.WC_HASH_TYPE_SHA256,
		gowolf.WC_SIGNATURE_TYPE_RSA_W_ENC,
		message, len(message),
		signature, len(signature),
		&key,
	)
	if rc != 0 {
		return false, nil
	}
	return true, nil
}
