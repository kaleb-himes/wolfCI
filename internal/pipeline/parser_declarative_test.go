package pipeline_test

/* internal/pipeline/parser_declarative_test.go - PLAN.md 18.12
 * gating tests.
 *
 * TestParser_DeclarativeAST feeds a synthetic Jenkinsfile that
 * exercises every declarative-top-level construct PLAN.md 18.12
 * names: a preamble of bare def declarations, a pipeline block
 * with agent / options / stages / post, multi-stage stages,
 * when, steps, post on a stage, parallel both as a step inside
 * steps and as a direct stage child, plus script {} and
 * withCredentials() {} steps whose bodies the parser captures
 * as raw token ranges.
 *
 * TestParser_DeclarativeMasterJob runs the real master-job
 * PRB.Jenkinsfile through Parse and asserts the high-level
 * shape (preamble with def declarations, agent label, two
 * stages named "Preflight" and "Run Tests").
 *
 * Several error-path subtests gate the parser on actionable
 * messages: missing pipeline block, unknown stage child,
 * unterminated braces, parallel without an arg AND without a
 * stage-child block.
 */

import (
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/pipeline"
)

func TestParser_DeclarativeAST(t *testing.T) {
    src := []byte(`
def tests = [:]
def utils

pipeline {
    agent { label 'linux || cloud' }
    options {
        timeout(time: 30, unit: 'MINUTES')
        retry(2)
    }
    stages {
        stage('Build') {
            steps {
                echo 'building'
                sh 'make all'
            }
        }
        stage('Parallel as step') {
            when {
                expression { return env.RUN == 'yes' }
            }
            steps {
                parallel tests
            }
        }
        stage('Parallel as stage child') {
            parallel {
                stage('Linux') {
                    steps {
                        sh 'make test-linux'
                    }
                }
                stage('Mac') {
                    steps {
                        sh 'make test-mac'
                    }
                }
            }
        }
        stage('With post') {
            steps {
                script {
                    echo 'inside script'
                }
                withCredentials([string(credentialsId: 'tok',
                                        variable: 'TOK')]) {
                    sh 'echo $TOK'
                }
            }
            post {
                always {
                    echo 'stage-post-always'
                }
                failure {
                    echo 'stage-post-failure'
                }
            }
        }
    }
    post {
        always {
            echo 'pipeline-post'
        }
    }
}
`)
    file, err := pipeline.Parse(src)
    if err != nil {
        t.Fatalf("Parse: %v", err)
    }
    if file.Pipeline == nil {
        t.Fatalf("Pipeline block missing")
    }

    /* Preamble: two def statements live before the pipeline
     * block. The declarative parser captures them as raw
     * tokens for 18.13 to interpret later; here we just
     * confirm at least one 'def' keyword survived.
     */
    foundDef := false
    for _, tok := range file.Preamble {
        if tok.Kind == pipeline.TokKeyword && tok.Value == "def" {
            foundDef = true
            break
        }
    }
    if !foundDef {
        t.Errorf("preamble missing 'def' keyword tokens")
    }

    /* Agent. */
    if file.Pipeline.Agent == nil {
        t.Fatalf("Agent block missing")
    }
    if file.Pipeline.Agent.Label != "linux || cloud" {
        t.Errorf("agent label = %q, want %q",
            file.Pipeline.Agent.Label, "linux || cloud")
    }

    /* Options: two calls. */
    if file.Pipeline.Options == nil {
        t.Fatalf("Options block missing")
    }
    if got := len(file.Pipeline.Options.Calls); got != 2 {
        t.Errorf("options.Calls len = %d, want 2", got)
    } else {
        names := []string{
            file.Pipeline.Options.Calls[0].Name,
            file.Pipeline.Options.Calls[1].Name,
        }
        if names[0] != "timeout" || names[1] != "retry" {
            t.Errorf("options call names = %v, want "+
                "[timeout retry]", names)
        }
    }

    /* Stages. */
    if file.Pipeline.Stages == nil {
        t.Fatalf("Stages block missing")
    }
    if got := len(file.Pipeline.Stages.Stages); got != 4 {
        t.Fatalf("stages len = %d, want 4", got)
    }
    stageNames := []string{
        file.Pipeline.Stages.Stages[0].Name,
        file.Pipeline.Stages.Stages[1].Name,
        file.Pipeline.Stages.Stages[2].Name,
        file.Pipeline.Stages.Stages[3].Name,
    }
    wantStageNames := []string{
        "Build",
        "Parallel as step",
        "Parallel as stage child",
        "With post",
    }
    for i, want := range wantStageNames {
        if stageNames[i] != want {
            t.Errorf("stages[%d].Name = %q, want %q",
                i, stageNames[i], want)
        }
    }

    /* Stage 0: Build. Two steps (echo, sh), naked args. */
    s0 := file.Pipeline.Stages.Stages[0]
    if s0.Steps == nil {
        t.Fatalf("stage[0].Steps missing")
    }
    if got := len(s0.Steps.Steps); got != 2 {
        t.Errorf("stage[0].Steps.Steps len = %d, want 2", got)
    } else {
        if s0.Steps.Steps[0].Name != "echo" {
            t.Errorf("stage[0].Steps.Steps[0].Name = %q, "+
                "want echo", s0.Steps.Steps[0].Name)
        }
        if s0.Steps.Steps[0].ArgsKind != pipeline.ArgsNaked {
            t.Errorf("stage[0].Steps.Steps[0].ArgsKind = %v, "+
                "want ArgsNaked", s0.Steps.Steps[0].ArgsKind)
        }
        if s0.Steps.Steps[0].HasBlock {
            t.Errorf("stage[0].Steps.Steps[0].HasBlock = true, "+
                "want false")
        }
        if s0.Steps.Steps[1].Name != "sh" {
            t.Errorf("stage[0].Steps.Steps[1].Name = %q, "+
                "want sh", s0.Steps.Steps[1].Name)
        }
    }

    /* Stage 1: parallel-as-step + when. */
    s1 := file.Pipeline.Stages.Stages[1]
    if s1.When == nil {
        t.Errorf("stage[1].When missing")
    } else if len(s1.When.ExpressionBody) == 0 {
        t.Errorf("stage[1].When.ExpressionBody empty")
    }
    if s1.Steps == nil {
        t.Fatalf("stage[1].Steps missing")
    }
    if got := len(s1.Steps.Steps); got != 1 {
        t.Errorf("stage[1].Steps.Steps len = %d, want 1", got)
    } else {
        st := s1.Steps.Steps[0]
        if st.Name != "parallel" {
            t.Errorf("stage[1] step name = %q, want parallel",
                st.Name)
        }
        if st.ArgsKind != pipeline.ArgsNaked {
            t.Errorf("stage[1] parallel-as-step ArgsKind = %v, "+
                "want ArgsNaked", st.ArgsKind)
        }
        if st.HasBlock {
            t.Errorf("stage[1] parallel-as-step HasBlock = true, "+
                "want false")
        }
        /* Naked args should include the identifier "tests". */
        gotTests := false
        for _, tok := range st.ArgTokens {
            if tok.Kind == pipeline.TokIdent &&
                tok.Value == "tests" {
                gotTests = true
            }
        }
        if !gotTests {
            t.Errorf("stage[1] parallel-as-step ArgTokens does "+
                "not contain ident 'tests'; got %+v",
                st.ArgTokens)
        }
    }
    if s1.Parallel != nil {
        t.Errorf("stage[1].Parallel must be nil for "+
            "parallel-as-step")
    }

    /* Stage 2: parallel-as-stage-child. */
    s2 := file.Pipeline.Stages.Stages[2]
    if s2.Parallel == nil {
        t.Fatalf("stage[2].Parallel missing")
    }
    if got := len(s2.Parallel.Branches); got != 2 {
        t.Fatalf("stage[2].Parallel.Branches len = %d, "+
            "want 2", got)
    }
    b0 := s2.Parallel.Branches[0]
    b1 := s2.Parallel.Branches[1]
    if b0.Name != "Linux" {
        t.Errorf("parallel branch[0] name = %q, want Linux",
            b0.Name)
    }
    if b1.Name != "Mac" {
        t.Errorf("parallel branch[1] name = %q, want Mac",
            b1.Name)
    }
    if b0.Steps == nil || len(b0.Steps.Steps) != 1 {
        t.Errorf("parallel branch[0] should have one step")
    }
    if s2.Steps != nil {
        t.Errorf("stage[2].Steps must be nil for "+
            "parallel-as-stage-child")
    }

    /* Stage 3: post on stage + script {} + withCredentials. */
    s3 := file.Pipeline.Stages.Stages[3]
    if s3.Steps == nil {
        t.Fatalf("stage[3].Steps missing")
    }
    if got := len(s3.Steps.Steps); got != 2 {
        t.Fatalf("stage[3].Steps.Steps len = %d, want 2", got)
    }
    scriptStep := s3.Steps.Steps[0]
    if scriptStep.Name != "script" {
        t.Errorf("stage[3].Steps.Steps[0].Name = %q, "+
            "want script", scriptStep.Name)
    }
    if scriptStep.ArgsKind != pipeline.ArgsNone {
        t.Errorf("script step ArgsKind = %v, want ArgsNone",
            scriptStep.ArgsKind)
    }
    if !scriptStep.HasBlock {
        t.Errorf("script step HasBlock = false, want true")
    }
    if len(scriptStep.Block) == 0 {
        t.Errorf("script step Block is empty")
    }
    wcStep := s3.Steps.Steps[1]
    if wcStep.Name != "withCredentials" {
        t.Errorf("stage[3].Steps.Steps[1].Name = %q, "+
            "want withCredentials", wcStep.Name)
    }
    if wcStep.ArgsKind != pipeline.ArgsParen {
        t.Errorf("withCredentials ArgsKind = %v, "+
            "want ArgsParen", wcStep.ArgsKind)
    }
    if !wcStep.HasBlock {
        t.Errorf("withCredentials HasBlock = false, want true")
    }
    if s3.Post == nil {
        t.Fatalf("stage[3].Post missing")
    }
    if len(s3.Post.Always) != 1 {
        t.Errorf("stage[3].Post.Always len = %d, want 1",
            len(s3.Post.Always))
    }
    if len(s3.Post.Failure) != 1 {
        t.Errorf("stage[3].Post.Failure len = %d, want 1",
            len(s3.Post.Failure))
    }

    /* Pipeline-level post. */
    if file.Pipeline.Post == nil {
        t.Fatalf("pipeline-level Post missing")
    }
    if len(file.Pipeline.Post.Always) != 1 {
        t.Errorf("pipeline.Post.Always len = %d, want 1",
            len(file.Pipeline.Post.Always))
    }
}

func TestParser_DeclarativeMasterJob(t *testing.T) {
    /* Parse the real master-job Jenkinsfile so the parser is
     * gated against an in-tree fixture that mirrors what
     * Phase 18.30 ultimately has to execute end-to-end.
     */
    path := filepath.Join("..", "..",
        "third_party", "testing", "Jenkins", "master-job",
        "PRB.Jenkinsfile")
    src, err := os.ReadFile(path)
    if err != nil {
        t.Fatalf("read %s: %v", path, err)
    }
    file, err := pipeline.Parse(src)
    if err != nil {
        t.Fatalf("Parse master-job: %v", err)
    }
    if file.Pipeline == nil {
        t.Fatalf("master-job has no pipeline block")
    }
    if file.Pipeline.Agent == nil {
        t.Fatalf("master-job agent block missing")
    }
    if !strings.Contains(file.Pipeline.Agent.Label,
        "master_linux_group") {
        t.Errorf("master-job agent label = %q, want substring "+
            "'master_linux_group'",
            file.Pipeline.Agent.Label)
    }
    /* master-job's options block is commented out, so Options
     * is expected to be nil. The check is intentionally soft
     * so that uncommenting the options block in the fixture
     * does not break this test.
     */
    if file.Pipeline.Stages == nil {
        t.Fatalf("master-job stages block missing")
    }
    if got := len(file.Pipeline.Stages.Stages); got != 2 {
        t.Fatalf("master-job stages len = %d, want 2 "+
            "(Preflight, Run Tests)", got)
    }
    if file.Pipeline.Stages.Stages[0].Name != "Preflight" {
        t.Errorf("master-job stages[0].Name = %q, want "+
            "Preflight",
            file.Pipeline.Stages.Stages[0].Name)
    }
    if file.Pipeline.Stages.Stages[1].Name != "Run Tests" {
        t.Errorf("master-job stages[1].Name = %q, want "+
            "Run Tests",
            file.Pipeline.Stages.Stages[1].Name)
    }
    /* The 'Run Tests' stage has a when block (PRB_SKIP_REASON
     * guard) and a steps block containing echo + script.
     */
    rt := file.Pipeline.Stages.Stages[1]
    if rt.When == nil {
        t.Errorf("Run Tests stage missing when block")
    }
    if rt.Steps == nil {
        t.Fatalf("Run Tests stage missing steps block")
    }
    /* echo then script. */
    if len(rt.Steps.Steps) < 2 {
        t.Fatalf("Run Tests stage has %d steps, want >= 2",
            len(rt.Steps.Steps))
    }
    foundEcho := false
    foundScript := false
    for _, st := range rt.Steps.Steps {
        if st.Name == "echo" {
            foundEcho = true
        }
        if st.Name == "script" {
            foundScript = true
        }
    }
    if !foundEcho || !foundScript {
        t.Errorf("Run Tests steps missing echo or script "+
            "(echo=%v script=%v)", foundEcho, foundScript)
    }
}

func TestParser_Errors(t *testing.T) {
    cases := []struct {
        name    string
        src     string
        wantSub string
    }{
        {
            name:    "no pipeline block",
            src:     "def x = 1",
            wantSub: "no pipeline block",
        },
        {
            name: "unterminated pipeline braces",
            src: "pipeline { agent { label 'x' } " +
                "stages { stage('A') { steps { echo 'hi' } } ",
            wantSub: "unterminated",
        },
        {
            name: "unknown stage child",
            src: "pipeline { agent { label 'x' } stages { " +
                "stage('A') { bogus { } } } }",
            wantSub: "stage child",
        },
        {
            name: "parallel-as-stage-child has non-stage entry",
            src: "pipeline { agent { label 'x' } stages { " +
                "stage('A') { parallel { sh 'oops' } } } }",
            wantSub: "parallel",
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            _, err := pipeline.Parse([]byte(tc.src))
            if err == nil {
                t.Fatalf("expected error containing %q, "+
                    "got nil", tc.wantSub)
            }
            if !strings.Contains(err.Error(), tc.wantSub) {
                t.Errorf("error = %q, want substring %q",
                    err.Error(), tc.wantSub)
            }
        })
    }
}
