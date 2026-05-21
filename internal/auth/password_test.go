package auth_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/auth"
)

// testIterations is a low PBKDF2 iteration count so the password
// suite still runs in milliseconds. Production defaults to 600000
// (OWASP 2023 guidance for PBKDF2-HMAC-SHA-256).
const testIterations = 1000

// TestPasswordStore_VerifyWhenEnabled: with password auth enabled,
// the correct password verifies and a wrong password is rejected.
// Same functional contract as the original bcrypt-backed test;
// Phase 10.2 swaps the underlying KDF to wolfCrypt PBKDF2.
func TestPasswordStore_VerifyWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	cfg := &auth.Config{
		PasswordEnabled:  true,
		PBKDF2Iterations: testIterations,
	}
	ps := auth.NewPasswordStore(dir, cfg)

	if err := ps.SetPassword("alice", "hunter2"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	if err := ps.VerifyPassword("alice", "hunter2"); err != nil {
		t.Fatalf("VerifyPassword(correct): %v, want nil", err)
	}
	if err := ps.VerifyPassword("alice", "wrong"); !errors.Is(err, auth.ErrInvalidPassword) {
		t.Fatalf("VerifyPassword(wrong): %v, want ErrInvalidPassword", err)
	}
	if err := ps.VerifyPassword("bob", "anything"); !errors.Is(err, auth.ErrUnknownUser) {
		t.Fatalf("VerifyPassword(unknown user): %v, want ErrUnknownUser", err)
	}
}

// TestPasswordStore_DisabledRejectsAll: with PasswordEnabled false,
// every password attempt is rejected without touching disk.
func TestPasswordStore_DisabledRejectsAll(t *testing.T) {
	dir := t.TempDir()
	cfg := &auth.Config{PasswordEnabled: true, PBKDF2Iterations: testIterations}
	ps := auth.NewPasswordStore(dir, cfg)
	if err := ps.SetPassword("alice", "hunter2"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	cfg.PasswordEnabled = false

	if err := ps.VerifyPassword("alice", "hunter2"); !errors.Is(err, auth.ErrPasswordAuthDisabled) {
		t.Fatalf("VerifyPassword when disabled: %v, want ErrPasswordAuthDisabled", err)
	}
}

// TestConfig_DefaultsAndRoundtrip pins the new defaults
// (PasswordEnabled false, PBKDF2Iterations 600000, PBKDF2SaltBytes 16)
// and round-trips Save/LoadConfig.
func TestConfig_DefaultsAndRoundtrip(t *testing.T) {
	def := auth.DefaultConfig()
	if def.PasswordEnabled {
		t.Errorf("DefaultConfig.PasswordEnabled = true, want false")
	}
	if def.PBKDF2Iterations != 600000 {
		t.Errorf("DefaultConfig.PBKDF2Iterations = %d, want 600000", def.PBKDF2Iterations)
	}
	if def.PBKDF2SaltBytes != 16 {
		t.Errorf("DefaultConfig.PBKDF2SaltBytes = %d, want 16", def.PBKDF2SaltBytes)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "auth", "config.yaml")
	if err := def.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := auth.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !reflect.DeepEqual(def, loaded) {
		t.Fatalf("Config round-trip mismatch.\nsaved:  %+v\nloaded: %+v", def, loaded)
	}
}

// TestLoadConfig_RejectsOutOfRangeIterations catches a wildly
// out-of-range iteration count. Too-low iterations defeat the KDF;
// too-high iterations are a DoS vector against legitimate logins.
func TestLoadConfig_RejectsOutOfRangeIterations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path,
		[]byte("password_enabled: false\npbkdf2_iterations: 50\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := auth.LoadConfig(path); err == nil {
		t.Fatal("LoadConfig: nil error for pbkdf2_iterations=50, want range error")
	}
}

// TestPasswordStore_OnDiskFormat pins the new file layout:
// extension is .pbkdf2 (not .bcrypt), and the first line of the
// file is the version sentinel so a wrong-KDF blob can be rejected
// without trying to use it.
func TestPasswordStore_OnDiskFormat(t *testing.T) {
	dir := t.TempDir()
	cfg := &auth.Config{PasswordEnabled: true, PBKDF2Iterations: testIterations}
	ps := auth.NewPasswordStore(dir, cfg)
	if err := ps.SetPassword("alice", "hunter2"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	got := filepath.Join(dir, "alice.pbkdf2")
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("expected hash file at %s: %v", got, err)
	}

	bogus := filepath.Join(dir, "alice.bcrypt")
	if _, err := os.Stat(bogus); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy %s should not exist; got err=%v", bogus, err)
	}

	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.HasPrefix(string(data), "wolfci-pbkdf2-v1\n") {
		t.Fatalf("hash file does not start with wolfci-pbkdf2-v1 sentinel: %q",
			string(data[:min(len(data), 32)]))
	}
}

// TestPasswordStore_RejectsWrongKDFHeader: if the hash file has a
// different sentinel (e.g. a leftover bcrypt blob, or a corrupted
// header), VerifyPassword must refuse it instead of mis-using the
// bytes as a PBKDF2 record.
func TestPasswordStore_RejectsWrongKDFHeader(t *testing.T) {
	dir := t.TempDir()
	cfg := &auth.Config{PasswordEnabled: true, PBKDF2Iterations: testIterations}
	ps := auth.NewPasswordStore(dir, cfg)

	// Write a hand-rolled file with a wrong sentinel.
	wrong := filepath.Join(dir, "alice.pbkdf2")
	if err := os.WriteFile(wrong, []byte("wolfci-pbkdf2-v2\nsomething\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := ps.VerifyPassword("alice", "anything")
	if err == nil {
		t.Fatal("VerifyPassword on wrong-sentinel file: nil error; want rejection")
	}
	if errors.Is(err, auth.ErrInvalidPassword) {
		// Either treating it as invalid-password or returning a
		// structural error is acceptable - never (nil) for a
		// wrong-KDF file.
		return
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
