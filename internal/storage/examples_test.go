package storage_test

/* Phase 15.7 - gate that validates the examples/jobs/*.yaml
 * specs. scripts/test-examples.sh invokes this via
 *
 *   go test -run TestExamples ./internal/storage/
 *
 * so a CI run picks up a typo in the examples directory the
 * same way it picks up a typo in any other tracked spec.
 */

import (
    "fmt"
    "os"
    "path/filepath"
    "reflect"
    "runtime"
    "strings"
    "testing"

    "gopkg.in/yaml.v3"

    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestExamples_AllSpecsRoundTripAndPersist(t *testing.T) {
    examplesDir := examplesJobsDir(t)
    entries, err := os.ReadDir(examplesDir)
    if err != nil {
        t.Fatalf("read examples dir: %v", err)
    }

    /* Stand up a fresh storage to exercise the cycle
     * check in SaveJob across the WHOLE example set. The
     * order matters: we save each spec in the order the
     * directory listing returns them, and SaveJob accepts
     * forward references in Upstream (advisory metadata)
     * but rejects a TriggersDownstream that closes a
     * cycle. The examples directory is curated to load in
     * any order without cycles, so we sort the files
     * alphabetically and march through.
     */
    root := t.TempDir()
    st, err := storage.New(root)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }

    var checked int
    for _, e := range entries {
        if e.IsDir() {
            continue
        }
        name := e.Name()
        if !strings.HasSuffix(name, ".yaml") &&
            !strings.HasSuffix(name, ".yml") {
            continue
        }
        path := filepath.Join(examplesDir, name)
        data, err := os.ReadFile(path)
        if err != nil {
            t.Fatalf("read %s: %v", name, err)
        }

        /* 1. Parse into storage.Job. */
        var spec storage.Job
        if err := yaml.Unmarshal(data, &spec); err != nil {
            t.Errorf("%s: yaml parse failed: %v", name, err)
            continue
        }
        if spec.Name == "" {
            t.Errorf("%s: spec missing name field", name)
            continue
        }

        /* 2. Round-trip: re-marshal + re-parse. The
         * re-parsed Job must deeply-equal the first parse
         * so a sneaky type confusion (e.g. int vs string)
         * fails the gate.
         */
        out, err := yaml.Marshal(&spec)
        if err != nil {
            t.Errorf("%s: yaml marshal failed: %v",
                name, err)
            continue
        }
        var roundtrip storage.Job
        if err := yaml.Unmarshal(out, &roundtrip); err != nil {
            t.Errorf("%s: re-parse failed: %v", name, err)
            continue
        }
        if !reflect.DeepEqual(spec, roundtrip) {
            t.Errorf("%s: round-trip diverged:\n"+
                "  original:  %+v\n"+
                "  roundtrip: %+v", name, spec, roundtrip)
        }

        /* 3. Persist via SaveJob. This runs the trigger
         * graph cycle check across every spec that has
         * already loaded plus this new one. A bad spec
         * fails here.
         */
        if err := st.SaveJob(&spec); err != nil {
            t.Errorf("%s: SaveJob refused the spec: %v",
                name, err)
            continue
        }
        checked++
    }

    if checked < 2 {
        t.Fatalf("validated %d example specs; expected at "+
            "least 2 (Phase 15.7 ships linux-bundle and "+
            "windows-test as the canonical pair)", checked)
    }
}

/* examplesJobsDir resolves the absolute path of
 * <repo>/examples/jobs/ from the test binary's runtime
 * location. Hardcoding a relative path would break when
 * the test runs from a different working directory (some
 * IDEs do this).
 */
func examplesJobsDir(t *testing.T) string {
    t.Helper()
    _, file, _, ok := runtime.Caller(0)
    if !ok {
        t.Fatalf("runtime.Caller failed")
    }
    /* file is .../internal/storage/examples_test.go; the
     * repo root is two levels up.
     */
    repoRoot := filepath.Dir(filepath.Dir(filepath.Dir(file)))
    dir := filepath.Join(repoRoot, "examples", "jobs")
    if _, err := os.Stat(dir); err != nil {
        t.Fatalf("examples dir not found at %s: %v "+
            "(make sure the test runs from the repo)",
            dir, err)
    }
    return fmt.Sprintf("%s", dir)
}
