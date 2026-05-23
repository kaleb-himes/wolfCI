package agente2e_test

/* tests/jenkinsfile_parse_smoke_test.go - PLAN.md 18.31
 * parse-smoke gate.
 *
 * Walks third_party/testing/Jenkins for every file named
 * *.Jenkinsfile or Jenkinsfile and runs each through
 * internal/pipeline.Parse. The test fails if any file fails
 * to parse. Files known to use plugin-provided steps that
 * wolfCI intentionally defers (docker.image, copyArtifacts,
 * etc.) still parse cleanly - the parser sees them as
 * generic function calls, and step-execution coverage is
 * tracked separately in docs/pipeline-coverage.md.
 *
 * The test reports one error per failing file (rather than
 * t.Fatal-ing on the first miss) so the operator can see the
 * full list when adding a new Jenkinsfile to the corpus
 * surfaces multiple unrelated regressions.
 */

import (
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/pipeline"
)

func TestJenkinsfile_ParseSmoke(t *testing.T) {
    root := "../third_party/testing/Jenkins"
    if _, err := os.Stat(root); err != nil {
        t.Skipf("Jenkins corpus not present at %s: %v",
            root, err)
    }

    var parsed, failed int
    err := filepath.Walk(root,
        func(path string, info os.FileInfo, err error) error {
            if err != nil {
                return err
            }
            if info.IsDir() {
                return nil
            }
            base := filepath.Base(path)
            if !strings.HasSuffix(base, ".Jenkinsfile") &&
                base != "Jenkinsfile" {
                return nil
            }
            data, readErr := os.ReadFile(path)
            if readErr != nil {
                t.Errorf("read %s: %v", path, readErr)
                failed++
                return nil
            }
            if _, parseErr := pipeline.Parse(
                data); parseErr != nil {
                t.Errorf("parse %s: %v", path, parseErr)
                failed++
                return nil
            }
            parsed++
            return nil
        })
    if err != nil {
        t.Fatalf("walk %s: %v", root, err)
    }
    t.Logf("parse-smoke: %d Jenkinsfiles parsed, %d failed",
        parsed, failed)
    if parsed == 0 {
        t.Errorf("walked %s but found no Jenkinsfiles - "+
            "corpus path may have drifted", root)
    }
}
