package server_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kaleb-himes/wolfCI/internal/server"
)

// TestSessionStore_CreateLookupRoundtrip is the basic round-trip
// gate for Phase 10.4 (and any future entropy-source swap):
//   - Two sessions for the same username get distinct tokens.
//   - Tokens are 64 lowercase hex chars.
//   - Lookup of a freshly-minted token returns the same Session.
//   - Lookup of a syntactically invalid token errors out before
//     touching the filesystem.
//
// The crypto/rand -> wolfcrypt.RandBytes swap in 10.4 must keep
// every one of these properties.
func TestSessionStore_CreateLookupRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store := server.NewSessionStore(filepath.Join(dir, "sessions"), time.Hour)

	a, err := store.Create("alice")
	if err != nil {
		t.Fatalf("Create(alice) #1: %v", err)
	}
	b, err := store.Create("alice")
	if err != nil {
		t.Fatalf("Create(alice) #2: %v", err)
	}
	if a.Token == b.Token {
		t.Fatal("two Create calls returned identical tokens; entropy source broken")
	}
	if len(a.Token) != 64 {
		t.Fatalf("token = %d chars, want 64", len(a.Token))
	}
	for _, c := range a.Token {
		ok := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !ok {
			t.Fatalf("token contains non-lowercase-hex char %q", c)
		}
	}

	got, err := store.Lookup(a.Token)
	if err != nil {
		t.Fatalf("Lookup(a): %v", err)
	}
	if got.Token != a.Token || got.Username != "alice" {
		t.Fatalf("Lookup roundtrip mismatch: got %+v, want token=%s, alice", got, a.Token)
	}

	if _, err := store.Lookup("definitely-not-64-hex"); err == nil {
		t.Fatal("Lookup(bad-token) accepted; want error before filesystem")
	}
}
