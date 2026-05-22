package server

/* Bootstrap mints the first-admin setup token on first server
 * start (PLAN.md Phase 11.2). It is deliberately BYOK ("bring
 * your own key"): wolfCI never generates an SSH keypair for a
 * user. The operator visits the printed setup URL, pastes their
 * own OpenSSH public key, and is registered as the first admin
 * (Phase 11.3 implements that endpoint).
 */

import (
    "encoding/hex"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "strings"

    "github.com/kaleb-himes/wolfCI/internal/wolfcrypt"
)

/* tokenBytes is the RNG draw length. 32 bytes hex-encoded yields
 * a 64-char token: 256 bits of entropy, which is overkill for a
 * one-shot URL token but comfortably outside any feasible online
 * guess.
 */
const tokenBytes = 32

/* Bootstrap wires the directories Mint operates on. KeysDir is
 * inspected for any *.pub (an existing admin); BootstrapDir is
 * created with mode 0700 when a token is minted and receives the
 * token file at mode 0600. ListenAddr is folded into the printed
 * setup URL verbatim.
 */
type Bootstrap struct {
    KeysDir      string
    BootstrapDir string
    ListenAddr   string
}

/* MintResult is what Mint returns on the first-start path. */
type MintResult struct {
    /* Token is the 64-char lowercase hex token. The operator
     * never sees this raw value; they click the SetupURL.
     */
    Token string

    /* SetupURL is the full URL the operator clicks
     * (https://<ListenAddr>/setup?token=<hex>).
     */
    SetupURL string
}

/* Mint checks whether any admin pubkey already exists under
 * KeysDir. If yes, returns (nil, nil): the server has already
 * been bootstrapped, nothing to do. If no, generates a token,
 * writes it to <BootstrapDir>/token (mode 0600), and returns
 * the MintResult; the caller is responsible for printing
 * MintResult.SetupURL to stdout.
 */
func (b *Bootstrap) Mint() (*MintResult, error) {
    if b == nil {
        return nil, errors.New("server.Bootstrap.Mint: nil receiver")
    }
    if b.KeysDir == "" {
        return nil, errors.New("server.Bootstrap.Mint: KeysDir is required")
    }
    if b.BootstrapDir == "" {
        return nil, errors.New("server.Bootstrap.Mint: BootstrapDir is required")
    }
    if b.ListenAddr == "" {
        return nil, errors.New("server.Bootstrap.Mint: ListenAddr is required")
    }

    has, err := hasAdminKey(b.KeysDir)
    if err != nil {
        return nil, fmt.Errorf("server.Bootstrap.Mint: scan keys dir: %w", err)
    }
    if has {
        return nil, nil
    }

    raw, err := wolfcrypt.RandBytes(tokenBytes)
    if err != nil {
        return nil, fmt.Errorf("server.Bootstrap.Mint: RandBytes: %w", err)
    }
    token := hex.EncodeToString(raw)

    if err := os.MkdirAll(b.BootstrapDir, 0o700); err != nil {
        return nil, fmt.Errorf("server.Bootstrap.Mint: mkdir: %w", err)
    }
    /* MkdirAll does not narrow an existing dir's mode; if a
     * prior partial bootstrap left the dir at a wider mode,
     * fix it.
     */
    if err := os.Chmod(b.BootstrapDir, 0o700); err != nil {
        return nil, fmt.Errorf("server.Bootstrap.Mint: chmod dir: %w", err)
    }
    tokenPath := filepath.Join(b.BootstrapDir, "token")
    if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
        return nil, fmt.Errorf("server.Bootstrap.Mint: write token: %w", err)
    }

    return &MintResult{
        Token:    token,
        SetupURL: "https://" + b.ListenAddr + "/setup?token=" + token,
    }, nil
}

/* hasAdminKey returns true iff dir contains at least one *.pub
 * file. A missing directory counts as "no admins"; that is the
 * fresh-install path.
 */
func hasAdminKey(dir string) (bool, error) {
    entries, err := os.ReadDir(dir)
    if err != nil {
        if os.IsNotExist(err) {
            return false, nil
        }
        return false, err
    }
    for _, e := range entries {
        if e.IsDir() {
            continue
        }
        if strings.HasSuffix(e.Name(), ".pub") {
            return true, nil
        }
    }
    return false, nil
}
