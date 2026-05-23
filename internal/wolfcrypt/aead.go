package wolfcrypt

/* internal/wolfcrypt/aead.go - thin wolfCI bridge to go-wolfssl's
 * AES-GCM and HKDF primitives.
 *
 * Per the package header, every cryptographic operation in wolfCI
 * goes through internal/wolfcrypt. The Phase 18 credential store
 * needs three building blocks layered together:
 *
 *   - HKDFSHA256: per-credential key derivation from a server-
 *     wide master secret. Info = credential id; salt is left
 *     empty (the master secret is already pseudorandom).
 *   - AESGCMSeal / AESGCMOpen: AEAD over the JSON-encoded
 *     credential record. The tag is appended to the ciphertext
 *     in the standard "ciphertext || tag" layout the rest of
 *     wolfCI uses.
 *
 * All three are 1:1 wrappers over the go-wolfssl exports already
 * carried in third_party/go-wolfssl. No new patches required.
 */

import (
    "errors"
    "fmt"

    gowolf "github.com/wolfssl/go-wolfssl"
)

/* AESGCMTagSize is the AES-GCM authentication tag length in bytes.
 * Matches go-wolfssl's AES_BLOCK_SIZE (16) which the Appended-Tag
 * helpers also use.
 */
const AESGCMTagSize = 16

/* AESGCMSeal encrypts plaintext under key with the given 12-byte
 * nonce, authenticating any additional data in aad, and returns
 * ciphertext || tag. key must be 16, 24, or 32 bytes (AES-128,
 * AES-192, AES-256). nonce must be 12 bytes; aad may be nil.
 *
 * The output is exactly len(plaintext) + AESGCMTagSize bytes.
 */
func AESGCMSeal(key, nonce, plaintext, aad []byte) ([]byte, error) {
    if l := len(key); l != 16 && l != 24 && l != 32 {
        return nil, fmt.Errorf(
            "wolfcrypt.AESGCMSeal: key length %d not in {16,24,32}",
            l)
    }
    if len(nonce) != 12 {
        return nil, fmt.Errorf(
            "wolfcrypt.AESGCMSeal: nonce length %d, want 12",
            len(nonce))
    }
    var aes gowolf.Aes
    if rc := gowolf.Wc_AesInit(&aes, nil,
        gowolf.INVALID_DEVID); rc != 0 {
        return nil, fmt.Errorf(
            "wolfcrypt.AESGCMSeal: Wc_AesInit: %d", rc)
    }
    defer gowolf.Wc_AesFree(&aes)

    if rc := gowolf.Wc_AesGcmSetKey(&aes, key,
        len(key)); rc != 0 {
        return nil, fmt.Errorf(
            "wolfcrypt.AESGCMSeal: Wc_AesGcmSetKey: %d", rc)
    }

    /* The Appended-Tag helper sizes its own output when we pass
     * a too-small buffer (or nil); pass nil and accept the
     * allocation. zero-length plaintext is legal for AES-GCM
     * (MAC over AAD with no plaintext) and the helper handles
     * it via a nil C pointer downstream.
     */
    sealed, rc := gowolf.Wc_AesGcm_Appended_Tag_Encrypt(
        &aes, nil, plaintext, nonce, aad)
    if rc != 0 {
        return nil, fmt.Errorf(
            "wolfcrypt.AESGCMSeal: "+
                "Wc_AesGcm_Appended_Tag_Encrypt: %d", rc)
    }
    return sealed, nil
}

/* AESGCMOpen reverses AESGCMSeal. ciphertextWithTag is the
 * concatenation of ciphertext and the 16-byte tag. Returns the
 * plaintext on success or a non-nil error if the tag rejects the
 * input (tampering, wrong key, wrong nonce, wrong aad).
 */
func AESGCMOpen(key, nonce, ciphertextWithTag,
    aad []byte) ([]byte, error) {

    if l := len(key); l != 16 && l != 24 && l != 32 {
        return nil, fmt.Errorf(
            "wolfcrypt.AESGCMOpen: key length %d not in {16,24,32}",
            l)
    }
    if len(nonce) != 12 {
        return nil, fmt.Errorf(
            "wolfcrypt.AESGCMOpen: nonce length %d, want 12",
            len(nonce))
    }
    if len(ciphertextWithTag) < AESGCMTagSize {
        return nil, errors.New(
            "wolfcrypt.AESGCMOpen: ciphertext shorter than tag")
    }

    var aes gowolf.Aes
    if rc := gowolf.Wc_AesInit(&aes, nil,
        gowolf.INVALID_DEVID); rc != 0 {
        return nil, fmt.Errorf(
            "wolfcrypt.AESGCMOpen: Wc_AesInit: %d", rc)
    }
    defer gowolf.Wc_AesFree(&aes)

    if rc := gowolf.Wc_AesGcmSetKey(&aes, key,
        len(key)); rc != 0 {
        return nil, fmt.Errorf(
            "wolfcrypt.AESGCMOpen: Wc_AesGcmSetKey: %d", rc)
    }

    plainLen := len(ciphertextWithTag) - AESGCMTagSize
    out := make([]byte, plainLen)
    if rc := gowolf.Wc_AesGcm_Appended_Tag_Decrypt(
        &aes, out, ciphertextWithTag, nonce,
        aad); rc != 0 {
        return nil, fmt.Errorf(
            "wolfcrypt.AESGCMOpen: "+
                "Wc_AesGcm_Appended_Tag_Decrypt: %d", rc)
    }
    return out, nil
}

/* HKDFSHA256 derives length bytes from ikm with HKDF-SHA-256
 * (RFC 5869), using the given salt and info parameters. Pass
 * nil/empty for salt when ikm is already pseudorandom (the
 * common case in wolfCI: master secrets are sourced from
 * RandBytes).
 */
func HKDFSHA256(ikm, salt, info []byte,
    length int) ([]byte, error) {

    if length <= 0 {
        return nil, errors.New(
            "wolfcrypt.HKDFSHA256: length must be positive")
    }
    out := make([]byte, length)
    rc := gowolf.Wc_HKDF(gowolf.WC_SHA256,
        ikm, len(ikm),
        salt, len(salt),
        info, len(info),
        out, length)
    if rc != 0 {
        return nil, fmt.Errorf(
            "wolfcrypt.HKDFSHA256: Wc_HKDF: %d", rc)
    }
    return out, nil
}
