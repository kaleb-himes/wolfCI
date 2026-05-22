package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kaleb-himes/wolfCI/internal/storage"
)

// teeLogWriter is the io.Writer used when a LocalExecutor has an
// onLog callback set: every byte written to it goes both to the
// build's on-disk log file AND to the streaming callback.
type teeLogWriter struct {
	inner    io.Writer
	onLog    func(jobName string, buildNum int, data []byte)
	jobName  string
	buildNum int
}

func (w *teeLogWriter) Write(p []byte) (int, error) {
	n, err := w.inner.Write(p)
	if n > 0 && w.onLog != nil {
		// Copy because os/exec may reuse the buffer.
		cp := make([]byte, n)
		copy(cp, p[:n])
		w.onLog(w.jobName, w.buildNum, cp)
	}
	return n, err
}

// LocalExecutor runs jobs in-process on the wolfCI server host.
// It is the only Executor implementation that ships in Phase 4;
// Phase 5 adds agent-driven executors against the same interface.
type LocalExecutor struct {
	store *storage.Storage
	onLog func(jobName string, buildNum int, data []byte)
}

// NewLocalExecutor constructs a LocalExecutor that writes logs
// and results under store.Root() / builds / <job> / <num>.
func NewLocalExecutor(store *storage.Storage) *LocalExecutor {
	return &LocalExecutor{store: store}
}

// NewLocalExecutorWithLogSink constructs a LocalExecutor that
// also fan-outs every stdout/stderr chunk to onLog as soon as
// the shell writes it. Used by the agent runtime so step
// output streams back to the wolfCI server live (Phase 5.7).
func NewLocalExecutorWithLogSink(store *storage.Storage, onLog func(jobName string, buildNum int, data []byte)) *LocalExecutor {
	return &LocalExecutor{store: store, onLog: onLog}
}

// Execute runs each Step in job sequentially via /bin/sh -c. The
// first step that exits non-zero terminates the build; later
// steps are skipped. stdout and stderr from every step are
// appended (in order) to builds/<job>/<num>/log. result.json
// records the final BuildResult.
//
// Env overlay: Step.Env is layered on top of os.Environ() with
// step keys overriding host keys.
func (e *LocalExecutor) Execute(ctx context.Context, job *storage.Job, num int) BuildResult {
	result := BuildResult{
		JobName: job.Name,
		Number:  num,
		Status:  StatusError,
	}
	if job.Name == "" {
		result.Error = "scheduler.LocalExecutor: empty Job.Name"
		return result
	}

	dir := filepath.Join(e.store.Root(), "builds", job.Name, strconv.Itoa(num))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		result.Error = fmt.Sprintf("scheduler.LocalExecutor: mkdir: %v", err)
		return result
	}
	/* Phase 15.3: each build runs in its own workspace dir
	 * so steps see a clean per-build sandbox and the
	 * Phase 14.2 workspace browser has something real to
	 * show. Created up-front; declared artifacts get copied
	 * out of here at the end of a successful run.
	 */
	workspace := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		result.Error = fmt.Sprintf("scheduler.LocalExecutor: mkdir workspace: %v", err)
		return result
	}

	logPath := filepath.Join(dir, "log")
	logFile, err := os.OpenFile(logPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		result.Error = fmt.Sprintf("scheduler.LocalExecutor: open log: %v", err)
		return result
	}
	defer logFile.Close()

	/* If this build was triggered by an upstream, expose
	 * the upstream's artifacts dir to every step's env via
	 * WOLFCI_INPUTS. Phase 15.3.
	 */
	var wolfciInputs string
	if parent := TriggeredByFrom(ctx); parent != nil {
		wolfciInputs = filepath.Join(e.store.Root(),
			"builds", parent.Job, strconv.Itoa(parent.Build),
			"artifacts")
		result.TriggeredBy = &BuildRef{
			Job: parent.Job, Build: parent.Build,
		}
	}

	result.Status = StatusSuccess

	for i, step := range job.Steps {
		if step.Shell == "" {
			continue
		}

		// Header line for human readers of the log.
		fmt.Fprintf(logFile, "+ [step %d", i+1)
		if step.Name != "" {
			fmt.Fprintf(logFile, " %s", step.Name)
		}
		fmt.Fprintf(logFile, "] %s\n", step.Shell)

		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", step.Shell)
		cmd.Dir = workspace
		var stepOut io.Writer = logFile
		if e.onLog != nil {
			stepOut = &teeLogWriter{
				inner:    logFile,
				onLog:    e.onLog,
				jobName:  job.Name,
				buildNum: num,
			}
		}
		cmd.Stdout = stepOut
		cmd.Stderr = stepOut
		envOverlay := step.Env
		if wolfciInputs != "" {
			if envOverlay == nil {
				envOverlay = map[string]string{}
			}
			/* Step.Env wins if the operator deliberately
			 * pinned WOLFCI_INPUTS for a particular step;
			 * else we set it. Practically nobody overrides
			 * it but the layering rule stays consistent
			 * with mergeEnv's "overlay wins" behavior.
			 */
			if _, ok := envOverlay["WOLFCI_INPUTS"]; !ok {
				envOverlay["WOLFCI_INPUTS"] = wolfciInputs
			}
		}
		cmd.Env = mergeEnv(os.Environ(), envOverlay)

		runErr := cmd.Run()
		if runErr != nil {
			if ctx.Err() != nil {
				result.Status = StatusCancelled
				result.Error = ctx.Err().Error()
				fmt.Fprintf(logFile, "[wolfci] cancelled: %v\n", ctx.Err())
				break
			}
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				result.Status = StatusFailure
				result.ExitCode = exitErr.ExitCode()
				fmt.Fprintf(logFile, "[wolfci] step exited with code %d\n", result.ExitCode)
			} else {
				result.Status = StatusError
				result.Error = runErr.Error()
				fmt.Fprintf(logFile, "[wolfci] step error: %v\n", runErr)
			}
			break
		}
	}

	/* Phase 15.3: on a successful run, copy every declared
	 * artifact from the workspace into builds/<job>/<n>/
	 * artifacts/<basename>. A missing artifact downgrades
	 * the build to failure with a clear error so the
	 * operator knows why the downstream did not fire.
	 */
	if result.Status == StatusSuccess &&
		len(job.TriggersDownstream) > 0 {

		artDir := filepath.Join(dir, "artifacts")
		if err := os.MkdirAll(artDir, 0o755); err != nil {
			result.Status = StatusError
			result.Error = fmt.Sprintf(
				"scheduler.LocalExecutor: mkdir artifacts: %v", err)
		} else {
			for _, ts := range job.TriggersDownstream {
				for _, rel := range ts.Artifacts {
					if err := copyArtifact(workspace,
						artDir, rel); err != nil {

						result.Status = StatusFailure
						result.Error = fmt.Sprintf(
							"scheduler.LocalExecutor: "+
								"missing artifact %q: %v",
							rel, err)
						fmt.Fprintf(logFile,
							"[wolfci] %s\n",
							result.Error)
						break
					}
				}
				if result.Status != StatusSuccess {
					break
				}
			}
		}
	}

	if err := writeResultJSON(dir, result); err != nil {
		fmt.Fprintf(logFile, "[wolfci] result.json: %v\n", err)
	}

	return result
}

/* copyArtifact copies one declared artifact from src/rel to
 * dst/<basename(rel)>. rel must NOT contain ".." or absolute
 * path segments - the spec is operator-authored and the
 * executor trusts it, but we still defend against a typo
 * that would point outside the workspace.
 */
func copyArtifact(srcRoot, dstDir, rel string) error {
	if rel == "" {
		return fmt.Errorf("empty artifact path")
	}
	if filepath.IsAbs(rel) {
		return fmt.Errorf("absolute path %q rejected", rel)
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return fmt.Errorf("path traversal %q rejected", rel)
		}
	}
	src := filepath.Join(srcRoot, filepath.FromSlash(rel))
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	dst := filepath.Join(dstDir, filepath.Base(rel))
	return os.WriteFile(dst, data, 0o644)
}

func writeResultJSON(dir string, result BuildResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"), data, 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// mergeEnv layers overlay on top of base. base entries with keys
// present in overlay are replaced; remaining overlay entries are
// appended.
func mergeEnv(base []string, overlay map[string]string) []string {
	if len(overlay) == 0 {
		return base
	}
	out := make([]string, 0, len(base)+len(overlay))
	overridden := make(map[string]bool, len(overlay))
	for _, kv := range base {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		key := kv[:eq]
		if v, ok := overlay[key]; ok {
			out = append(out, key+"="+v)
			overridden[key] = true
		} else {
			out = append(out, kv)
		}
	}
	for k, v := range overlay {
		if !overridden[k] {
			out = append(out, k+"="+v)
		}
	}
	return out
}
