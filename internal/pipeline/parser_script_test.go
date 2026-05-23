package pipeline_test

/* internal/pipeline/parser_script_test.go - PLAN.md 18.13
 * gating tests.
 *
 * TestParser_ScriptSubsetParsesJenkinsUtils is the spec's
 * named gate: parse the real
 * third_party/testing/Jenkins/jenkins-functions/
 * jenkinsUtils.groovy without error. That file exercises the
 * full Groovy-subset surface the script-block interpreter
 * (18.15) needs: def function declarations (typed and
 * untyped), chained method calls, list literals (with leading-
 * comma multi-line formatting), map subscripts, instanceof,
 * ternary expressions, for-in loops, try/catch with typed
 * catch parameter, @NonCPS annotations, command-style method
 * calls (echo "..."), and `return this` as the script's tail.
 *
 * Sibling focused tests lock down individual constructs from
 * PLAN.md's "supports" list so the parser surface is gated
 * even if a future jenkinsUtils edit drops one of them.
 */

import (
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/pipeline"
)

func TestParser_ScriptSubsetParsesJenkinsUtils(t *testing.T) {
    path := filepath.Join("..", "..",
        "third_party", "testing", "Jenkins",
        "jenkins-functions", "jenkinsUtils.groovy")
    src, err := os.ReadFile(path)
    if err != nil {
        t.Fatalf("read %s: %v", path, err)
    }
    file, err := pipeline.ParseScript(src)
    if err != nil {
        t.Fatalf("ParseScript jenkinsUtils.groovy: %v", err)
    }
    if file == nil || file.Block == nil {
        t.Fatalf("ParseScript returned nil")
    }
    /* Top-level should contain the function declarations and
     * the trailing `return this`. Confirm we got non-zero
     * statements. */
    if len(file.Block.Statements) == 0 {
        t.Errorf("script has zero top-level statements")
    }
    /* Six function declarations + the trailing return.
     * The exact count is brittle if jenkinsUtils.groovy
     * changes; assert the names we expect using a function-
     * decl helper. */
    expectedFuncs := []string{
        "cleanupName",
        "getJobResultName",
        "commitHashForBuild",
        "getLastBuild",
        "checkIfPassed",
        "shouldTestRetry",
    }
    for _, want := range expectedFuncs {
        if !hasFuncDecl(file.Block, want) {
            t.Errorf("script missing func decl %q", want)
        }
    }
    if !hasReturnStmt(file.Block) {
        t.Errorf("script missing trailing return statement")
    }
}

func TestParser_Script_FuncDeclUntyped(t *testing.T) {
    src := []byte(`def add(a, b) {
    return a + b
}`)
    file, err := pipeline.ParseScript(src)
    if err != nil {
        t.Fatalf("ParseScript: %v", err)
    }
    if !hasFuncDecl(file.Block, "add") {
        t.Errorf("missing func decl 'add'")
    }
}

func TestParser_Script_FuncDeclTyped(t *testing.T) {
    src := []byte(`def transform(String s, int n) {
    return s
}`)
    file, err := pipeline.ParseScript(src)
    if err != nil {
        t.Fatalf("ParseScript: %v", err)
    }
    if !hasFuncDecl(file.Block, "transform") {
        t.Errorf("missing func decl 'transform'")
    }
}

func TestParser_Script_ListAndMapLiterals(t *testing.T) {
    src := []byte(`def xs = [1, 2, 3]
def m = [a: 1, b: 2]
def empty = [:]`)
    _, err := pipeline.ParseScript(src)
    if err != nil {
        t.Fatalf("ParseScript: %v", err)
    }
}

func TestParser_Script_Closure(t *testing.T) {
    src := []byte(`def f = { x -> x + 1 }
def g = { it * 2 }`)
    _, err := pipeline.ParseScript(src)
    if err != nil {
        t.Fatalf("ParseScript: %v", err)
    }
}

func TestParser_Script_MemberAccessAndCall(t *testing.T) {
    src := []byte(`def y = a.b.c.fn(x, k: v)`)
    _, err := pipeline.ParseScript(src)
    if err != nil {
        t.Fatalf("ParseScript: %v", err)
    }
}

func TestParser_Script_IfElse(t *testing.T) {
    src := []byte(`if (x > 0) {
    return 1
} else if (x == 0) {
    return 0
} else {
    return -1
}`)
    _, err := pipeline.ParseScript(src)
    if err != nil {
        t.Fatalf("ParseScript: %v", err)
    }
}

func TestParser_Script_ForWhile(t *testing.T) {
    src := []byte(`for (s in items) {
    echo s
}
while (n > 0) {
    n = n - 1
}`)
    _, err := pipeline.ParseScript(src)
    if err != nil {
        t.Fatalf("ParseScript: %v", err)
    }
}

func TestParser_Script_TryCatchFinally(t *testing.T) {
    src := []byte(`try {
    doIt()
} catch (Exception e) {
    echo e.getMessage()
} finally {
    cleanup()
}`)
    _, err := pipeline.ParseScript(src)
    if err != nil {
        t.Fatalf("ParseScript: %v", err)
    }
}

func TestParser_Script_ReturnThrowBreakContinue(t *testing.T) {
    src := []byte(`def loop() {
    for (s in items) {
        if (s == "skip") {
            continue
        }
        if (s == "done") {
            break
        }
        if (s == "bad") {
            throw new Exception("bad")
        }
    }
    return null
}`)
    _, err := pipeline.ParseScript(src)
    if err != nil {
        t.Fatalf("ParseScript: %v", err)
    }
}

func TestParser_Script_EachClosure(t *testing.T) {
    src := []byte(`xs.each { item ->
    echo item
}`)
    _, err := pipeline.ParseScript(src)
    if err != nil {
        t.Fatalf("ParseScript: %v", err)
    }
}

func TestParser_Script_Annotation(t *testing.T) {
    src := []byte(`@NonCPS
def f(x) {
    return x
}`)
    file, err := pipeline.ParseScript(src)
    if err != nil {
        t.Fatalf("ParseScript: %v", err)
    }
    if !hasFuncDecl(file.Block, "f") {
        t.Errorf("annotated function 'f' missing from decls")
    }
}

func TestParser_Script_Error_UnterminatedBlock(t *testing.T) {
    src := []byte(`def f() {
    return 1`)
    _, err := pipeline.ParseScript(src)
    if err == nil {
        t.Fatalf("expected error on unterminated block")
    }
    if !strings.Contains(err.Error(), "unterminated") &&
        !strings.Contains(err.Error(), "expected") {
        t.Errorf("error = %q, want 'unterminated' or "+
            "'expected'", err.Error())
    }
}

/* hasFuncDecl scans a top-level block for a FuncDecl whose
 * Name matches. */
func hasFuncDecl(block *pipeline.Block, name string) bool {
    if block == nil {
        return false
    }
    for _, st := range block.Statements {
        if fd, ok := st.(*pipeline.FuncDecl); ok {
            if fd.Name == name {
                return true
            }
        }
    }
    return false
}

/* hasReturnStmt reports whether the block contains any
 * ReturnStmt at the top level. */
func hasReturnStmt(block *pipeline.Block) bool {
    if block == nil {
        return false
    }
    for _, st := range block.Statements {
        if _, ok := st.(*pipeline.ReturnStmt); ok {
            return true
        }
    }
    return false
}
