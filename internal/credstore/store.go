/* internal/credstore/store.go - on-disk credential store.
 *
 * Layout:
 *
 *   <dir>/<id>.sealed    AES-256-GCM ciphertext per credential
 *                        (see seal.go for the wire format).
 *   <dir>/index.yaml     [id, type, label, created_at] rows
 *                        so callers can list creds without
 *                        unsealing every file (operators may
 *                        also peek at this file by hand to see
 *                        what credentials exist).
 *
 * The index is the source of truth for "what credentials exist";
 * a sealed file present on disk but absent from the index is
 * treated as orphaned and not returned by List. A NewStore call
 * trusts the index over the directory listing for that reason.
 *
 * id validation: alphanumeric + dash + underscore, with at least
 * one character; no leading/trailing dot; no path separators; not
 * the literal "index" (would clash with index.yaml when an
 * operator writes the sealed file under .yaml by mistake). The
 * ".sealed" suffix is reserved by the on-disk format so an id of
 * "name.sealed" is rejected too.
 */
package credstore

import (
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "regexp"
    "sort"
    "strings"

    "gopkg.in/yaml.v3"
)

/* IndexEntry is a single row in index.yaml. The fields are the
 * three pieces of metadata that callers need to display a
 * credential picker or audit-log row without unsealing the
 * record: id, type, label, and the creation timestamp.
 */
type IndexEntry struct {
    ID        string   `yaml:"id"`
    Type      CredType `yaml:"type"`
    Label     string   `yaml:"label,omitempty"`
    CreatedAt string   `yaml:"created_at"`
}

/* indexFile is the YAML shape of <dir>/index.yaml. Wrapped in a
 * struct so future fields (schema version, last-modified, etc.)
 * can be added without churning every caller.
 */
type indexFile struct {
    Entries []IndexEntry `yaml:"entries"`
}

/* Store is a credential-on-disk handle. Use NewStore to open or
 * create one. The zero value is not usable.
 */
type Store struct {
    dir          string
    masterSecret []byte
}

/* idPattern matches the safe-for-filename + safe-for-display id
 * shape: alphanumeric, dash, underscore, dot (but not leading or
 * trailing), 1-128 chars. The dot is permitted inside an id (e.g.
 * "github.com-token") but is filtered separately to keep "." and
 * ".." out.
 */
var idPattern = regexp.MustCompile(
    `^[A-Za-z0-9_][A-Za-z0-9_.-]{0,126}[A-Za-z0-9_-]$|^[A-Za-z0-9_-]$`)

/* NewStore opens (or creates) a credential store rooted at dir,
 * using masterSecret for HKDF key derivation on every Seal/Unseal.
 * The dir is created if missing; an existing index.yaml is
 * parsed; a missing index.yaml starts empty.
 */
func NewStore(dir string, masterSecret []byte) (*Store, error) {
    if dir == "" {
        return nil, errors.New(
            "credstore.NewStore: empty dir")
    }
    if len(masterSecret) == 0 {
        return nil, errors.New(
            "credstore.NewStore: empty masterSecret")
    }
    if err := os.MkdirAll(dir, 0o700); err != nil {
        return nil, fmt.Errorf(
            "credstore.NewStore: mkdir %s: %w", dir, err)
    }
    s := &Store{dir: dir, masterSecret: masterSecret}
    /* readIndex tolerates a missing file - that is the empty
     * store. Any other error (parse failure, permission denied)
     * propagates so the caller does not silently lose existing
     * credentials by overwriting a broken file.
     */
    if _, err := s.readIndex(); err != nil {
        return nil, err
    }
    return s, nil
}

/* Add seals record under id and writes it to <dir>/<id>.sealed,
 * then updates the index. id must be safe (validateID). Existing
 * entries with the same id are overwritten in both the file and
 * the index (callers that need create-only semantics can List
 * first and reject the duplicate).
 */
func (s *Store) Add(id string, record *Record) error {
    if err := validateID(id); err != nil {
        return fmt.Errorf("credstore.Add: %w", err)
    }
    if record == nil {
        return errors.New("credstore.Add: nil Record")
    }

    sealed, err := Seal(s.masterSecret, id, record)
    if err != nil {
        return fmt.Errorf("credstore.Add: seal %q: %w", id, err)
    }

    path := filepath.Join(s.dir, id+".sealed")
    if err := writeFileAtomically(path, sealed,
        0o600); err != nil {
        return fmt.Errorf("credstore.Add: write %s: %w",
            path, err)
    }

    /* Append or replace the index entry. CreatedAt round-trips
     * via RFC3339 so the YAML is human-readable and stable
     * across implementations that may not share Go's time
     * decoder.
     */
    idx, err := s.readIndex()
    if err != nil {
        return fmt.Errorf("credstore.Add: read index: %w", err)
    }
    entry := IndexEntry{
        ID:        id,
        Type:      record.Type,
        Label:     record.Label,
        CreatedAt: record.CreatedAt.UTC().Format(timeFormat),
    }
    replaced := false
    for i := range idx.Entries {
        if idx.Entries[i].ID == id {
            idx.Entries[i] = entry
            replaced = true
            break
        }
    }
    if !replaced {
        idx.Entries = append(idx.Entries, entry)
    }
    if err := s.writeIndex(idx); err != nil {
        return fmt.Errorf("credstore.Add: write index: %w", err)
    }
    return nil
}

/* Get reads <dir>/<id>.sealed and returns the unsealed record.
 * Returns an error if id is not in the index (avoids returning
 * an orphan sealed file the operator may not know about).
 */
func (s *Store) Get(id string) (*Record, error) {
    if err := validateID(id); err != nil {
        return nil, fmt.Errorf("credstore.Get: %w", err)
    }
    idx, err := s.readIndex()
    if err != nil {
        return nil, fmt.Errorf("credstore.Get: read index: %w",
            err)
    }
    if !indexHas(idx, id) {
        return nil, fmt.Errorf(
            "credstore.Get: %q not in index", id)
    }
    path := filepath.Join(s.dir, id+".sealed")
    sealed, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("credstore.Get: read %s: %w",
            path, err)
    }
    rec, err := Unseal(s.masterSecret, id, sealed)
    if err != nil {
        return nil, fmt.Errorf("credstore.Get: unseal %q: %w",
            id, err)
    }
    return rec, nil
}

/* List returns every IndexEntry currently in <dir>/index.yaml.
 * Order is whatever the file contained; callers that want a
 * deterministic order should sort the returned slice.
 */
func (s *Store) List() ([]IndexEntry, error) {
    idx, err := s.readIndex()
    if err != nil {
        return nil, fmt.Errorf("credstore.List: %w", err)
    }
    out := make([]IndexEntry, len(idx.Entries))
    copy(out, idx.Entries)
    return out, nil
}

/* Delete removes both <dir>/<id>.sealed and the index entry for
 * id. Returns an error if id is not present.
 */
func (s *Store) Delete(id string) error {
    if err := validateID(id); err != nil {
        return fmt.Errorf("credstore.Delete: %w", err)
    }
    idx, err := s.readIndex()
    if err != nil {
        return fmt.Errorf("credstore.Delete: read index: %w",
            err)
    }
    found := false
    out := idx.Entries[:0]
    for _, e := range idx.Entries {
        if e.ID == id {
            found = true
            continue
        }
        out = append(out, e)
    }
    if !found {
        return fmt.Errorf("credstore.Delete: %q not in index",
            id)
    }
    idx.Entries = out
    if err := s.writeIndex(idx); err != nil {
        return fmt.Errorf("credstore.Delete: write index: %w",
            err)
    }
    path := filepath.Join(s.dir, id+".sealed")
    if err := os.Remove(path); err != nil &&
        !os.IsNotExist(err) {
        return fmt.Errorf("credstore.Delete: remove %s: %w",
            path, err)
    }
    return nil
}

/* timeFormat is the RFC3339 nanosecond-precision form Go uses
 * for time.Time MarshalText. Keeping it as a constant means the
 * YAML representation does not drift if a future Go version
 * changes the default time encoding.
 */
const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

/* readIndex parses <dir>/index.yaml. A missing file returns an
 * empty indexFile and nil; any other error propagates.
 */
func (s *Store) readIndex() (*indexFile, error) {
    path := filepath.Join(s.dir, "index.yaml")
    data, err := os.ReadFile(path)
    if err != nil {
        if os.IsNotExist(err) {
            return &indexFile{}, nil
        }
        return nil, fmt.Errorf("read %s: %w", path, err)
    }
    idx := &indexFile{}
    if err := yaml.Unmarshal(data, idx); err != nil {
        return nil, fmt.Errorf("parse %s: %w", path, err)
    }
    return idx, nil
}

/* writeIndex serializes idx to <dir>/index.yaml atomically. */
func (s *Store) writeIndex(idx *indexFile) error {
    data, err := yaml.Marshal(idx)
    if err != nil {
        return fmt.Errorf("marshal index: %w", err)
    }
    path := filepath.Join(s.dir, "index.yaml")
    return writeFileAtomically(path, data, 0o600)
}

/* writeFileAtomically writes data to path via a temp file +
 * rename so a crash mid-write cannot leave a torn file behind.
 */
func writeFileAtomically(path string, data []byte,
    perm os.FileMode) error {

    dir := filepath.Dir(path)
    f, err := os.CreateTemp(dir, ".tmp-*")
    if err != nil {
        return err
    }
    tmpPath := f.Name()
    if _, err := f.Write(data); err != nil {
        _ = f.Close()
        _ = os.Remove(tmpPath)
        return err
    }
    if err := f.Chmod(perm); err != nil {
        _ = f.Close()
        _ = os.Remove(tmpPath)
        return err
    }
    if err := f.Close(); err != nil {
        _ = os.Remove(tmpPath)
        return err
    }
    return os.Rename(tmpPath, path)
}

/* validateID enforces the id rules documented at the top of this
 * file. Returns a descriptive error so the wolfci-ctl CLI can
 * surface it verbatim.
 */
func validateID(id string) error {
    if id == "" {
        return errors.New("id must not be empty")
    }
    if id == "index" {
        return errors.New(`id "index" is reserved for the index file`)
    }
    if strings.HasSuffix(id, ".sealed") {
        return fmt.Errorf("id %q must not end in .sealed", id)
    }
    if strings.HasPrefix(id, ".") || strings.HasSuffix(id, ".") {
        return fmt.Errorf("id %q must not start or end with a dot",
            id)
    }
    if strings.ContainsAny(id, "/\\") {
        return fmt.Errorf("id %q contains a path separator", id)
    }
    if !idPattern.MatchString(id) {
        return fmt.Errorf("id %q must match "+
            "[A-Za-z0-9_-] with optional internal dots", id)
    }
    return nil
}

/* indexHas reports whether idx contains an entry with the given
 * id.
 */
func indexHas(idx *indexFile, id string) bool {
    for _, e := range idx.Entries {
        if e.ID == id {
            return true
        }
    }
    return false
}

/* SortByID sorts entries in place by ID for callers that want a
 * deterministic List order. Exported because the wolfci-ctl
 * cred list command will use it.
 */
func SortByID(entries []IndexEntry) {
    sort.Slice(entries, func(i, j int) bool {
        return entries[i].ID < entries[j].ID
    })
}
