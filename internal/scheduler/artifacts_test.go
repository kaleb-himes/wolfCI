package scheduler_test

/* Phase 15.3 - executor artifact copy + downstream
 * WOLFCI_INPUTS plumbing.
 *
 * LocalExecutor now:
 *   - creates builds/<job>/<n>/workspace/ before the first
 *     step and sets cmd.Dir = workspace so steps see a
 *     per-build sandbox,
 *   - after every step succeeds, copies each path listed
 *     in job.TriggersDownstream[i].Artifacts from the
 *     workspace into builds/<job>/<n>/artifacts/<basename>,
 *     BEFORE writing result.json (a missing artifact fails
 *     the build),
 *   - when a TriggeredBy ref is in the execution context,
 *     sets WOLFCI_INPUTS=<upstream artifacts dir> in every
 *     step's env so a downstream build's shell can read
 *     the upstream's deliverables.
 */

import (
    "context"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/scheduler"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestExecutor_CopiesDeclaredArtifacts(t *testing.T) {
    dir := t.TempDir()
    store, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    job := &storage.Job{
        Name: "src",
        Steps: []storage.Step{
            {Shell: "mkdir -p dist && " +
                "echo bundle-bytes > dist/bundle.tar.gz"},
        },
        TriggersDownstream: []storage.TriggerSpec{
            {Name: "dst",
                Artifacts: []string{"dist/bundle.tar.gz"}},
        },
    }
    exec := scheduler.NewLocalExecutor(store)
    result := exec.Execute(context.Background(), job, 1)
    if result.Status != scheduler.StatusSuccess {
        t.Fatalf("Status = %q (err=%q), want success",
            result.Status, result.Error)
    }
    /* The artifact is now at builds/src/1/artifacts/bundle.tar.gz. */
    artPath := filepath.Join(dir, "builds", "src", "1",
        "artifacts", "bundle.tar.gz")
    data, err := os.ReadFile(artPath)
    if err != nil {
        t.Fatalf("read artifact: %v", err)
    }
    if !strings.Contains(string(data), "bundle-bytes") {
        t.Errorf("artifact bytes = %q, want to contain "+
            "'bundle-bytes'", string(data))
    }
}

func TestExecutor_MissingArtifactFailsBuild(t *testing.T) {
    dir := t.TempDir()
    store, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    job := &storage.Job{
        Name: "missing",
        Steps: []storage.Step{
            /* Build succeeds but does NOT produce the
             * declared artifact.
             */
            {Shell: "true"},
        },
        TriggersDownstream: []storage.TriggerSpec{
            {Name: "dst",
                Artifacts: []string{"dist/never-built.zip"}},
        },
    }
    exec := scheduler.NewLocalExecutor(store)
    result := exec.Execute(context.Background(), job, 1)
    if result.Status != scheduler.StatusFailure {
        t.Errorf("Status = %q, want failure (missing "+
            "artifact must fail the build)", result.Status)
    }
    if !strings.Contains(result.Error, "never-built.zip") {
        t.Errorf("Error = %q, want it to mention the "+
            "missing artifact path", result.Error)
    }
    /* result.json was still written so the UI can show
     * the failure. The artifact dir may exist (we may
     * have partially populated it) but never-built.zip
     * is not there.
     */
    bogus := filepath.Join(dir, "builds", "missing", "1",
        "artifacts", "never-built.zip")
    if _, err := os.Stat(bogus); !os.IsNotExist(err) {
        t.Errorf("phantom artifact present: %v", err)
    }
}

func TestExecutor_DownstreamSeesUpstreamArtifacts(t *testing.T) {
    dir := t.TempDir()
    store, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    /* Plant an upstream build's artifacts dir with a
     * known file. The downstream's step verifies WOLFCI_
     * INPUTS points at it and that the file is readable.
     */
    upArt := filepath.Join(dir, "builds", "up", "5",
        "artifacts")
    if err := os.MkdirAll(upArt, 0o755); err != nil {
        t.Fatalf("mkdir upstream artifacts: %v", err)
    }
    if err := os.WriteFile(
        filepath.Join(upArt, "bundle.tar.gz"),
        []byte("hello-from-upstream"), 0o644); err != nil {
        t.Fatalf("write upstream artifact: %v", err)
    }

    downstream := &storage.Job{
        Name: "down",
        Steps: []storage.Step{
            /* Fail loudly if WOLFCI_INPUTS is missing or
             * the file is unreadable.
             */
            {Shell: `test -n "$WOLFCI_INPUTS" && ` +
                `grep -q hello-from-upstream ` +
                `"$WOLFCI_INPUTS/bundle.tar.gz"`},
        },
    }

    ctx := scheduler.WithTriggeredBy(context.Background(),
        &scheduler.BuildRef{Job: "up", Build: 5})
    exec := scheduler.NewLocalExecutor(store)
    result := exec.Execute(ctx, downstream, 1)
    if result.Status != scheduler.StatusSuccess {
        t.Fatalf("Status = %q (err=%q), want success - "+
            "downstream should see the upstream's "+
            "artifacts via WOLFCI_INPUTS", result.Status,
            result.Error)
    }
}
