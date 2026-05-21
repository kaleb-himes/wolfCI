// Package wolfcrypt gate tests for Phase 10.1a: RandBytes,
// HMACSHA256, PBKDF2HMACSHA256.
//
// Phase 10 rule: every cryptographic operation in this tree must
// come from wolfCrypt. These tests pin the public API and prove
// the primitives match published test vectors so call sites can
// switch off the stdlib equivalents in 10.2 onward.
package wolfcrypt_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/wolfcrypt"
)

// TestRandBytes_NonZero exercises the wolfCrypt RNG by drawing
// many 32-byte blocks and asserting they are not all zero (catches
// a missing wc_InitRng) and not all identical (catches a static
// buffer reuse). The test does not check distribution; that is what
// the wolfCrypt unit tests are for. We only verify the bridge.
func TestRandBytes_NonZero(t *testing.T) {
	const n = 1000
	const size = 32
	seen := make(map[string]struct{}, n)
	allZero := bytes.Repeat([]byte{0}, size)
	for i := 0; i < n; i++ {
		buf, err := wolfcrypt.RandBytes(size)
		if err != nil {
			t.Fatalf("RandBytes(%d) iter %d: %v", size, i, err)
		}
		if len(buf) != size {
			t.Fatalf("RandBytes(%d) returned %d bytes", size, len(buf))
		}
		if bytes.Equal(buf, allZero) {
			t.Fatalf("RandBytes(%d) returned all-zero at iter %d", size, i)
		}
		seen[string(buf)] = struct{}{}
	}
	if len(seen) < n-1 {
		// A single collision in 1000 draws of 32 bytes is
		// astronomically unlikely with a working CSPRNG; more than
		// one means the buffer is not being filled.
		t.Fatalf("RandBytes appears to reuse output: %d unique of %d", len(seen), n)
	}
}

// TestRandBytes_SizeZero is the small-input edge case.
func TestRandBytes_SizeZero(t *testing.T) {
	buf, err := wolfcrypt.RandBytes(0)
	if err != nil {
		t.Fatalf("RandBytes(0): %v", err)
	}
	if len(buf) != 0 {
		t.Fatalf("RandBytes(0) returned %d bytes", len(buf))
	}
}

// TestHMACSHA256_RFC4231_TC1 pins HMAC-SHA-256 to RFC 4231 test
// case 1, the canonical known-answer test:
//
//	Key  = 0x0b repeated 20 times
//	Data = "Hi There"
//	MAC  = b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7
func TestHMACSHA256_RFC4231_TC1(t *testing.T) {
	key := bytes.Repeat([]byte{0x0b}, 20)
	data := []byte("Hi There")
	want, _ := hex.DecodeString(
		"b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7",
	)
	got, err := wolfcrypt.HMACSHA256(key, data)
	if err != nil {
		t.Fatalf("HMACSHA256: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("HMACSHA256\n got = %x\nwant = %x", got, want)
	}
}

// TestHMACSHA256_RFC4231_TC2 pins HMAC-SHA-256 to RFC 4231 test
// case 2, which exercises a short key (4 bytes):
//
//	Key  = "Jefe"
//	Data = "what do ya want for nothing?"
//	MAC  = 5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843
func TestHMACSHA256_RFC4231_TC2(t *testing.T) {
	key := []byte("Jefe")
	data := []byte("what do ya want for nothing?")
	want, _ := hex.DecodeString(
		"5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843",
	)
	got, err := wolfcrypt.HMACSHA256(key, data)
	if err != nil {
		t.Fatalf("HMACSHA256: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("HMACSHA256\n got = %x\nwant = %x", got, want)
	}
}

// TestPBKDF2_RFC7914_Param exercises PBKDF2-HMAC-SHA-256 with the
// parameter set RFC 7914 uses to illustrate scrypt's PBKDF2 step
// (also a useful KAT for the HMAC-SHA-256 variant directly):
//
//	P = "passwd"
//	S = "salt"
//	c = 1
//	dkLen = 64
//	DK = 55ac046e56e3089fec1691c22544b605
//	     f94185216dde0465e68b9d57c20dacbc
//	     49ca9cccf179b645991664b39d77ef31
//	     7c71b845b1e30bd509112041d3a19783
func TestPBKDF2_RFC7914_Param(t *testing.T) {
	password := []byte("passwd")
	salt := []byte("salt")
	iterations := 1
	keyLen := 64
	want, _ := hex.DecodeString(
		"55ac046e56e3089fec1691c22544b605" +
			"f94185216dde0465e68b9d57c20dacbc" +
			"49ca9cccf179b645991664b39d77ef31" +
			"7c71b845b1e30bd509112041d3a19783",
	)
	got, err := wolfcrypt.PBKDF2HMACSHA256(password, salt, iterations, keyLen)
	if err != nil {
		t.Fatalf("PBKDF2HMACSHA256: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("PBKDF2HMACSHA256\n got = %x\nwant = %x", got, want)
	}
}

// TestPBKDF2_Determinism verifies that the same inputs produce the
// same output (catches accidental nondeterminism in the CGO bridge,
// e.g. salt mixing with stale buffer contents).
func TestPBKDF2_Determinism(t *testing.T) {
	password := []byte("hunter2")
	salt := []byte("salty mc saltface")
	const iterations = 1000
	const keyLen = 32
	a, err := wolfcrypt.PBKDF2HMACSHA256(password, salt, iterations, keyLen)
	if err != nil {
		t.Fatalf("PBKDF2HMACSHA256: %v", err)
	}
	b, err := wolfcrypt.PBKDF2HMACSHA256(password, salt, iterations, keyLen)
	if err != nil {
		t.Fatalf("PBKDF2HMACSHA256 second call: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("PBKDF2 not deterministic:\n a = %x\n b = %x", a, b)
	}
}

// TestPBKDF2_DifferentSaltsDiverge verifies salts actually mix in
// (catches a stub implementation that ignores salt).
func TestPBKDF2_DifferentSaltsDiverge(t *testing.T) {
	a, err := wolfcrypt.PBKDF2HMACSHA256([]byte("p"), []byte("salt-a"), 100, 32)
	if err != nil {
		t.Fatalf("PBKDF2HMACSHA256(salt-a): %v", err)
	}
	b, err := wolfcrypt.PBKDF2HMACSHA256([]byte("p"), []byte("salt-b"), 100, 32)
	if err != nil {
		t.Fatalf("PBKDF2HMACSHA256(salt-b): %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatalf("PBKDF2 ignored salt: salt-a and salt-b produced identical output")
	}
}
