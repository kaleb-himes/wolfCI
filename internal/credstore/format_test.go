package credstore_test

/* internal/credstore/format_test.go - PLAN.md 18.1 gating test.
 *
 * Exercises:
 *   1. Marshal/Unmarshal of the inner JSON record round-trips
 *      every supported credential type byte-for-byte (Type,
 *      Payload, CreatedAt, Label).
 *   2. Seal/Unseal with a fixed HKDF master secret round-trips
 *      each record back to the original via AES-256-GCM.
 *   3. Flipping any byte in the sealed ciphertext makes Unseal
 *      return a non-nil error - the authentication tag rejects
 *      tampering.
 */

import (
    "bytes"
    "encoding/json"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/credstore"
)

func TestRecord_MarshalUnmarshalRoundTrip(t *testing.T) {
    fixedTime := time.Date(2026, 5, 22, 9, 30, 0, 0, time.UTC)

    cases := []struct {
        name    string
        record  *credstore.Record
        payload any
    }{
        {
            name: "secret-text",
            record: &credstore.Record{
                Type:      credstore.TypeSecretText,
                CreatedAt: fixedTime,
                Label:     "GitHub API token",
            },
            payload: credstore.SecretTextPayload{
                Secret: "ghp_fakeFakeFakeFakeFakeFakeFake1234",
            },
        },
        {
            name: "ssh-private-key",
            record: &credstore.Record{
                Type:      credstore.TypeSshPrivateKey,
                CreatedAt: fixedTime,
                Label:     "deploy key for build farm",
            },
            payload: credstore.SshPrivateKeyPayload{
                PrivateKey: "-----BEGIN OPENSSH PRIVATE KEY-----\n" +
                    "fakefakefakefake\n" +
                    "-----END OPENSSH PRIVATE KEY-----\n",
                Passphrase: "passw0rd",
            },
        },
        {
            name: "username-password",
            record: &credstore.Record{
                Type:      credstore.TypeUsernamePassword,
                CreatedAt: fixedTime,
                Label:     "Nexus repo",
            },
            payload: credstore.UsernamePasswordPayload{
                Username: "build-bot",
                Password: "hunter2",
            },
        },
    }

    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            payloadBytes, err := json.Marshal(tc.payload)
            if err != nil {
                t.Fatalf("json.Marshal payload: %v", err)
            }
            tc.record.Payload = json.RawMessage(payloadBytes)

            data, err := tc.record.Marshal()
            if err != nil {
                t.Fatalf("Record.Marshal: %v", err)
            }

            var got credstore.Record
            if err := got.Unmarshal(data); err != nil {
                t.Fatalf("Record.Unmarshal: %v", err)
            }
            if got.Type != tc.record.Type {
                t.Errorf("Type = %q, want %q",
                    got.Type, tc.record.Type)
            }
            if !got.CreatedAt.Equal(tc.record.CreatedAt) {
                t.Errorf("CreatedAt = %v, want %v",
                    got.CreatedAt, tc.record.CreatedAt)
            }
            if got.Label != tc.record.Label {
                t.Errorf("Label = %q, want %q",
                    got.Label, tc.record.Label)
            }
            if !bytes.Equal(canonical(t, got.Payload),
                canonical(t, tc.record.Payload)) {
                t.Errorf("Payload mismatch:\n got %s\nwant %s",
                    got.Payload, tc.record.Payload)
            }
        })
    }
}

func TestSeal_RoundTripsEveryType(t *testing.T) {
    master := bytes.Repeat([]byte{0x42}, 32)
    fixedTime := time.Date(2026, 5, 22, 9, 30, 0, 0, time.UTC)

    cases := []struct {
        credID string
        record *credstore.Record
    }{
        {
            credID: "gh-token-1",
            record: makeRecord(t,
                credstore.TypeSecretText,
                fixedTime,
                "GitHub token",
                credstore.SecretTextPayload{Secret: "ghp_xxx"}),
        },
        {
            credID: "deploy-key-1",
            record: makeRecord(t,
                credstore.TypeSshPrivateKey,
                fixedTime,
                "deploy key",
                credstore.SshPrivateKeyPayload{
                    PrivateKey: "-----BEGIN-----\nx\n-----END-----\n",
                    Passphrase: "secret",
                }),
        },
        {
            credID: "nexus-1",
            record: makeRecord(t,
                credstore.TypeUsernamePassword,
                fixedTime,
                "Nexus",
                credstore.UsernamePasswordPayload{
                    Username: "build-bot",
                    Password: "hunter2",
                }),
        },
    }

    for _, tc := range cases {
        t.Run(tc.credID, func(t *testing.T) {
            sealed, err := credstore.Seal(master, tc.credID,
                tc.record)
            if err != nil {
                t.Fatalf("Seal: %v", err)
            }
            /* Sealed output must include at least the 12-byte
             * nonce + 16-byte tag.
             */
            if len(sealed) < 12+16 {
                t.Fatalf("sealed length %d too short for "+
                    "nonce+tag", len(sealed))
            }

            got, err := credstore.Unseal(master, tc.credID,
                sealed)
            if err != nil {
                t.Fatalf("Unseal: %v", err)
            }
            if got.Type != tc.record.Type {
                t.Errorf("Type = %q, want %q",
                    got.Type, tc.record.Type)
            }
            if !got.CreatedAt.Equal(tc.record.CreatedAt) {
                t.Errorf("CreatedAt = %v, want %v",
                    got.CreatedAt, tc.record.CreatedAt)
            }
            if got.Label != tc.record.Label {
                t.Errorf("Label = %q, want %q",
                    got.Label, tc.record.Label)
            }
            if !bytes.Equal(canonical(t, got.Payload),
                canonical(t, tc.record.Payload)) {
                t.Errorf("Payload mismatch:\n got %s\nwant %s",
                    got.Payload, tc.record.Payload)
            }
        })
    }
}

func TestSeal_FreshNoncePerCall(t *testing.T) {
    master := bytes.Repeat([]byte{0x33}, 32)
    record := makeRecord(t,
        credstore.TypeSecretText,
        time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC),
        "",
        credstore.SecretTextPayload{Secret: "same input"})

    a, err := credstore.Seal(master, "cid", record)
    if err != nil {
        t.Fatalf("Seal a: %v", err)
    }
    b, err := credstore.Seal(master, "cid", record)
    if err != nil {
        t.Fatalf("Seal b: %v", err)
    }
    /* Two seals of the same plaintext under the same key must
     * differ because the nonce is fresh per call. If they match,
     * the implementation is using a deterministic nonce - that
     * would be catastrophic for AES-GCM under any future scheme
     * that reseals a record without rotating the master key.
     */
    if bytes.Equal(a, b) {
        t.Fatalf("two Seal calls produced identical ciphertext; "+
            "nonce is not fresh per call")
    }
}

func TestUnseal_RejectsTampering(t *testing.T) {
    master := bytes.Repeat([]byte{0x55}, 32)
    record := makeRecord(t,
        credstore.TypeSecretText,
        time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC),
        "",
        credstore.SecretTextPayload{Secret: "do not leak"})

    sealed, err := credstore.Seal(master, "cid", record)
    if err != nil {
        t.Fatalf("Seal: %v", err)
    }

    /* Flip every single byte one at a time; Unseal must reject
     * each variant. This covers the nonce, the ciphertext, and
     * the appended auth tag.
     */
    for i := 0; i < len(sealed); i++ {
        bad := append([]byte(nil), sealed...)
        bad[i] ^= 0x01
        if _, err := credstore.Unseal(master, "cid",
            bad); err == nil {
            t.Errorf("byte %d flip accepted by Unseal; "+
                "authentication tag did not reject tampering",
                i)
        }
    }
}

func TestUnseal_RejectsWrongCredID(t *testing.T) {
    master := bytes.Repeat([]byte{0x66}, 32)
    record := makeRecord(t,
        credstore.TypeSecretText,
        time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC),
        "",
        credstore.SecretTextPayload{Secret: "context binding"})

    sealed, err := credstore.Seal(master, "cid-A", record)
    if err != nil {
        t.Fatalf("Seal: %v", err)
    }
    if _, err := credstore.Unseal(master, "cid-B", sealed); err == nil {
        t.Fatalf("Unseal under a different credID succeeded; " +
            "credID is not bound into the HKDF info parameter")
    }
}

func makeRecord(t *testing.T, typ credstore.CredType,
    when time.Time, label string, payload any) *credstore.Record {

    t.Helper()
    raw, err := json.Marshal(payload)
    if err != nil {
        t.Fatalf("marshal payload: %v", err)
    }
    return &credstore.Record{
        Type:      typ,
        Payload:   json.RawMessage(raw),
        CreatedAt: when,
        Label:     label,
    }
}

/* canonical re-marshals a RawMessage so the equality check is
 * insensitive to whitespace differences that json.Marshal might
 * otherwise produce between encode and decode paths.
 */
func canonical(t *testing.T, raw json.RawMessage) []byte {
    t.Helper()
    var v any
    if err := json.Unmarshal(raw, &v); err != nil {
        t.Fatalf("canonical unmarshal: %v", err)
    }
    out, err := json.Marshal(v)
    if err != nil {
        t.Fatalf("canonical marshal: %v", err)
    }
    return out
}
