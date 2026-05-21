package auth_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kaleb-himes/wolfCI/internal/auth"
)

// TestPasswordStore_VerifyWhenEnabled is the gating test for
// PLAN.md task 3.3. With password auth enabled, the correct
// password verifies and a wrong password is rejected.
func TestPasswordStore_VerifyWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	cfg := &auth.Config{PasswordEnabled: true, BcryptCost: 4} // min cost; tests run fast
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

// TestPasswordStore_DisabledRejectsAll is the gating test for
// PLAN.md task 3.4's "disabled by default" requirement. With
// PasswordEnabled false, every password attempt is rejected
// regardless of whether a hash exists on disk.
func TestPasswordStore_DisabledRejectsAll(t *testing.T) {
	dir := t.TempDir()
	cfg := &auth.Config{PasswordEnabled: true, BcryptCost: 4}
	ps := auth.NewPasswordStore(dir, cfg)
	if err := ps.SetPassword("alice", "hunter2"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	// Flip the toggle off; the hash on disk remains.
	cfg.PasswordEnabled = false

	if err := ps.VerifyPassword("alice", "hunter2"); !errors.Is(err, auth.ErrPasswordAuthDisabled) {
		t.Fatalf("VerifyPassword when disabled: %v, want ErrPasswordAuthDisabled", err)
	}
}

// TestConfig_DefaultsAndRoundtrip pins the default Config values
// (PasswordEnabled false, BcryptCost 12) and verifies that
// Save/LoadConfig is a faithful round trip.
func TestConfig_DefaultsAndRoundtrip(t *testing.T) {
	def := auth.DefaultConfig()
	if def.PasswordEnabled {
		t.Errorf("DefaultConfig.PasswordEnabled = true, want false")
	}
	if def.BcryptCost != 12 {
		t.Errorf("DefaultConfig.BcryptCost = %d, want 12", def.BcryptCost)
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

// TestLoadConfig_RejectsOutOfRangeCost ensures the config loader
// catches a wildly out-of-range bcrypt cost.
func TestLoadConfig_RejectsOutOfRangeCost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("password_enabled: false\nbcrypt_cost: 99\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := auth.LoadConfig(path); err == nil {
		t.Fatal("LoadConfig: nil error for bcrypt_cost=99, want range error")
	}
}
