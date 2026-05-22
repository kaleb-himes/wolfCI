package retention_test

/* Phase 14.1 - build retention sweeper.
 *
 * A wolfCI deployment that runs builds 24/7 fills disk fast.
 * The retention sweeper removes builds/<job>/<n>/ directories
 * that fall outside the job's Retention policy. Tests focus on
 * the policy decisions (which directories survive); the
 * goroutine that calls Sweep on a ticker is wired in
 * cmd/wolfci.
 *
 * Decisions baked into these tests:
 *   - MaxBuilds keeps the N newest, drops the rest.
 *   - MaxAge keeps anything newer than the cutoff, drops the
 *     rest.
 *   - With both set, EITHER condition protects a build. So a
 *     job with MaxBuilds=30 + MaxAge=1h keeps "the 30 newest
 *     OR anything from the last hour", whichever group is
 *     larger.
 *   - Nil Retention (or both fields zero) means "keep
 *     forever"; the sweeper skips the job.
 */

import (
    "encoding/json"
    "os"
    "path/filepath"
    "sort"
    "strconv"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/retention"
    "github.com/kaleb-himes/wolfCI/internal/scheduler"
    "github.com/kaleb-himes/wolfCI/internal/storage"
)

func TestRetention_KeepsMaxBuilds(t *testing.T) {
    dir := t.TempDir()
    store, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    if err := store.SaveJob(&storage.Job{
        Name:      "trim",
        Retention: &storage.Retention{MaxBuilds: 30},
        Steps:     []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    /* 33 builds, mtimes spaced one minute apart so #33 is
     * newest and #1 is oldest. With MaxBuilds=30 the sweep
     * keeps the 30 newest (#4..#33) and removes #1..#3.
     */
    base := time.Now().Add(-1 * time.Hour)
    for n := 1; n <= 33; n++ {
        seedBuildResult(t, dir, "trim", n,
            scheduler.StatusSuccess,
            base.Add(time.Duration(n)*time.Minute))
    }

    removed, err := retention.SweepJob(store, "trim", time.Now())
    if err != nil {
        t.Fatalf("SweepJob: %v", err)
    }
    sort.Ints(removed)
    want := []int{1, 2, 3}
    if !equalInts(removed, want) {
        t.Errorf("removed = %v, want %v", removed, want)
    }
    for n := 4; n <= 33; n++ {
        path := filepath.Join(dir, "builds", "trim",
            strconv.Itoa(n), "result.json")
        if _, err := os.Stat(path); err != nil {
            t.Errorf("build %d should have survived, stat: %v",
                n, err)
        }
    }
    for _, n := range []int{1, 2, 3} {
        path := filepath.Join(dir, "builds", "trim",
            strconv.Itoa(n))
        if _, err := os.Stat(path); !os.IsNotExist(err) {
            t.Errorf("build %d should be removed, stat: %v",
                n, err)
        }
    }
}

func TestRetention_KeepsByAge(t *testing.T) {
    dir := t.TempDir()
    store, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    if err := store.SaveJob(&storage.Job{
        Name:      "agetrim",
        Retention: &storage.Retention{MaxAge: "1h"},
        Steps:     []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    /* Five builds: #1 at 4h ago, #2 at 3h, #3 at 2h, #4 at
     * 30m ago, #5 at 5m ago. With MaxAge=1h the sweep keeps
     * {#4, #5} and removes {#1, #2, #3}.
     */
    now := time.Now()
    offsets := map[int]time.Duration{
        1: -4 * time.Hour,
        2: -3 * time.Hour,
        3: -2 * time.Hour,
        4: -30 * time.Minute,
        5: -5 * time.Minute,
    }
    for n, off := range offsets {
        seedBuildResult(t, dir, "agetrim", n,
            scheduler.StatusSuccess, now.Add(off))
    }

    removed, err := retention.SweepJob(store, "agetrim", now)
    if err != nil {
        t.Fatalf("SweepJob: %v", err)
    }
    sort.Ints(removed)
    want := []int{1, 2, 3}
    if !equalInts(removed, want) {
        t.Errorf("removed = %v, want %v", removed, want)
    }
}

func TestRetention_KeepsByEitherWhenBothSet(t *testing.T) {
    dir := t.TempDir()
    store, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    /* MaxBuilds=2 alone would keep only the two newest. But
     * combined with MaxAge=24h, anything from the last 24h
     * also survives. We seed three builds inside the last
     * 24h plus one older build that fails both rules; the
     * sweep should keep the three recent ones and remove
     * only the ancient one.
     */
    if err := store.SaveJob(&storage.Job{
        Name: "either",
        Retention: &storage.Retention{
            MaxBuilds: 2,
            MaxAge:    "24h",
        },
        Steps: []storage.Step{{Shell: "true"}},
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    now := time.Now()
    seedBuildResult(t, dir, "either", 1,
        scheduler.StatusSuccess, now.Add(-48*time.Hour))
    seedBuildResult(t, dir, "either", 2,
        scheduler.StatusSuccess, now.Add(-12*time.Hour))
    seedBuildResult(t, dir, "either", 3,
        scheduler.StatusSuccess, now.Add(-6*time.Hour))
    seedBuildResult(t, dir, "either", 4,
        scheduler.StatusSuccess, now.Add(-1*time.Hour))

    removed, err := retention.SweepJob(store, "either", now)
    if err != nil {
        t.Fatalf("SweepJob: %v", err)
    }
    sort.Ints(removed)
    /* #1 is the only ancient one. #2 fails MaxBuilds=2 (it
     * is not in the top 2) BUT passes MaxAge=24h. Either-
     * passes-protects so #2 survives.
     */
    want := []int{1}
    if !equalInts(removed, want) {
        t.Errorf("removed = %v, want %v "+
            "(MaxBuilds alone would also remove #2)",
            removed, want)
    }
}

func TestRetention_DefaultIsKeepForever(t *testing.T) {
    dir := t.TempDir()
    store, err := storage.New(dir)
    if err != nil {
        t.Fatalf("storage.New: %v", err)
    }
    if err := store.SaveJob(&storage.Job{
        Name:  "forever",
        Steps: []storage.Step{{Shell: "true"}},
        /* No Retention field -> nil. */
    }); err != nil {
        t.Fatalf("SaveJob: %v", err)
    }

    /* Seed 50 ancient builds. With no retention policy, none
     * may be removed.
     */
    base := time.Now().Add(-365 * 24 * time.Hour)
    for n := 1; n <= 50; n++ {
        seedBuildResult(t, dir, "forever", n,
            scheduler.StatusSuccess,
            base.Add(time.Duration(n)*time.Minute))
    }

    removed, err := retention.SweepJob(store, "forever",
        time.Now())
    if err != nil {
        t.Fatalf("SweepJob: %v", err)
    }
    if len(removed) != 0 {
        t.Errorf("removed = %v, want [] (no Retention block "+
            "must mean keep forever)", removed)
    }
    /* Spot-check the oldest survives. */
    path := filepath.Join(dir, "builds", "forever", "1",
        "result.json")
    if _, err := os.Stat(path); err != nil {
        t.Errorf("build 1 (year-old) should survive: %v", err)
    }
}

/* seedBuildResult writes a result.json under
 * builds/<jobName>/<num>/ and force-sets the mtime so the
 * "newest-first by mtime" tests in this package are
 * deterministic. Mirrors the seedBuild helper in the server
 * tests but stays local to the retention package to avoid a
 * test-only cross-package import.
 */
func seedBuildResult(t *testing.T, root, jobName string,
    num int, status scheduler.Status, when time.Time) {

    t.Helper()
    dir := filepath.Join(root, "builds", jobName,
        strconv.Itoa(num))
    if err := os.MkdirAll(dir, 0o755); err != nil {
        t.Fatalf("mkdir build %d: %v", num, err)
    }
    data, err := json.Marshal(scheduler.BuildResult{
        JobName: jobName,
        Number:  num,
        Status:  status,
    })
    if err != nil {
        t.Fatalf("marshal build %d: %v", num, err)
    }
    path := filepath.Join(dir, "result.json")
    if err := os.WriteFile(path, data, 0o644); err != nil {
        t.Fatalf("write build %d: %v", num, err)
    }
    if err := os.Chtimes(path, when, when); err != nil {
        t.Fatalf("chtimes build %d: %v", num, err)
    }
    /* Also set the build dir mtime so the retention scanner,
     * which falls back to the dir's mtime when result.json
     * is absent, sees the same value.
     */
    if err := os.Chtimes(dir, when, when); err != nil {
        t.Fatalf("chtimes dir %d: %v", num, err)
    }
}

func equalInts(a, b []int) bool {
    if len(a) != len(b) {
        return false
    }
    for i := range a {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}
