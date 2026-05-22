package server_test

/* Gates PLAN.md task 11.2: on first server start with no users
 * under config-files/auth/keys/, wolfCI mints a one-time hex
 * token, prints a setup URL, and writes the token to the
 * bootstrap directory. BYOK: no keypair generation here.
 */

import (
    "encoding/hex"
    "os"
    "path/filepath"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/server"
)

func TestBootstrap_FirstStartMintsToken(t *testing.T) {
    dir := t.TempDir()
    keysDir := filepath.Join(dir, "keys")
    bootstrapDir := filepath.Join(dir, "bootstrap")
    if err := os.MkdirAll(keysDir, 0o755); err != nil {
        t.Fatalf("mkdir keys: %v", err)
    }

    b := &server.Bootstrap{
        KeysDir:      keysDir,
        BootstrapDir: bootstrapDir,
        ListenAddr:   "127.0.0.1:8443",
    }
    res, err := b.Mint()
    if err != nil {
        t.Fatalf("Mint: %v", err)
    }
    if res == nil {
        t.Fatal("Mint returned nil result with no admins present")
    }
    if res.Token == "" {
        t.Error("Token is empty")
    }
    if res.SetupURL == "" {
        t.Error("SetupURL is empty")
    }
    want := "https://127.0.0.1:8443/setup?token=" + res.Token
    if res.SetupURL != want {
        t.Errorf("SetupURL = %q, want %q", res.SetupURL, want)
    }

    /* Token file must exist with the exact hex content. */
    tokenPath := filepath.Join(bootstrapDir, "token")
    data, err := os.ReadFile(tokenPath)
    if err != nil {
        t.Fatalf("read token file: %v", err)
    }
    if string(data) != res.Token {
        t.Errorf("token file contents = %q, want %q",
            string(data), res.Token)
    }
}

func TestBootstrap_SkipsWhenAdminsExist(t *testing.T) {
    dir := t.TempDir()
    keysDir := filepath.Join(dir, "keys")
    bootstrapDir := filepath.Join(dir, "bootstrap")
    if err := os.MkdirAll(keysDir, 0o755); err != nil {
        t.Fatalf("mkdir keys: %v", err)
    }
    /* Any existing *.pub means an admin is already provisioned. */
    if err := os.WriteFile(
        filepath.Join(keysDir, "alice.pub"),
        []byte("ssh-ed25519 AAAA..."),
        0o644,
    ); err != nil {
        t.Fatalf("seed alice.pub: %v", err)
    }

    b := &server.Bootstrap{
        KeysDir:      keysDir,
        BootstrapDir: bootstrapDir,
        ListenAddr:   "127.0.0.1:8443",
    }
    res, err := b.Mint()
    if err != nil {
        t.Fatalf("Mint: %v", err)
    }
    if res != nil {
        t.Errorf("Mint returned %+v, want nil when admins exist", res)
    }
    if _, err := os.Stat(bootstrapDir); !os.IsNotExist(err) {
        t.Errorf("bootstrap dir was created despite existing admin: stat err=%v", err)
    }
}

func TestBootstrap_TokenFormat(t *testing.T) {
    dir := t.TempDir()
    keysDir := filepath.Join(dir, "keys")
    bootstrapDir := filepath.Join(dir, "bootstrap")
    if err := os.MkdirAll(keysDir, 0o755); err != nil {
        t.Fatalf("mkdir keys: %v", err)
    }

    b := &server.Bootstrap{
        KeysDir:      keysDir,
        BootstrapDir: bootstrapDir,
        ListenAddr:   "127.0.0.1:8443",
    }
    res, err := b.Mint()
    if err != nil {
        t.Fatalf("Mint: %v", err)
    }
    /* 32 random bytes hex-encoded = 64 lowercase hex chars. */
    if len(res.Token) != 64 {
        t.Errorf("len(Token) = %d, want 64", len(res.Token))
    }
    raw, err := hex.DecodeString(res.Token)
    if err != nil {
        t.Errorf("token is not hex: %v", err)
    }
    if len(raw) != 32 {
        t.Errorf("decoded token length = %d, want 32", len(raw))
    }
    for _, c := range res.Token {
        if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
            t.Errorf("token contains non-lowercase-hex char %q", c)
            break
        }
    }
}

func TestBootstrap_FilePermissions(t *testing.T) {
    dir := t.TempDir()
    keysDir := filepath.Join(dir, "keys")
    bootstrapDir := filepath.Join(dir, "bootstrap")
    if err := os.MkdirAll(keysDir, 0o755); err != nil {
        t.Fatalf("mkdir keys: %v", err)
    }

    b := &server.Bootstrap{
        KeysDir:      keysDir,
        BootstrapDir: bootstrapDir,
        ListenAddr:   "127.0.0.1:8443",
    }
    if _, err := b.Mint(); err != nil {
        t.Fatalf("Mint: %v", err)
    }

    info, err := os.Stat(filepath.Join(bootstrapDir, "token"))
    if err != nil {
        t.Fatalf("stat token: %v", err)
    }
    if mode := info.Mode().Perm(); mode != 0o600 {
        t.Errorf("token file perm = %o, want 0600", mode)
    }
    dinfo, err := os.Stat(bootstrapDir)
    if err != nil {
        t.Fatalf("stat bootstrap dir: %v", err)
    }
    if mode := dinfo.Mode().Perm(); mode != 0o700 {
        t.Errorf("bootstrap dir perm = %o, want 0700", mode)
    }
}
