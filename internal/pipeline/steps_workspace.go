/* internal/pipeline/steps_workspace.go - PLAN.md 18.17.
 *
 * Workspace-management step library: cleanWs, dir, stash,
 * unstash, archiveArtifacts. These five natives operate on
 * the runtime's workspace / stashDir / artifactsDir paths
 * (inherited from LocalExecutor in exec_declarative.go).
 *
 * Scope choices that keep 18.17 small:
 *   - File copies are plain os.ReadFile / os.WriteFile;
 *     a future phase swaps the implementation for hardlinks
 *     or tarballs if perf becomes relevant.
 *   - Glob patterns are matched per-comma-separated entry
 *     against (a) the file's path relative to the workspace
 *     and (b) the basename, using filepath.Match. The "**"
 *     wildcard for "every file" is recognised as a special
 *     case because filepath.Match does not understand it.
 *   - dir() mutates the runtime's workspace AND the
 *     LocalExecutor's Workspace field for the duration of
 *     the closure body. This is not parallel-safe inside a
 *     single LocalExecutor; the master-job pipeline never
 *     nests dir() inside parallel branches, so we defer the
 *     per-branch executor story to a follow-on.
 *   - cleanWs accepts no args in 18.17 (no "deleteDirs",
 *     "patterns", etc.). Promoting those options into named
 *     args costs a handful of map lookups when needed.
 */
package pipeline

import (
    "context"
    "fmt"
    "io/fs"
    "os"
    "path/filepath"
    "strings"
)

/* registerWorkspaceSteps installs the 18.17 step library on
 * the supplied runtime. Called from registerCoreSteps so the
 * core + workspace surfaces live behind one entry point.
 */
func registerWorkspaceSteps(rt *scriptRuntime) {
    rt.globals.define("cleanWs",
        &sNative{name: "cleanWs", fn: nativeCleanWs})
    rt.globals.define("dir",
        &sNative{name: "dir", fn: nativeDir})
    rt.globals.define("stash",
        &sNative{name: "stash", fn: nativeStash})
    rt.globals.define("unstash",
        &sNative{name: "unstash", fn: nativeUnstash})
    rt.globals.define("archiveArtifacts",
        &sNative{name: "archiveArtifacts",
            fn: nativeArchiveArtifacts})
}

/* ----- cleanWs ---------------------------------------------- */

/* nativeCleanWs deletes every entry inside the workspace dir
 * while keeping the dir itself. Returns null on success.
 */
func nativeCleanWs(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if rt.workspace == "" {
        return nil, fmt.Errorf(
            "cleanWs: workspace not configured on executor")
    }
    entries, err := os.ReadDir(rt.workspace)
    if err != nil {
        return nil, fmt.Errorf("cleanWs: %w", err)
    }
    for _, e := range entries {
        if err := os.RemoveAll(
            filepath.Join(rt.workspace, e.Name())); err != nil {
            return nil, fmt.Errorf("cleanWs: %w", err)
        }
    }
    return &sNull{}, nil
}

/* ----- dir -------------------------------------------------- */

/* nativeDir runs the trailing closure with the workspace
 * temporarily changed to path (resolved relative to the
 * current workspace when not absolute). Both the runtime's
 * workspace field AND the LocalExecutor's Workspace are
 * swapped for the duration of the call so subsequent sh and
 * workspace-step invocations see the new directory.
 */
func nativeDir(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if len(args) < 2 {
        return nil, fmt.Errorf(
            "dir: expected path and closure arguments")
    }
    pathV, ok := args[0].(*sStr)
    if !ok {
        return nil, fmt.Errorf(
            "dir: first arg must be a string path (got %T)",
            args[0])
    }
    cl, ok := args[len(args)-1].(*sClosure)
    if !ok {
        return nil, fmt.Errorf(
            "dir: last arg must be a closure (got %T)",
            args[len(args)-1])
    }
    newDir := pathV.v
    if !filepath.IsAbs(newDir) {
        newDir = filepath.Join(rt.workspace, newDir)
    }
    if err := os.MkdirAll(newDir, 0o755); err != nil {
        return nil, fmt.Errorf("dir: %w", err)
    }
    savedRt := rt.workspace
    rt.workspace = newDir
    var le *LocalExecutor
    var savedExe string
    if e, ok := rt.executor.(*LocalExecutor); ok {
        le = e
        savedExe = le.Workspace
        le.Workspace = newDir
    }
    defer func() {
        rt.workspace = savedRt
        if le != nil {
            le.Workspace = savedExe
        }
    }()
    if _, err := invokeClosure(ctx, rt, cl, nil); err != nil {
        return nil, err
    }
    return &sNull{}, nil
}

/* ----- stash ------------------------------------------------ */

/* nativeStash copies matching files from the workspace into
 * <stashDir>/<name>/, preserving relative paths. Accepts the
 * map form `stash(name: '...', includes: '...', excludes:
 * '...')` or the bare string form `stash 'name'` (which
 * stashes "**" by default).
 */
func nativeStash(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if len(args) == 0 {
        return nil, fmt.Errorf("stash: no arguments")
    }
    var name, includes, excludes string
    includes = "**"
    switch a := args[0].(type) {
    case *sStr:
        name = a.v
    case *sMap:
        if s, ok := a.values["name"].(*sStr); ok {
            name = s.v
        }
        if s, ok := a.values["includes"].(*sStr); ok {
            includes = s.v
        }
        if s, ok := a.values["excludes"].(*sStr); ok {
            excludes = s.v
        }
    default:
        return nil, fmt.Errorf(
            "stash: arg must be string or map (got %T)",
            args[0])
    }
    if name == "" {
        return nil, fmt.Errorf("stash: missing 'name'")
    }
    if rt.workspace == "" {
        return nil, fmt.Errorf(
            "stash: workspace not configured on executor")
    }
    if rt.stashDir == "" {
        return nil, fmt.Errorf(
            "stash: stashDir not configured on executor")
    }
    dest := filepath.Join(rt.stashDir, name)
    if err := os.RemoveAll(dest); err != nil {
        return nil, fmt.Errorf("stash: %w", err)
    }
    if err := os.MkdirAll(dest, 0o755); err != nil {
        return nil, fmt.Errorf("stash: %w", err)
    }
    if err := copyByGlob(rt.workspace, dest, includes,
        excludes); err != nil {
        return nil, fmt.Errorf("stash: %w", err)
    }
    return &sNull{}, nil
}

/* ----- unstash ---------------------------------------------- */

/* nativeUnstash restores the named stash bundle back into the
 * workspace. Accepts a bare string `unstash 'name'` or the
 * map form `unstash(name: '...')`. */
func nativeUnstash(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if len(args) == 0 {
        return nil, fmt.Errorf("unstash: no arguments")
    }
    var name string
    switch a := args[0].(type) {
    case *sStr:
        name = a.v
    case *sMap:
        if s, ok := a.values["name"].(*sStr); ok {
            name = s.v
        }
    default:
        return nil, fmt.Errorf(
            "unstash: arg must be string or map (got %T)",
            args[0])
    }
    if name == "" {
        return nil, fmt.Errorf("unstash: missing name")
    }
    if rt.workspace == "" || rt.stashDir == "" {
        return nil, fmt.Errorf(
            "unstash: workspace/stashDir not configured")
    }
    src := filepath.Join(rt.stashDir, name)
    if info, err := os.Stat(src); err != nil || !info.IsDir() {
        return nil, fmt.Errorf(
            "unstash: stash %q not found", name)
    }
    if err := os.MkdirAll(rt.workspace, 0o755); err != nil {
        return nil, fmt.Errorf("unstash: %w", err)
    }
    if err := copyTree(src, rt.workspace); err != nil {
        return nil, fmt.Errorf("unstash: %w", err)
    }
    return &sNull{}, nil
}

/* ----- archiveArtifacts ------------------------------------- */

/* nativeArchiveArtifacts copies workspace files matching the
 * 'artifacts' glob into the runtime's ArtifactsDir. Accepts
 * the bare string form `archiveArtifacts 'pattern'` or the
 * map form `archiveArtifacts(artifacts: 'pattern', excludes:
 * '...')`. */
func nativeArchiveArtifacts(ctx context.Context,
    rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if len(args) == 0 {
        return nil, fmt.Errorf(
            "archiveArtifacts: no arguments")
    }
    var pattern, excludes string
    switch a := args[0].(type) {
    case *sStr:
        pattern = a.v
    case *sMap:
        if s, ok := a.values["artifacts"].(*sStr); ok {
            pattern = s.v
        }
        if s, ok := a.values["excludes"].(*sStr); ok {
            excludes = s.v
        }
    default:
        return nil, fmt.Errorf(
            "archiveArtifacts: arg must be string or map "+
                "(got %T)", args[0])
    }
    if pattern == "" {
        return nil, fmt.Errorf(
            "archiveArtifacts: missing 'artifacts' pattern")
    }
    if rt.workspace == "" || rt.artifactsDir == "" {
        return nil, fmt.Errorf(
            "archiveArtifacts: workspace/ArtifactsDir not "+
                "configured")
    }
    if err := os.MkdirAll(rt.artifactsDir,
        0o755); err != nil {
        return nil, fmt.Errorf("archiveArtifacts: %w", err)
    }
    if err := copyByGlob(rt.workspace, rt.artifactsDir,
        pattern, excludes); err != nil {
        return nil, fmt.Errorf("archiveArtifacts: %w", err)
    }
    return &sNull{}, nil
}

/* ----- shared helpers --------------------------------------- */

/* copyByGlob walks src recursively and copies every file
 * matching `includes` (and not matching `excludes`) into dst,
 * preserving the file's path relative to src. Returns the
 * first error encountered. */
func copyByGlob(src, dst, includes, excludes string) error {
    return filepath.WalkDir(src,
        func(path string, d fs.DirEntry, err error) error {
            if err != nil {
                return err
            }
            if d.IsDir() {
                return nil
            }
            rel, err := filepath.Rel(src, path)
            if err != nil {
                return err
            }
            if !matchAnyGlob(includes, rel) {
                return nil
            }
            if excludes != "" &&
                matchAnyGlob(excludes, rel) {
                return nil
            }
            target := filepath.Join(dst, rel)
            if err := os.MkdirAll(filepath.Dir(target),
                0o755); err != nil {
                return err
            }
            return copyOneFile(path, target)
        })
}

/* copyTree mirrors src into dst, creating directories and
 * copying every file. */
func copyTree(src, dst string) error {
    return filepath.WalkDir(src,
        func(path string, d fs.DirEntry, err error) error {
            if err != nil {
                return err
            }
            rel, err := filepath.Rel(src, path)
            if err != nil {
                return err
            }
            if rel == "." {
                return nil
            }
            target := filepath.Join(dst, rel)
            if d.IsDir() {
                return os.MkdirAll(target, 0o755)
            }
            return copyOneFile(path, target)
        })
}

/* copyOneFile reads src wholesale and writes to dst. Adequate
 * for the small artifacts and stash bundles that flow through
 * a typical Jenkins job; larger payloads would warrant
 * io.Copy with a streamed buffer. */
func copyOneFile(src, dst string) error {
    data, err := os.ReadFile(src)
    if err != nil {
        return err
    }
    return os.WriteFile(dst, data, 0o644)
}

/* matchAnyGlob splits pattern on commas and tries each
 * sub-pattern against (a) the full relative path and (b) the
 * basename. "**" is the catch-all wildcard recognised here
 * because filepath.Match does not understand it. */
func matchAnyGlob(pattern, relPath string) bool {
    if pattern == "" {
        return false
    }
    for _, p := range strings.Split(pattern, ",") {
        p = strings.TrimSpace(p)
        if p == "" {
            continue
        }
        if p == "**" {
            return true
        }
        if ok, _ := filepath.Match(p, relPath); ok {
            return true
        }
        if ok, _ := filepath.Match(p,
            filepath.Base(relPath)); ok {
            return true
        }
    }
    return false
}
