package wolfcrypt_test

/* internal/wolfcrypt/aead_test.go - PLAN.md 18.2 gating tests.
 *
 * Exercises the wolfCrypt seal/unseal primitive (Wc_HKDF +
 * Wc_AesGcm*) through the internal/wolfcrypt bridge layer.
 *
 * Phase 18.1 shipped both these primitives and the higher-level
 * internal/credstore.Seal/Unseal that layers on top. 18.2's
 * value is a primitive-level gate that is independent of the
 * credstore record schema: if a future change retires the JSON
 * record shape, these tests still confirm the AES-GCM + HKDF
 * wiring works end-to-end through go-wolfssl.
 *
 * Per CLAUDE.md rule 11 and PLAN.md 18.2: NO crypto/hkdf, NO
 * golang.org/x/crypto fallback. All operations go through
 * github.com/wolfssl/go-wolfssl via the internal/wolfcrypt
 * package.
 */

import (
    "bytes"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/wolfcrypt"
)

/* TestSeal_RoundTrip is the gating test name PLAN.md 18.2 calls
 * out. It exercises the full HKDF -> AES-GCM seal/open path with
 * a fixed master secret and fixed info parameter so the result is
 * reproducible.
 */
func TestSeal_RoundTrip(t *testing.T) {
    master := bytes.Repeat([]byte{0xA5}, 32)
    info := []byte("wolfci/credstore/test")
    plaintext := []byte("PLAN.md 18.2 - wolfCrypt seal/unseal " +
        "round-trip via Wc_HKDF + Wc_AesGcm")

    key, err := wolfcrypt.HKDFSHA256(master, nil, info, 32)
    if err != nil {
        t.Fatalf("HKDFSHA256: %v", err)
    }
    if len(key) != 32 {
        t.Fatalf("derived key length = %d, want 32", len(key))
    }

    nonce, err := wolfcrypt.RandBytes(12)
    if err != nil {
        t.Fatalf("RandBytes: %v", err)
    }

    sealed, err := wolfcrypt.AESGCMSeal(key, nonce, plaintext, nil)
    if err != nil {
        t.Fatalf("AESGCMSeal: %v", err)
    }
    if len(sealed) != len(plaintext)+wolfcrypt.AESGCMTagSize {
        t.Fatalf("sealed length = %d, want %d",
            len(sealed),
            len(plaintext)+wolfcrypt.AESGCMTagSize)
    }

    got, err := wolfcrypt.AESGCMOpen(key, nonce, sealed, nil)
    if err != nil {
        t.Fatalf("AESGCMOpen: %v", err)
    }
    if !bytes.Equal(got, plaintext) {
        t.Errorf("plaintext mismatch:\n got %q\nwant %q",
            got, plaintext)
    }
}

/* TestHKDFSHA256_DistinguishesInfo: two HKDF outputs derived from
 * the same IKM but different info parameters must differ. This is
 * the property the credstore relies on to bind the credential id
 * into the per-credential key, so it deserves its own gate at
 * the primitive layer.
 */
func TestHKDFSHA256_DistinguishesInfo(t *testing.T) {
    master := bytes.Repeat([]byte{0x11}, 32)
    keyA, err := wolfcrypt.HKDFSHA256(master, nil,
        []byte("info-A"), 32)
    if err != nil {
        t.Fatalf("HKDFSHA256 A: %v", err)
    }
    keyB, err := wolfcrypt.HKDFSHA256(master, nil,
        []byte("info-B"), 32)
    if err != nil {
        t.Fatalf("HKDFSHA256 B: %v", err)
    }
    if bytes.Equal(keyA, keyB) {
        t.Fatalf("HKDF info parameter ignored; keys equal")
    }
}

/* TestHKDFSHA256_Deterministic: HKDF is a deterministic function;
 * the same (IKM, salt, info, length) tuple must produce the same
 * output bytes on every call. credstore.Unseal relies on this to
 * recover the seal key from the master secret + credID.
 */
func TestHKDFSHA256_Deterministic(t *testing.T) {
    master := bytes.Repeat([]byte{0x22}, 32)
    info := []byte("deterministic")

    a, err := wolfcrypt.HKDFSHA256(master, nil, info, 48)
    if err != nil {
        t.Fatalf("a: %v", err)
    }
    b, err := wolfcrypt.HKDFSHA256(master, nil, info, 48)
    if err != nil {
        t.Fatalf("b: %v", err)
    }
    if !bytes.Equal(a, b) {
        t.Fatalf("HKDF nondeterministic; got two different "+
            "outputs for identical inputs:\n a=%x\n b=%x",
            a, b)
    }
}

/* TestAESGCMOpen_RejectsTampering: the appended GCM tag must
 * reject any single-byte modification to the ciphertext, the
 * nonce (passed separately so not really part of the "sealed"
 * blob here), or the tag itself.
 */
func TestAESGCMOpen_RejectsTampering(t *testing.T) {
    key := bytes.Repeat([]byte{0x33}, 32)
    nonce := bytes.Repeat([]byte{0x44}, 12)
    plaintext := []byte("do not tamper")

    sealed, err := wolfcrypt.AESGCMSeal(key, nonce, plaintext, nil)
    if err != nil {
        t.Fatalf("AESGCMSeal: %v", err)
    }
    for i := 0; i < len(sealed); i++ {
        bad := append([]byte(nil), sealed...)
        bad[i] ^= 0x80
        if _, err := wolfcrypt.AESGCMOpen(
            key, nonce, bad, nil); err == nil {
            t.Errorf(
                "byte %d flip accepted; auth tag did not reject",
                i)
        }
    }
}

/* TestAESGCMOpen_RejectsWrongKey: changing the key must make the
 * tag fail. This is the property HKDFSHA256_DistinguishesInfo
 * cascades into for credstore (the wrong credID derives the
 * wrong key, which then fails open here).
 */
func TestAESGCMOpen_RejectsWrongKey(t *testing.T) {
    keyA := bytes.Repeat([]byte{0x55}, 32)
    keyB := bytes.Repeat([]byte{0x56}, 32)
    nonce := bytes.Repeat([]byte{0x77}, 12)
    plaintext := []byte("payload")

    sealed, err := wolfcrypt.AESGCMSeal(keyA, nonce, plaintext, nil)
    if err != nil {
        t.Fatalf("AESGCMSeal: %v", err)
    }
    if _, err := wolfcrypt.AESGCMOpen(
        keyB, nonce, sealed, nil); err == nil {
        t.Fatalf("Open under wrong key succeeded; expected "+
            "auth tag failure")
    }
}

/* TestAESGCMSeal_AADBound: the additional-authenticated-data
 * argument participates in the tag. Sealing with one aad and
 * opening with another must fail.
 */
func TestAESGCMSeal_AADBound(t *testing.T) {
    key := bytes.Repeat([]byte{0x88}, 32)
    nonce := bytes.Repeat([]byte{0x99}, 12)
    plaintext := []byte("aad test")

    sealed, err := wolfcrypt.AESGCMSeal(key, nonce, plaintext,
        []byte("context-A"))
    if err != nil {
        t.Fatalf("AESGCMSeal: %v", err)
    }
    if _, err := wolfcrypt.AESGCMOpen(key, nonce, sealed,
        []byte("context-B")); err == nil {
        t.Fatalf("Open under wrong AAD succeeded; expected " +
            "auth tag failure")
    }
    /* And the matching AAD must succeed. */
    if _, err := wolfcrypt.AESGCMOpen(key, nonce, sealed,
        []byte("context-A")); err != nil {
        t.Fatalf("Open under matching AAD failed: %v", err)
    }
}

/* TestAESGCMSeal_RejectsBadKeyLen: the wrapper must reject any
 * key length other than 16, 24, or 32. Encoding this at the
 * Go-level prevents a stray copy-paste from feeding wolfSSL a
 * key the cipher cannot use.
 */
func TestAESGCMSeal_RejectsBadKeyLen(t *testing.T) {
    nonce := bytes.Repeat([]byte{0xAA}, 12)
    for _, bad := range []int{0, 1, 15, 17, 23, 25, 31, 33, 64} {
        key := make([]byte, bad)
        if _, err := wolfcrypt.AESGCMSeal(key, nonce, []byte("x"),
            nil); err == nil {
            t.Errorf(
                "AESGCMSeal accepted key length %d", bad)
        }
    }
}

/* TestAESGCMSeal_RejectsBadNonceLen mirrors the key-length gate
 * for the 12-byte nonce requirement.
 */
func TestAESGCMSeal_RejectsBadNonceLen(t *testing.T) {
    key := bytes.Repeat([]byte{0xBB}, 32)
    for _, bad := range []int{0, 1, 11, 13, 16, 24} {
        nonce := make([]byte, bad)
        if _, err := wolfcrypt.AESGCMSeal(key, nonce,
            []byte("x"), nil); err == nil {
            t.Errorf(
                "AESGCMSeal accepted nonce length %d", bad)
        }
    }
}
