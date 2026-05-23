/* internal/credstore/seal.go - HKDF-derived AES-256-GCM seal and
 * unseal for credential records. Per PLAN.md Phase 18 decisions,
 * wolfCrypt-native primitives only (wc_HKDF + wc_AesGcm*).
 *
 * Layout produced by Seal and consumed by Unseal:
 *
 *   sealed = nonce(12) || ciphertext || tag(16)
 *
 * No length prefix, no version byte; both fields are fixed-size
 * by AES-GCM and the file is intentionally as small as possible.
 * If a future revision needs an algorithm switch, it can be
 * gated on a single magic byte added at file write time and
 * detected here without changing the AEAD wire format.
 */
package credstore

import (
    "errors"
    "fmt"

    "github.com/kaleb-himes/wolfCI/internal/wolfcrypt"
)

/* sealKeyLen is the size of the AES key derived via HKDF for
 * each credential. 32 bytes = AES-256.
 */
const sealKeyLen = 32

/* sealNonceLen is the AES-GCM nonce size in bytes. 12 is the
 * standard, the only size wolfcrypt.AESGCM{Seal,Open} accepts,
 * and what NIST SP 800-38D recommends.
 */
const sealNonceLen = 12

/* Seal returns the on-disk ciphertext of r under masterSecret +
 * credID. A fresh 12-byte nonce is drawn from wolfCrypt's CSPRNG
 * on every call (verified by tests/TestSeal_FreshNoncePerCall).
 *
 * The credential id is bound into the HKDF info parameter so a
 * ciphertext produced under id A cannot be opened under id B
 * even when both share masterSecret.
 *
 * Errors propagate from underlying wolfCrypt primitives; callers
 * should treat any non-nil error as fatal for the credential
 * (do not retry without rotating the master secret first).
 */
func Seal(masterSecret []byte, credID string,
    r *Record) ([]byte, error) {

    if r == nil {
        return nil, errors.New("credstore.Seal: nil Record")
    }
    if credID == "" {
        return nil, errors.New("credstore.Seal: empty credID")
    }
    if len(masterSecret) == 0 {
        return nil, errors.New(
            "credstore.Seal: empty masterSecret")
    }

    plaintext, err := r.Marshal()
    if err != nil {
        return nil, fmt.Errorf(
            "credstore.Seal: marshal record: %w", err)
    }

    key, err := deriveSealKey(masterSecret, credID)
    if err != nil {
        return nil, err
    }
    nonce, err := wolfcrypt.RandBytes(sealNonceLen)
    if err != nil {
        return nil, fmt.Errorf(
            "credstore.Seal: nonce: %w", err)
    }

    ctTag, err := wolfcrypt.AESGCMSeal(key, nonce, plaintext, nil)
    if err != nil {
        return nil, fmt.Errorf(
            "credstore.Seal: AESGCMSeal: %w", err)
    }

    sealed := make([]byte, 0, sealNonceLen+len(ctTag))
    sealed = append(sealed, nonce...)
    sealed = append(sealed, ctTag...)
    return sealed, nil
}

/* Unseal reverses Seal. The returned Record is owned by the
 * caller; mutating its Payload does not affect the sealed bytes.
 *
 * Errors:
 *   - input shorter than nonce+tag                  -> invalid
 *   - HKDF derivation failure                        -> propagate
 *   - AES-GCM tag rejection (tampered, wrong key,
 *     wrong credID, wrong masterSecret)              -> propagate
 *   - JSON Unmarshal of the recovered plaintext     -> propagate
 */
func Unseal(masterSecret []byte, credID string,
    sealed []byte) (*Record, error) {

    if credID == "" {
        return nil, errors.New("credstore.Unseal: empty credID")
    }
    if len(masterSecret) == 0 {
        return nil, errors.New(
            "credstore.Unseal: empty masterSecret")
    }
    if len(sealed) < sealNonceLen+wolfcrypt.AESGCMTagSize {
        return nil, fmt.Errorf(
            "credstore.Unseal: sealed length %d shorter than "+
                "nonce+tag (%d)", len(sealed),
            sealNonceLen+wolfcrypt.AESGCMTagSize)
    }

    nonce := sealed[:sealNonceLen]
    ctTag := sealed[sealNonceLen:]

    key, err := deriveSealKey(masterSecret, credID)
    if err != nil {
        return nil, err
    }
    plaintext, err := wolfcrypt.AESGCMOpen(key, nonce, ctTag, nil)
    if err != nil {
        return nil, fmt.Errorf(
            "credstore.Unseal: AESGCMOpen: %w", err)
    }

    rec := &Record{}
    if err := rec.Unmarshal(plaintext); err != nil {
        return nil, fmt.Errorf(
            "credstore.Unseal: parse record: %w", err)
    }
    return rec, nil
}

/* deriveSealKey runs HKDF-SHA256 with masterSecret as IKM, no
 * salt, and credID as the info parameter. The 32-byte output is
 * the AES-256 key the seal/open paths use. Exposed as a package
 * helper so future store-level code (delete-by-id, rotate) can
 * derive the same key.
 */
func deriveSealKey(masterSecret []byte,
    credID string) ([]byte, error) {

    key, err := wolfcrypt.HKDFSHA256(masterSecret, nil,
        []byte(credID), sealKeyLen)
    if err != nil {
        return nil, fmt.Errorf(
            "credstore: derive seal key: %w", err)
    }
    return key, nil
}
