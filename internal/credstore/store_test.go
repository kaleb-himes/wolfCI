package credstore_test

/* internal/credstore/store_test.go - PLAN.md 18.3 gating test.
 *
 * Exercises the on-disk credential store layout:
 *
 *   config-files/credentials/<id>.sealed  ciphertext per cred
 *   config-files/credentials/index.yaml   id -> {type, label,
 *                                         created_at} listing
 *
 * Add seals + writes the sealed file and updates the index.
 * List returns IndexEntry rows sorted by id. Get unseals.
 * Delete removes both the sealed file and the index entry.
 */

import (
    "encoding/json"
    "os"
    "path/filepath"
    "sort"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/credstore"
)

func TestStore_AddListGet(t *testing.T) {
    dir := t.TempDir()
    master := []byte("test-master-secret-32-bytes-long-x")

    store, err := credstore.NewStore(dir, master)
    if err != nil {
        t.Fatalf("NewStore: %v", err)
    }

    fixedTime := time.Date(2026, 5, 22, 9, 30, 0, 0, time.UTC)

    /* Add one of each credential type. */
    additions := []struct {
        id      string
        typ     credstore.CredType
        label   string
        payload any
    }{
        {
            id:      "gh-token",
            typ:     credstore.TypeSecretText,
            label:   "GitHub PAT",
            payload: credstore.SecretTextPayload{Secret: "ghp_xxx"},
        },
        {
            id:    "deploy-key",
            typ:   credstore.TypeSshPrivateKey,
            label: "deploy key",
            payload: credstore.SshPrivateKeyPayload{
                PrivateKey: "-----BEGIN-----\nx\n-----END-----\n",
                Passphrase: "secret",
            },
        },
        {
            id:    "nexus",
            typ:   credstore.TypeUsernamePassword,
            label: "Nexus repo",
            payload: credstore.UsernamePasswordPayload{
                Username: "build-bot",
                Password: "hunter2",
            },
        },
    }

    for _, a := range additions {
        raw, err := json.Marshal(a.payload)
        if err != nil {
            t.Fatalf("marshal %s: %v", a.id, err)
        }
        rec := &credstore.Record{
            Type:      a.typ,
            Payload:   raw,
            CreatedAt: fixedTime,
            Label:     a.label,
        }
        if err := store.Add(a.id, rec); err != nil {
            t.Fatalf("Add %s: %v", a.id, err)
        }
        /* Sealed file must exist on disk. */
        path := filepath.Join(dir, a.id+".sealed")
        if _, err := os.Stat(path); err != nil {
            t.Errorf("expected %s on disk: %v", path, err)
        }
    }

    /* Index file must exist. */
    if _, err := os.Stat(filepath.Join(dir,
        "index.yaml")); err != nil {
        t.Errorf("expected index.yaml on disk: %v", err)
    }

    /* List returns every id with the right Type and Label. */
    entries, err := store.List()
    if err != nil {
        t.Fatalf("List: %v", err)
    }
    if len(entries) != 3 {
        t.Fatalf("List returned %d entries, want 3", len(entries))
    }
    /* Sort by id so the assertion is order-independent. */
    sort.Slice(entries, func(i, j int) bool {
        return entries[i].ID < entries[j].ID
    })
    wantIDs := []string{"deploy-key", "gh-token", "nexus"}
    for i, want := range wantIDs {
        if entries[i].ID != want {
            t.Errorf("entries[%d].ID = %q, want %q",
                i, entries[i].ID, want)
        }
    }

    /* Get each one back and verify Type + Label survive. */
    for _, a := range additions {
        got, err := store.Get(a.id)
        if err != nil {
            t.Fatalf("Get %s: %v", a.id, err)
        }
        if got.Type != a.typ {
            t.Errorf("Get %s Type = %q, want %q",
                a.id, got.Type, a.typ)
        }
        if got.Label != a.label {
            t.Errorf("Get %s Label = %q, want %q",
                a.id, got.Label, a.label)
        }
    }

    /* Delete one and verify both the sealed file and the index
     * entry are gone.
     */
    if err := store.Delete("gh-token"); err != nil {
        t.Fatalf("Delete gh-token: %v", err)
    }
    if _, err := os.Stat(filepath.Join(dir,
        "gh-token.sealed")); !os.IsNotExist(err) {
        t.Errorf("gh-token.sealed still present after Delete: %v",
            err)
    }
    entries, err = store.List()
    if err != nil {
        t.Fatalf("List after delete: %v", err)
    }
    if len(entries) != 2 {
        t.Fatalf("List after delete = %d entries, want 2",
            len(entries))
    }
    for _, e := range entries {
        if e.ID == "gh-token" {
            t.Errorf("gh-token still in index after Delete: %+v",
                e)
        }
    }
}

func TestStore_GetMissing(t *testing.T) {
    store, err := credstore.NewStore(t.TempDir(),
        []byte("master"))
    if err != nil {
        t.Fatalf("NewStore: %v", err)
    }
    if _, err := store.Get("never-added"); err == nil {
        t.Fatalf("Get on missing id returned nil error")
    }
}

func TestStore_AddRejectsUnsafeID(t *testing.T) {
    store, err := credstore.NewStore(t.TempDir(),
        []byte("master"))
    if err != nil {
        t.Fatalf("NewStore: %v", err)
    }
    rec := &credstore.Record{
        Type:      credstore.TypeSecretText,
        Payload:   []byte(`{"secret":"x"}`),
        CreatedAt: time.Now(),
    }
    bad := []string{
        "",
        "../escape",
        "with/slash",
        "with\\backslash",
        "with space",
        ".dotleader",
        "trailing.",
        "name.sealed",
    }
    for _, id := range bad {
        if err := store.Add(id, rec); err == nil {
            t.Errorf("Add accepted unsafe id %q", id)
        }
    }
}

func TestStore_ReopensExistingIndex(t *testing.T) {
    dir := t.TempDir()
    master := []byte("master-secret")

    first, err := credstore.NewStore(dir, master)
    if err != nil {
        t.Fatalf("NewStore (first): %v", err)
    }
    rec := &credstore.Record{
        Type:      credstore.TypeSecretText,
        Payload:   []byte(`{"secret":"persist me"}`),
        CreatedAt: time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC),
        Label:     "persisted",
    }
    if err := first.Add("persist", rec); err != nil {
        t.Fatalf("Add: %v", err)
    }

    /* Reopen the store. List + Get must see the entry. */
    second, err := credstore.NewStore(dir, master)
    if err != nil {
        t.Fatalf("NewStore (second): %v", err)
    }
    entries, err := second.List()
    if err != nil {
        t.Fatalf("List: %v", err)
    }
    if len(entries) != 1 || entries[0].ID != "persist" {
        t.Fatalf("entries = %+v, want one entry id=persist",
            entries)
    }
    got, err := second.Get("persist")
    if err != nil {
        t.Fatalf("Get: %v", err)
    }
    if got.Label != "persisted" {
        t.Errorf("Label = %q, want persisted", got.Label)
    }
}
