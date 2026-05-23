/* Package credstore is the wolfCI credential store backing the
 * Phase 18 Pipeline DSL withCredentials step and the wolfci-ctl
 * cred subcommands.
 *
 * On-disk layout (see PLAN.md Phase 18 decisions):
 *
 *   config-files/credentials/<id>.sealed   AES-256-GCM ciphertext
 *                                          over the JSON record.
 *   config-files/credentials/index.yaml    id -> {type, label,
 *                                          created_at} index.
 *
 * Each sealed file is exactly:
 *
 *   [12-byte nonce | ciphertext | 16-byte GCM tag]
 *
 * The 256-bit AES-GCM key is derived per credential via
 *
 *   HKDF-SHA256(IKM = server master secret,
 *               salt = nil,
 *               info = credential id) -> 32 bytes
 *
 * Binding the credential id into the HKDF info parameter means a
 * ciphertext minted under id A cannot be opened under id B even
 * when both share the master secret (the AES-GCM tag verification
 * will reject the wrong key).
 *
 * Phase 18.1 ships the Record schema, JSON Marshal/Unmarshal, and
 * the in-memory Seal/Unseal API. The on-disk store directory and
 * the wolfci-ctl CLI come later in the phase (18.3, 18.4).
 */
package credstore

import (
    "encoding/json"
    "time"
)

/* CredType is the discriminator on Record.Type. The wire values
 * match the strings the Phase 18 spec calls out ("secret-text",
 * "ssh-private-key", "username-password"); operators see these
 * verbatim in the wolfci-ctl cred subcommands and in the UI form.
 */
type CredType string

const (
    TypeSecretText       CredType = "secret-text"
    TypeSshPrivateKey    CredType = "ssh-private-key"
    TypeUsernamePassword CredType = "username-password"
)

/* Record is the inner plaintext of a sealed credential. Payload's
 * JSON shape depends on Type:
 *
 *   secret-text       -> SecretTextPayload
 *   ssh-private-key   -> SshPrivateKeyPayload
 *   username-password -> UsernamePasswordPayload
 *
 * Storing Payload as json.RawMessage keeps Record itself agnostic
 * of the typed payload structs and lets callers Marshal/Unmarshal
 * the inner payload separately, matching the Phase 18 spec's
 * two-step round-trip test (inner JSON record first, then
 * Seal/Unseal).
 */
type Record struct {
    Type      CredType        `json:"type"`
    Payload   json.RawMessage `json:"payload"`
    CreatedAt time.Time       `json:"created_at"`
    Label     string          `json:"label,omitempty"`
}

/* SecretTextPayload is the typed shape of Record.Payload when
 * Record.Type == TypeSecretText.
 */
type SecretTextPayload struct {
    Secret string `json:"secret"`
}

/* SshPrivateKeyPayload is the typed shape of Record.Payload when
 * Record.Type == TypeSshPrivateKey. PrivateKey is the PEM-encoded
 * key text; Passphrase is empty for unencrypted keys.
 */
type SshPrivateKeyPayload struct {
    PrivateKey string `json:"private_key"`
    Passphrase string `json:"passphrase,omitempty"`
}

/* UsernamePasswordPayload is the typed shape of Record.Payload
 * when Record.Type == TypeUsernamePassword.
 */
type UsernamePasswordPayload struct {
    Username string `json:"username"`
    Password string `json:"password"`
}

/* Marshal returns the canonical JSON encoding of r. The resulting
 * bytes are what Seal feeds into AES-GCM as the plaintext.
 */
func (r *Record) Marshal() ([]byte, error) {
    return json.Marshal(r)
}

/* Unmarshal parses JSON-encoded record bytes into r. Errors
 * propagate the encoding/json package's diagnostics so the
 * caller can distinguish a syntax error from a type mismatch.
 */
func (r *Record) Unmarshal(data []byte) error {
    return json.Unmarshal(data, r)
}
