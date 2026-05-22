package server_test

/* Gates PLAN.md task 11.3: the /setup endpoint consumes a
 * bootstrap token (Phase 11.2) by writing the operator's pasted
 * OpenSSH pubkey to keys/<username>.pub, adding the username as
 * an admin in matrix.yaml, and renaming the bootstrap dir to
 * bootstrap.consumed/ so no second submission can happen.
 *
 * Tests build real Ed25519 keypairs via wolfcrypt + gowolfssh
 * (test-only keygen is explicitly allowed by feedback memory
 * byok-no-keygen; production wolfCI never calls Ed25519GenKey
 * for user-facing credentials).
 */

import (
    "net/http"
    "net/http/httptest"
    "net/url"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/auth"
    "github.com/kaleb-himes/wolfCI/internal/server"
    "github.com/kaleb-himes/wolfCI/internal/wolfcrypt"

    gowolfssh "github.com/wolfssl/go-wolfssl/wolfssh"
)

/* setupFixture mints a bootstrap token, builds an Ed25519 pubkey
 * line, and returns the SetupHandler under test. Each subtest
 * gets a fresh temp dir.
 */
type setupFixture struct {
    handler        *server.SetupHandler
    keysDir        string
    bootstrapDir   string
    matrixPath     string
    authCfg        *auth.Config
    authConfigPath string
    passwords      *auth.PasswordStore
    token          string
    pubKeyLine     string
}

const testPassword = "correct horse battery staple"

func newSetupFixture(t *testing.T) *setupFixture {
    t.Helper()
    dir := t.TempDir()
    keysDir := filepath.Join(dir, "keys")
    bootstrapDir := filepath.Join(dir, "bootstrap")
    matrixPath := filepath.Join(dir, "matrix.yaml")
    if err := os.MkdirAll(keysDir, 0o755); err != nil {
        t.Fatalf("mkdir keys: %v", err)
    }

    b := &server.Bootstrap{
        KeysDir:      keysDir,
        BootstrapDir: bootstrapDir,
        ListenAddr:   "127.0.0.1:8443",
    }
    mintRes, err := b.Mint()
    if err != nil {
        t.Fatalf("Bootstrap.Mint: %v", err)
    }

    pub, _, err := wolfcrypt.Ed25519GenKey()
    if err != nil {
        t.Fatalf("Ed25519GenKey: %v", err)
    }
    pubLine := string(gowolfssh.EncodeSSHEd25519AuthorizedKey(pub, "test-key"))

    authCfg := auth.DefaultConfig()
    /* Cheap iteration count so the test does not spend a second
     * per PBKDF2 derivation; production defaults to 600000.
     */
    authCfg.PBKDF2Iterations = 1000
    passwords := auth.NewPasswordStore(filepath.Join(dir, "passwords"), authCfg)
    authConfigPath := filepath.Join(dir, "auth", "config.yaml")

    return &setupFixture{
        handler: &server.SetupHandler{
            KeysDir:        keysDir,
            BootstrapDir:   bootstrapDir,
            MatrixPath:     matrixPath,
            Passwords:      passwords,
            AuthConfig:     authCfg,
            AuthConfigPath: authConfigPath,
        },
        keysDir:        keysDir,
        bootstrapDir:   bootstrapDir,
        matrixPath:     matrixPath,
        authCfg:        authCfg,
        authConfigPath: authConfigPath,
        passwords:      passwords,
        token:          mintRes.Token,
        pubKeyLine:     pubLine,
    }
}

func (f *setupFixture) postForm(t *testing.T, token, user, pubkey string) *httptest.ResponseRecorder {
    t.Helper()
    return f.postFormPw(t, token, user, pubkey, testPassword, testPassword)
}

func (f *setupFixture) postFormPw(t *testing.T, token, user, pubkey, pw, pwConfirm string) *httptest.ResponseRecorder {
    t.Helper()
    form := url.Values{}
    form.Set("token", token)
    form.Set("username", user)
    form.Set("pubkey", pubkey)
    form.Set("password", pw)
    form.Set("password_confirm", pwConfirm)
    req := httptest.NewRequest(http.MethodPost, "/setup",
        strings.NewReader(form.Encode()))
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    rec := httptest.NewRecorder()
    f.handler.ServeHTTP(rec, req)
    return rec
}

func TestSetup_AcceptsValidToken(t *testing.T) {
    f := newSetupFixture(t)

    /* GET serves the form. */
    req := httptest.NewRequest(http.MethodGet,
        "/setup?token="+f.token, nil)
    rec := httptest.NewRecorder()
    f.handler.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK {
        t.Errorf("GET status = %d, want 200", rec.Code)
    }
    body := rec.Body.String()
    if !strings.Contains(body, `<form`) {
        t.Errorf("GET body missing <form>: %q", body)
    }
    if !strings.Contains(body, f.token) {
        t.Error("GET body must echo the token back so POST can carry it")
    }

    /* POST consumes the token. */
    rec = f.postForm(t, f.token, "alice", f.pubKeyLine)
    if rec.Code < 200 || rec.Code >= 400 {
        t.Errorf("POST status = %d, want 2xx/3xx, body=%q",
            rec.Code, rec.Body.String())
    }
    if _, err := os.Stat(filepath.Join(f.keysDir, "alice.pub")); err != nil {
        t.Errorf("alice.pub not written: %v", err)
    }
}

func TestSetup_RejectsInvalidToken(t *testing.T) {
    f := newSetupFixture(t)

    rec := f.postForm(t, "not-the-real-token", "alice", f.pubKeyLine)
    if rec.Code != http.StatusForbidden {
        t.Errorf("status = %d, want 403", rec.Code)
    }
    if _, err := os.Stat(filepath.Join(f.keysDir, "alice.pub")); !os.IsNotExist(err) {
        t.Errorf("alice.pub leaked on rejected POST: err=%v", err)
    }
}

func TestSetup_RejectsAfterConsumption(t *testing.T) {
    f := newSetupFixture(t)

    /* First POST succeeds. */
    rec := f.postForm(t, f.token, "alice", f.pubKeyLine)
    if rec.Code >= 400 {
        t.Fatalf("first POST status = %d, want success", rec.Code)
    }

    /* Second POST with the same token must be rejected.
     * After consumption the bootstrap dir no longer holds a
     * token, so the loader returns "no active bootstrap".
     */
    rec2 := f.postForm(t, f.token, "bob", f.pubKeyLine)
    if rec2.Code != http.StatusGone && rec2.Code != http.StatusForbidden {
        t.Errorf("second POST status = %d, want 410 or 403", rec2.Code)
    }
    if _, err := os.Stat(filepath.Join(f.keysDir, "bob.pub")); !os.IsNotExist(err) {
        t.Errorf("bob.pub leaked on second POST: err=%v", err)
    }
}

func TestSetup_RejectsMalformedPubkey(t *testing.T) {
    f := newSetupFixture(t)

    rec := f.postForm(t, f.token, "alice", "this is not an openssh pubkey")
    if rec.Code != http.StatusBadRequest {
        t.Errorf("status = %d, want 400", rec.Code)
    }
    /* The bootstrap dir must still exist (token un-consumed) so
     * the operator can retry with a valid key.
     */
    if _, err := os.Stat(f.bootstrapDir); err != nil {
        t.Errorf("bootstrap dir destroyed on bad-pubkey POST: %v", err)
    }
}

func TestSetup_RegistersAdminInMatrix(t *testing.T) {
    f := newSetupFixture(t)

    rec := f.postForm(t, f.token, "alice", f.pubKeyLine)
    if rec.Code >= 400 {
        t.Fatalf("POST status = %d", rec.Code)
    }

    /* matrix.yaml must exist and map alice -> admin. */
    data, err := os.ReadFile(f.matrixPath)
    if err != nil {
        t.Fatalf("read matrix.yaml: %v", err)
    }
    s := string(data)
    if !strings.Contains(s, "alice") {
        t.Errorf("matrix.yaml missing alice: %q", s)
    }
    if !strings.Contains(s, "admin") {
        t.Errorf("matrix.yaml missing admin role: %q", s)
    }
}

func TestSetup_RenamesBootstrapDir(t *testing.T) {
    f := newSetupFixture(t)

    rec := f.postForm(t, f.token, "alice", f.pubKeyLine)
    if rec.Code >= 400 {
        t.Fatalf("POST status = %d", rec.Code)
    }

    if _, err := os.Stat(f.bootstrapDir); !os.IsNotExist(err) {
        t.Errorf("bootstrap dir still exists post-consumption: err=%v", err)
    }
    consumedDir := f.bootstrapDir + ".consumed"
    info, err := os.Stat(consumedDir)
    if err != nil {
        t.Fatalf("bootstrap.consumed/ missing: %v", err)
    }
    if !info.IsDir() {
        t.Errorf("bootstrap.consumed is not a directory")
    }
}

func TestSetup_StoresPasswordAndEnablesPasswordAuth(t *testing.T) {
    f := newSetupFixture(t)
    rec := f.postForm(t, f.token, "alice", f.pubKeyLine)
    if rec.Code >= 400 {
        t.Fatalf("POST status = %d, body=%q", rec.Code, rec.Body.String())
    }

    /* Password store accepts the chosen password. */
    if err := f.passwords.VerifyPassword("alice", testPassword); err != nil {
        t.Errorf("VerifyPassword(alice, ...): %v", err)
    }

    /* Wrong password is still wrong. */
    if err := f.passwords.VerifyPassword("alice", "wrong"); err == nil {
        t.Error("VerifyPassword accepted wrong password")
    }

    /* In-memory flag flipped. */
    if !f.authCfg.PasswordEnabled {
        t.Error("AuthConfig.PasswordEnabled = false; want true after /setup")
    }

    /* Persisted to disk for the next restart. */
    persisted, err := auth.LoadConfig(f.authConfigPath)
    if err != nil {
        t.Fatalf("LoadConfig: %v", err)
    }
    if !persisted.PasswordEnabled {
        t.Error("persisted PasswordEnabled = false; want true")
    }
}

func TestSetup_RejectsMismatchedPassword(t *testing.T) {
    f := newSetupFixture(t)
    rec := f.postFormPw(t, f.token, "alice", f.pubKeyLine,
        "one", "two")
    if rec.Code != http.StatusBadRequest {
        t.Errorf("status = %d, want 400", rec.Code)
    }
    /* No persistence on a rejected attempt. */
    if _, err := os.Stat(filepath.Join(f.keysDir, "alice.pub")); !os.IsNotExist(err) {
        t.Errorf("alice.pub leaked: err=%v", err)
    }
    if _, err := os.Stat(f.bootstrapDir); err != nil {
        t.Errorf("bootstrap dir destroyed on mismatch: %v", err)
    }
}

func TestSetup_RejectsEmptyPassword(t *testing.T) {
    f := newSetupFixture(t)
    rec := f.postFormPw(t, f.token, "alice", f.pubKeyLine, "", "")
    if rec.Code != http.StatusBadRequest {
        t.Errorf("status = %d, want 400", rec.Code)
    }
}
