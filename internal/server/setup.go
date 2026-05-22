package server

/* SetupHandler is the /setup endpoint that consumes a bootstrap
 * token (PLAN.md Phase 11.3). The operator visits
 * https://<addr>/setup?token=<hex>, sees a form, pastes their own
 * OpenSSH pubkey and chosen username, and submits. The handler
 * validates the token, writes the pubkey to keys/<username>.pub,
 * adds the username as admin in matrix.yaml, and renames the
 * bootstrap directory to bootstrap.consumed/ so no second
 * submission can consume the same token.
 *
 * BYOK: this handler never generates a keypair. The pubkey it
 * persists is whatever bytes the operator pasted.
 */

import (
    "crypto/subtle"
    "errors"
    "fmt"
    "html/template"
    "net/http"
    "os"
    "path/filepath"
    "strings"

    gowolfssh "github.com/wolfssl/go-wolfssl/wolfssh"

    "github.com/kaleb-himes/wolfCI/internal/authz"
)

type SetupHandler struct {
    /* KeysDir is config-files/auth/keys/. The submitted pubkey
     * lands at <KeysDir>/<username>.pub on success.
     */
    KeysDir string

    /* BootstrapDir is config-files/auth/bootstrap/. The token
     * file lives at <BootstrapDir>/token; on success the dir is
     * renamed to <BootstrapDir>.consumed.
     */
    BootstrapDir string

    /* MatrixPath is config-files/auth/matrix.yaml. Created with
     * a fresh DefaultMatrix if absent, then the new admin user
     * is added.
     */
    MatrixPath string
}

var setupFormTmpl = template.Must(template.New("setup").Parse(`<!DOCTYPE html>
<html lang="en">
<head><meta charset="utf-8"><title>wolfCI first-admin setup</title></head>
<body>
<h1>wolfCI first-admin setup</h1>
<p>Paste your OpenSSH public key (the contents of e.g.
<code>~/.ssh/id_ed25519.pub</code>) and pick a username. wolfCI
will register you as the first admin and consume this one-time
setup token.</p>
<form method="POST" action="/setup">
<input type="hidden" name="token" value="{{ .Token }}">
<p><label>Username: <input type="text" name="username" required></label></p>
<p><label>OpenSSH public key:<br>
<textarea name="pubkey" rows="3" cols="80" required></textarea></label></p>
<p><button type="submit">Register first admin</button></p>
</form>
</body>
</html>
`))

func (h *SetupHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    switch r.Method {
    case http.MethodGet:
        h.serveForm(w, r)
    case http.MethodPost:
        h.handleSubmit(w, r)
    default:
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
    }
}

func (h *SetupHandler) serveForm(w http.ResponseWriter, r *http.Request) {
    /* GET also validates that there is an active token; rendering
     * a form when none exists would be misleading.
     */
    stored, err := h.loadToken()
    if err != nil {
        if errors.Is(err, errNoActiveBootstrap) {
            http.Error(w, "first-admin setup is not available; the bootstrap token has already been consumed",
                http.StatusGone)
            return
        }
        http.Error(w, "read bootstrap token: "+err.Error(),
            http.StatusInternalServerError)
        return
    }
    submitted := r.URL.Query().Get("token")
    if subtle.ConstantTimeCompare([]byte(stored), []byte(submitted)) != 1 {
        http.Error(w, "invalid bootstrap token", http.StatusForbidden)
        return
    }
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    if err := setupFormTmpl.Execute(w, struct{ Token string }{stored}); err != nil {
        /* Header already sent; best-effort log via http error. */
        http.Error(w, "render: "+err.Error(),
            http.StatusInternalServerError)
    }
}

func (h *SetupHandler) handleSubmit(w http.ResponseWriter, r *http.Request) {
    if err := r.ParseForm(); err != nil {
        http.Error(w, "parse form: "+err.Error(), http.StatusBadRequest)
        return
    }
    stored, err := h.loadToken()
    if err != nil {
        if errors.Is(err, errNoActiveBootstrap) {
            http.Error(w, "no active bootstrap token", http.StatusGone)
            return
        }
        http.Error(w, "read bootstrap token: "+err.Error(),
            http.StatusInternalServerError)
        return
    }
    submitted := r.PostFormValue("token")
    if subtle.ConstantTimeCompare([]byte(stored), []byte(submitted)) != 1 {
        http.Error(w, "invalid bootstrap token", http.StatusForbidden)
        return
    }

    username := strings.TrimSpace(r.PostFormValue("username"))
    if err := validateSetupUsername(username); err != nil {
        http.Error(w, err.Error(), http.StatusBadRequest)
        return
    }
    pubkey := strings.TrimSpace(r.PostFormValue("pubkey"))
    if pubkey == "" {
        http.Error(w, "pubkey is required", http.StatusBadRequest)
        return
    }
    /* ParseAuthorizedKey validates the algo prefix, base64
     * blob, and wire-format key fields. We discard the parsed
     * fields and persist the original line verbatim so the
     * operator's chosen comment is preserved.
     */
    if _, err := gowolfssh.ParseAuthorizedKey([]byte(pubkey)); err != nil {
        http.Error(w, "malformed OpenSSH public key: "+err.Error(),
            http.StatusBadRequest)
        return
    }

    keyPath := filepath.Join(h.KeysDir, username+".pub")
    /* Persist with a trailing newline; OpenSSH's
     * authorized_keys parser is forgiving but the convention is
     * one key per line, line-terminated.
     */
    if !strings.HasSuffix(pubkey, "\n") {
        pubkey += "\n"
    }
    if err := os.WriteFile(keyPath, []byte(pubkey), 0o644); err != nil {
        http.Error(w, "write pubkey: "+err.Error(),
            http.StatusInternalServerError)
        return
    }

    if err := h.addAdminToMatrix(username); err != nil {
        /* Undo the pubkey write so the operator can retry
         * cleanly. matrix.yaml mutation failed; we are still
         * pre-consumption.
         */
        _ = os.Remove(keyPath)
        http.Error(w, "update matrix.yaml: "+err.Error(),
            http.StatusInternalServerError)
        return
    }

    /* Consume: rename bootstrap -> bootstrap.consumed atomically.
     * After this returns, the token file no longer exists at the
     * loader's expected path, so subsequent POSTs (and GETs)
     * fall into the errNoActiveBootstrap branch.
     */
    consumed := h.BootstrapDir + ".consumed"
    _ = os.RemoveAll(consumed)
    if err := os.Rename(h.BootstrapDir, consumed); err != nil {
        http.Error(w, "consume bootstrap: "+err.Error(),
            http.StatusInternalServerError)
        return
    }

    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    fmt.Fprintf(w, `<!DOCTYPE html>
<html><body>
<h1>wolfCI first-admin setup complete</h1>
<p>User <code>%s</code> is now an admin. Sign in at <a href="/login">/login</a>.</p>
</body></html>
`, template.HTMLEscapeString(username))
}

/* errNoActiveBootstrap is returned by loadToken when the bootstrap
 * dir or its token file is absent. Used by both GET and POST to
 * distinguish "wrong token" (403) from "no token at all" (410).
 */
var errNoActiveBootstrap = errors.New("no active bootstrap token")

func (h *SetupHandler) loadToken() (string, error) {
    data, err := os.ReadFile(filepath.Join(h.BootstrapDir, "token"))
    if err != nil {
        if os.IsNotExist(err) {
            return "", errNoActiveBootstrap
        }
        return "", err
    }
    return strings.TrimSpace(string(data)), nil
}

func (h *SetupHandler) addAdminToMatrix(username string) error {
    var m *authz.Matrix
    loaded, err := authz.LoadMatrix(h.MatrixPath)
    if err != nil {
        if !os.IsNotExist(errors.Unwrap(err)) && !os.IsNotExist(err) {
            return err
        }
        m = authz.DefaultMatrix()
    } else {
        m = loaded
    }
    if m.Users == nil {
        m.Users = map[string]authz.Role{}
    }
    m.Users[username] = authz.RoleAdmin
    return m.Save(h.MatrixPath)
}

/* validateSetupUsername mirrors internal/auth.validateUsername.
 * Duplicated rather than exported to keep that package's API
 * surface unchanged; the four checks are stable.
 */
func validateSetupUsername(user string) error {
    if user == "" {
        return errors.New("username is required")
    }
    if user == "." || user == ".." {
        return fmt.Errorf("reserved username %q", user)
    }
    if strings.HasPrefix(user, ".") {
        return fmt.Errorf("username has leading dot: %q", user)
    }
    if strings.ContainsAny(user, "/\\\x00") {
        return fmt.Errorf("username contains a path separator: %q", user)
    }
    return nil
}
