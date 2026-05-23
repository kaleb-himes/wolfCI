/* Package jobspec carries the job-spec shapes wolfCI loads
 * from disk plus the SCM-aware runner that 18.26 introduced.
 *
 * The 18.26 surface: a job spec can declare its pipeline
 * comes from source control instead of being inlined in the
 * job YAML, then the runner fetches the named script from a
 * remote at build start, parses it through the existing
 * internal/pipeline parser, and executes it through
 * pipeline.ExecDeclarative.
 *
 * YAML shape:
 *
 *   name: master-job
 *   pipeline:
 *     definition: from_scm
 *     scm:
 *       repo_url: git@github.com:wolfssl/wolfssl.git
 *       credentials_id: wolfssl-bot-credentials-with-private-key
 *       branch_specifier: "*\/master"
 *       script_path: Jenkins/master-job/PRB.Jenkinsfile
 *       lightweight_checkout: true
 *
 * RunFromSCM(ctx, spec, workspace, executor) is the entry
 * point: it shells out to `git` to fetch the named branch
 * (shallow, --depth 1; sparse-checkout limited to script_path
 * when lightweight_checkout is true), reads ScriptPath out of
 * the clone, hands the bytes to pipeline.Parse, and runs the
 * resulting Build through pipeline.ExecDeclarative against
 * the supplied Executor. Errors surface as plain Go errors
 * with enough context to map back to the offending field.
 *
 * Out of scope for 18.26 and tracked in the backlog: the
 * SSH-private-key credential is not actually wired into git's
 * SSH transport yet; the gating test points at a local
 * file:// URL so the cred slot exercises the loader without
 * needing real auth. The wiring lands when the master-job
 * pipeline phase actually has to talk to GitHub.
 */
package jobspec

import (
    "bytes"
    "context"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strings"

    "gopkg.in/yaml.v3"

    "github.com/kaleb-himes/wolfCI/internal/pipeline"
)

/* PipelineDefinition enumerates how a job sources its
 * pipeline. The 18.26 wire values match the Jenkinsfile-author
 * vocabulary verbatim. */
type PipelineDefinition string

const (
    /* DefinitionInline reads the pipeline source from a
     * Pipeline.Script field on the same Pipeline block.
     * Used by the existing jobs that ship the script inline.
     */
    DefinitionInline PipelineDefinition = "inline"

    /* DefinitionFromSCM fetches the pipeline script from the
     * SCM block (see SCMConfig). 18.26's gate. */
    DefinitionFromSCM PipelineDefinition = "from_scm"
)

/* JobSpec is the loadable shape of a wolfCI job spec for the
 * pipeline-from-SCM path. It is intentionally narrower than
 * the production storage.Job: only the fields RunFromSCM
 * reads. Once the production job schema grows a Pipeline
 * block, JobSpec collapses into / aliases storage.Job. */
type JobSpec struct {
    Name        string         `yaml:"name"`
    Description string         `yaml:"description,omitempty"`
    Pipeline    *PipelineBlock `yaml:"pipeline,omitempty"`
}

/* PipelineBlock is the per-job pipeline configuration. */
type PipelineBlock struct {
    /* Definition selects the pipeline source. Required for
     * 18.26; missing definition triggers a load error so the
     * operator notices the typo. */
    Definition PipelineDefinition `yaml:"definition"`

    /* Script is the inline source when Definition == inline.
     * Empty when Definition == from_scm. */
    Script string `yaml:"script,omitempty"`

    /* SCM is the source-control configuration when
     * Definition == from_scm. Nil when Definition == inline. */
    SCM *SCMConfig `yaml:"scm,omitempty"`
}

/* SCMConfig is the source-control configuration for a
 * pipeline-from-SCM job. The field names match the Jenkins
 * "Pipeline script from SCM" form so a Jenkins operator
 * migrating to wolfCI sees familiar terminology. */
type SCMConfig struct {
    /* RepoURL is the git remote URL. Required. The 18.26
     * gating test points at a local file:// URL so the
     * fetcher does not have to negotiate auth; production
     * use lands when the master-job pipeline phase actually
     * needs GitHub auth. */
    RepoURL string `yaml:"repo_url"`

    /* CredentialsID names the credstore record holding the
     * ssh-private-key (or username-password / secret-text)
     * git uses to authenticate against RepoURL. Empty when
     * the remote is anonymous (HTTPS without auth or a
     * file:// URL). */
    CredentialsID string `yaml:"credentials_id,omitempty"`

    /* BranchSpecifier identifies the branch to fetch.
     * Jenkins-style "*\/master" is normalised to "master"
     * before the underlying git fetch; richer wildcards land
     * in a follow-on (the 18.26 gate ships the single-branch
     * subset the master-job uses). */
    BranchSpecifier string `yaml:"branch_specifier"`

    /* ScriptPath is the repo-relative path to the
     * Jenkinsfile, e.g. "Jenkins/master-job/PRB.Jenkinsfile".
     * Required. */
    ScriptPath string `yaml:"script_path"`

    /* LightweightCheckout enables the sparse-checkout +
     * depth-1 path: the fetcher pulls only the script-path
     * blob (and the tree entries leading to it) instead of
     * the full repo. When false the fetcher does a depth-1
     * clone of the whole repo. */
    LightweightCheckout bool `yaml:"lightweight_checkout,omitempty"`
}

/* LoadSpec parses a JobSpec from YAML bytes. The function
 * does no file I/O - callers handle that. Errors include the
 * offending field name when the schema mismatch is
 * recoverable. */
func LoadSpec(data []byte) (*JobSpec, error) {
    spec := &JobSpec{}
    if err := yaml.Unmarshal(data, spec); err != nil {
        return nil, fmt.Errorf(
            "jobspec.LoadSpec: parse YAML: %w", err)
    }
    if spec.Name == "" {
        return nil, fmt.Errorf(
            "jobspec.LoadSpec: missing required field 'name'")
    }
    if spec.Pipeline != nil {
        if spec.Pipeline.Definition == "" {
            return nil, fmt.Errorf(
                "jobspec.LoadSpec: pipeline.definition is " +
                    "required when pipeline block is present")
        }
        switch spec.Pipeline.Definition {
        case DefinitionInline, DefinitionFromSCM:
        default:
            return nil, fmt.Errorf(
                "jobspec.LoadSpec: unknown pipeline."+
                    "definition %q (want %q or %q)",
                spec.Pipeline.Definition,
                DefinitionInline, DefinitionFromSCM)
        }
        if spec.Pipeline.Definition == DefinitionFromSCM {
            if spec.Pipeline.SCM == nil {
                return nil, fmt.Errorf(
                    "jobspec.LoadSpec: pipeline.scm is " +
                        "required when definition=from_scm")
            }
            if spec.Pipeline.SCM.RepoURL == "" {
                return nil, fmt.Errorf(
                    "jobspec.LoadSpec: pipeline.scm." +
                        "repo_url is required")
            }
            if spec.Pipeline.SCM.ScriptPath == "" {
                return nil, fmt.Errorf(
                    "jobspec.LoadSpec: pipeline.scm." +
                        "script_path is required")
            }
            if spec.Pipeline.SCM.BranchSpecifier == "" {
                return nil, fmt.Errorf(
                    "jobspec.LoadSpec: pipeline.scm." +
                        "branch_specifier is required")
            }
        }
    }
    return spec, nil
}

/* RunFromSCM fetches the named pipeline script per the spec's
 * SCM config, parses it, and runs it through the supplied
 * Executor. workspace is the build's working directory; the
 * fetcher clones into a temp subdir under workspace and reads
 * the script out of there.
 *
 * Returns the resulting pipeline.Build plus any error. The
 * error surface distinguishes:
 *   - configuration errors (nil spec, missing SCM block) -
 *     surface immediately with no Build.
 *   - fetch errors (no such branch, git not on PATH,
 *     credentials missing) - surface with the underlying git
 *     stderr in the message so an operator can diagnose.
 *   - parse errors - surface from pipeline.Parse verbatim.
 *   - execution errors - return the (possibly-failed) Build
 *     plus the error from pipeline.ExecDeclarative.
 */
func RunFromSCM(ctx context.Context, spec *JobSpec,
    workspace string,
    executor pipeline.Executor) (*pipeline.Build, error) {

    if spec == nil {
        return nil, fmt.Errorf(
            "jobspec.RunFromSCM: nil spec")
    }
    if spec.Pipeline == nil ||
        spec.Pipeline.Definition != DefinitionFromSCM {
        return nil, fmt.Errorf(
            "jobspec.RunFromSCM: spec is not from_scm " +
                "(definition mismatch)")
    }
    if spec.Pipeline.SCM == nil {
        return nil, fmt.Errorf(
            "jobspec.RunFromSCM: missing scm block")
    }
    cloneDir := filepath.Join(workspace, ".scm-checkout")
    if err := os.RemoveAll(cloneDir); err != nil {
        return nil, fmt.Errorf(
            "jobspec.RunFromSCM: clean prior checkout: %w",
            err)
    }
    branch := normaliseBranchSpecifier(
        spec.Pipeline.SCM.BranchSpecifier)
    if err := gitCheckout(ctx, spec.Pipeline.SCM,
        branch, cloneDir); err != nil {
        return nil, fmt.Errorf(
            "jobspec.RunFromSCM: git checkout: %w", err)
    }
    scriptBytes, err := os.ReadFile(
        filepath.Join(cloneDir,
            spec.Pipeline.SCM.ScriptPath))
    if err != nil {
        return nil, fmt.Errorf(
            "jobspec.RunFromSCM: read script %q: %w",
            spec.Pipeline.SCM.ScriptPath, err)
    }
    parsed, err := pipeline.Parse(scriptBytes)
    if err != nil {
        return nil, fmt.Errorf(
            "jobspec.RunFromSCM: parse %s: %w",
            spec.Pipeline.SCM.ScriptPath, err)
    }
    return pipeline.ExecDeclarative(ctx, parsed, executor)
}

/* normaliseBranchSpecifier converts Jenkins' "*\/master" form
 * into the bare branch name git needs. Anything not matching
 * the prefix passes through unchanged so a caller can still
 * pass a bare branch ("main", "release/1.0"). */
func normaliseBranchSpecifier(s string) string {
    if strings.HasPrefix(s, "*/") {
        return strings.TrimPrefix(s, "*/")
    }
    return s
}

/* gitCheckout drives the shell git binary to materialise the
 * named branch into dest. lightweight_checkout selects between
 * a sparse-checkout-of-script-path (small) and a full --depth
 * 1 clone (covers everything). Either path is depth 1 so the
 * fetcher never pulls history. */
func gitCheckout(ctx context.Context, scm *SCMConfig,
    branch, dest string) error {
    if scm.LightweightCheckout {
        return gitLightweightCheckout(ctx, scm, branch, dest)
    }
    return gitFullCheckout(ctx, scm, branch, dest)
}

/* gitFullCheckout does a depth-1 clone of the entire repo
 * onto disk. Simpler and faster for small repos; the
 * lightweight path is the right call for monorepos where a
 * pipeline script is one tiny file among thousands. */
func gitFullCheckout(ctx context.Context, scm *SCMConfig,
    branch, dest string) error {
    cmd := exec.CommandContext(ctx, "git", "clone",
        "--depth", "1",
        "--branch", branch,
        "--single-branch",
        scm.RepoURL, dest)
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        return fmt.Errorf(
            "git clone %s @ %s: %w: %s",
            scm.RepoURL, branch, err,
            strings.TrimSpace(stderr.String()))
    }
    return nil
}

/* gitLightweightCheckout combines --depth 1 --filter=blob:none
 * --sparse with a sparse-checkout limited to the script path
 * so only the blob the pipeline runner actually reads gets
 * pulled. The clone + sparse-set pair runs sequentially via
 * the same git binary; failures from either surface with the
 * full stderr included so an operator can diagnose. */
func gitLightweightCheckout(ctx context.Context,
    scm *SCMConfig, branch, dest string) error {
    cloneArgs := []string{
        "clone",
        "--depth", "1",
        "--branch", branch,
        "--single-branch",
        "--filter=blob:none",
        "--sparse",
        scm.RepoURL, dest,
    }
    cmd := exec.CommandContext(ctx, "git", cloneArgs...)
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    if err := cmd.Run(); err != nil {
        return fmt.Errorf(
            "git clone --sparse %s @ %s: %w: %s",
            scm.RepoURL, branch, err,
            strings.TrimSpace(stderr.String()))
    }
    /* Cone-mode sparse-checkout treats its arguments as
     * directory prefixes. Pass the parent directory of the
     * script path so the leaf file is included (passing the
     * file itself errors with "is not a directory"). When
     * the script is at the repo root the prefix degenerates
     * to "." which sparse-checkout reads as "include
     * everything", matching a non-lightweight clone. */
    sparseArg := filepath.Dir(scm.ScriptPath)
    if sparseArg == "" || sparseArg == "." {
        sparseArg = "/"
    }
    sparseCmd := exec.CommandContext(ctx, "git",
        "-C", dest, "sparse-checkout", "set",
        sparseArg)
    var sparseErr bytes.Buffer
    sparseCmd.Stderr = &sparseErr
    if err := sparseCmd.Run(); err != nil {
        return fmt.Errorf(
            "git sparse-checkout set %s: %w: %s",
            scm.ScriptPath, err,
            strings.TrimSpace(sparseErr.String()))
    }
    return nil
}
