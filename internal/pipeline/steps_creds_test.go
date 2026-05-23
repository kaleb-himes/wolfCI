package pipeline_test

/* internal/pipeline/steps_creds_test.go - PLAN.md 18.18
 * gating test.
 *
 * TestStep_WithCredentialsSecretText creates an in-memory
 * credstore.Store, adds a secret-text cred, and runs a
 * pipeline that wraps two `sh` steps in
 * `withCredentials([string(credentialsId: 'c1',
 * variable: 'TOKEN')]) { ... }`. The test asserts two
 * properties:
 *
 *   1. The shell saw the unsealed secret in its environment:
 *      `sh 'echo $TOKEN > saved.txt'` writes the raw secret
 *      to a workspace file we then read directly.
 *   2. The secret is masked in the build log: a subsequent
 *      `sh 'echo got=$TOKEN'` lands in the StepRun's Output,
 *      where the raw secret value must not appear.
 */

import (
    "context"
    "encoding/json"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/credstore"
    "github.com/kaleb-himes/wolfCI/internal/pipeline"
)

func TestStep_WithCredentialsSecretText(t *testing.T) {
    workspace := t.TempDir()
    credsDir := t.TempDir()
    /* 32-byte master secret keeps HKDF + AES-GCM happy. The
     * exact bytes are irrelevant - the store seals and the
     * very same instance unseals. */
    master := []byte("0123456789abcdef0123456789abcdef")
    store, err := credstore.NewStore(credsDir, master)
    if err != nil {
        t.Fatalf("NewStore: %v", err)
    }
    const secretValue = "super-secret-token-value-1234"
    payload, err := json.Marshal(
        credstore.SecretTextPayload{Secret: secretValue})
    if err != nil {
        t.Fatalf("marshal payload: %v", err)
    }
    rec := &credstore.Record{
        Type:      credstore.TypeSecretText,
        Payload:   payload,
        CreatedAt: time.Now(),
        Label:     "test",
    }
    if err := store.Add("c1", rec); err != nil {
        t.Fatalf("Add: %v", err)
    }

    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                withCredentials([string(credentialsId: 'c1',
                                        variable: 'TOKEN')]) {
                    sh 'echo $TOKEN > saved.txt'
                    sh 'echo got=$TOKEN'
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
        Creds:     store,
    }
    build, err := pipeline.ExecDeclarative(
        context.Background(), file, exe)
    if err != nil {
        t.Fatalf("ExecDeclarative: %v", err)
    }
    if build.Status != pipeline.BuildSuccess {
        t.Fatalf("build.Status = %v, want BuildSuccess; "+
            "build: %+v", build.Status, build)
    }

    /* (1) sh saw the secret: saved.txt holds the raw value. */
    data, err := os.ReadFile(
        filepath.Join(workspace, "saved.txt"))
    if err != nil {
        t.Fatalf("read saved.txt (env should have propagated "+
            "into the shell): %v", err)
    }
    if !strings.Contains(string(data), secretValue) {
        t.Errorf("saved.txt = %q, want it to contain the "+
            "raw secret (TOKEN env was not visible to sh)",
            string(data))
    }

    /* (2) Build log shows the masked form, not the raw
     * secret. The 'got=' prefix proves the second sh did
     * fire; the mask check proves the secret was scrubbed. */
    out := ""
    for _, st := range build.Stages[0].Steps {
        out += st.Output + "\n"
    }
    if !strings.Contains(out, "got=") {
        t.Errorf("expected 'got=' prefix in step output; "+
            "got %q", out)
    }
    if strings.Contains(out, secretValue) {
        t.Errorf("secret leaked into log output; got %q "+
            "(masking failed)", out)
    }
}

/* TestStep_WithCredentials_UnknownCred locks in the error
 * path: a missing cred id surfaces an actionable build
 * failure, not a panic. */
func TestStep_WithCredentials_UnknownCred(t *testing.T) {
    workspace := t.TempDir()
    credsDir := t.TempDir()
    store, err := credstore.NewStore(credsDir,
        []byte("0123456789abcdef0123456789abcdef"))
    if err != nil {
        t.Fatalf("NewStore: %v", err)
    }
    src := []byte(`pipeline {
    agent { label '' }
    stages {
        stage('S') {
            steps {
                withCredentials([string(credentialsId: 'missing',
                                        variable: 'X')]) {
                    sh 'true'
                }
            }
        }
    }
}`)
    file, _ := pipeline.Parse(src)
    exe := &pipeline.LocalExecutor{
        Workspace: workspace,
        Creds:     store,
    }
    build, _ := pipeline.ExecDeclarative(
        context.Background(), file, exe)
    if build == nil || build.Status != pipeline.BuildFailure {
        t.Errorf("expected BuildFailure for missing cred; "+
            "got %+v", build)
    }
}
