package pipeline_test

/* internal/pipeline/builtins_currentbuild_test.go - PLAN.md
 * 18.25 gating test.
 *
 * TestBuiltin_CurrentBuildPrev wires a tiny in-memory
 * BuildInfoProvider with two builds (current = #2,
 * previous = #1, both for job "demo") and runs a pipeline
 * whose script block exercises the surface jenkinsUtils.groovy
 * actually reaches for:
 *
 *   currentBuild.getDisplayName()              -> "#2"
 *   def prev = currentBuild.getPreviousBuild()
 *   prev.getBuildVariables().X                 -> "from-#1"
 *   prev.rawBuild.getEnvironment().GIT_COMMIT  -> "abc123"
 *
 * Each value is written to its own file in the workspace so
 * the test asserts the right outputs without depending on
 * step output ordering.
 *
 * A second test, TestBuiltin_CurrentBuildNoPrev, confirms
 * .getPreviousBuild() returns null when the in-flight build
 * has no predecessor (PreviousBuildNumber = 0); the
 * Jenkinsfile's `if (prev)` short-circuit relies on the null
 * being falsy.
 */

import (
    "context"
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/pipeline"
)

func TestBuiltin_CurrentBuildPrev(t *testing.T) {
    workspace := t.TempDir()
    provider := &mapBuildInfo{
        current: &pipeline.BuildInfo{
            JobName:             "demo",
            Number:              2,
            DisplayName:         "#2",
            Result:              "",
            PreviousBuildNumber: 1,
        },
        builds: map[int]*pipeline.BuildInfo{
            1: {
                JobName:     "demo",
                Number:      1,
                DisplayName: "#1",
                Result:      "SUCCESS",
                Variables: map[string]string{
                    "X": "from-#1",
                },
                Environment: map[string]string{
                    "GIT_COMMIT": "abc123",
                },
            },
        },
    }

    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                script {
                    def name = currentBuild.getDisplayName()
                    def cmd1 = "printf '%s' '" + name + "' > name.txt"
                    sh cmd1

                    def prev = currentBuild.getPreviousBuild()
                    def vars = prev.getBuildVariables()
                    def cmd2 = "printf '%s' '" + vars.X + "' > vars.txt"
                    sh cmd2

                    def envMap = prev.rawBuild.getEnvironment()
                    def commit = envMap.GIT_COMMIT
                    def cmd3 = "printf '%s' '" + commit + "' > commit.txt"
                    sh cmd3
                }
            }
        }
    }
}`)
    file, err := pipeline.Parse(src)
    if err != nil {
        t.Fatalf("Parse: %v", err)
    }
    exe := &pipeline.LocalExecutor{
        Workspace: workspace,
        BuildInfo: provider,
    }
    build, err := pipeline.ExecDeclarative(
        context.Background(), file, exe)
    if err != nil {
        t.Fatalf("ExecDeclarative: %v", err)
    }
    if build.Status != pipeline.BuildSuccess {
        var allOut strings.Builder
        for _, st := range build.Stages[0].Steps {
            allOut.WriteString(st.Output)
            allOut.WriteString("\n")
        }
        t.Fatalf("build.Status = %v, want BuildSuccess; "+
            "step output:\n%s", build.Status, allOut.String())
    }
    expect := []struct {
        file string
        want string
    }{
        {"name.txt", "#2"},
        {"vars.txt", "from-#1"},
        {"commit.txt", "abc123"},
    }
    for _, c := range expect {
        data, err := os.ReadFile(
            filepath.Join(workspace, c.file))
        if err != nil {
            t.Errorf("read %s: %v", c.file, err)
            continue
        }
        got := strings.TrimSpace(string(data))
        if got != c.want {
            t.Errorf("%s = %q, want %q",
                c.file, got, c.want)
        }
    }
}

func TestBuiltin_CurrentBuildNoPrev(t *testing.T) {
    workspace := t.TempDir()
    provider := &mapBuildInfo{
        current: &pipeline.BuildInfo{
            JobName:             "demo",
            Number:              1,
            DisplayName:         "#1",
            PreviousBuildNumber: 0,
        },
    }
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                script {
                    def prev = currentBuild.getPreviousBuild()
                    def tag = (prev == null ? 'none' : 'have-prev')
                    sh "echo " + tag + " > prev.txt"
                }
            }
        }
    }
}`)
    file, _ := pipeline.Parse(src)
    exe := &pipeline.LocalExecutor{
        Workspace: workspace,
        BuildInfo: provider,
    }
    build, err := pipeline.ExecDeclarative(
        context.Background(), file, exe)
    if err != nil {
        t.Fatalf("ExecDeclarative: %v", err)
    }
    if build.Status != pipeline.BuildSuccess {
        t.Fatalf("build.Status = %v, want BuildSuccess",
            build.Status)
    }
    data, err := os.ReadFile(
        filepath.Join(workspace, "prev.txt"))
    if err != nil {
        t.Fatalf("read prev.txt: %v", err)
    }
    got := strings.TrimSpace(string(data))
    if got != "none" {
        t.Errorf("prev.txt = %q, want %q (getPreviousBuild "+
            "should return null when no prior build)",
            got, "none")
    }
}

/* mapBuildInfo is a tiny in-memory BuildInfoProvider keyed on
 * build number; the gating tests use it to feed synthetic
 * histories without standing up the full storage layer. */
type mapBuildInfo struct {
    current *pipeline.BuildInfo
    builds  map[int]*pipeline.BuildInfo
}

func (m *mapBuildInfo) CurrentBuild() *pipeline.BuildInfo {
    return m.current
}

func (m *mapBuildInfo) LookupBuild(jobName string,
    num int) *pipeline.BuildInfo {
    if m.builds == nil {
        return nil
    }
    b, ok := m.builds[num]
    if !ok || b.JobName != jobName {
        return nil
    }
    return b
}
