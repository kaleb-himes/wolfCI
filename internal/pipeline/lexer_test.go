package pipeline_test

/* internal/pipeline/lexer_test.go - PLAN.md 18.11 gating tests.
 *
 * TestLexer_TokenizeMasterJob feeds the whole master-job
 * PRB.Jenkinsfile through Tokenize and asserts the resulting
 * stream contains at least one token of every category the
 * spec calls out: every keyword, every operator, every string
 * style, numbers, identifiers, both comment forms, and def.
 *
 * Several of those categories (triple-single, /* * / comments,
 * += operator) do not appear in the master-job verbatim;
 * sibling tests below add focused fixtures so the lexer is
 * gated on the full surface area even when the umbrella file
 * does not exercise it.
 */

import (
    "os"
    "path/filepath"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/pipeline"
)

func TestLexer_TokenizeMasterJob(t *testing.T) {
    path := filepath.Join("..", "..",
        "third_party", "testing", "Jenkins", "master-job",
        "PRB.Jenkinsfile")
    src, err := os.ReadFile(path)
    if err != nil {
        t.Fatalf("read %s: %v", path, err)
    }
    tokens, err := pipeline.Tokenize(src)
    if err != nil {
        t.Fatalf("Tokenize: %v", err)
    }
    if len(tokens) < 100 {
        t.Fatalf("got %d tokens, expected the master-job to "+
            "produce hundreds", len(tokens))
    }
    /* Last token must be EOF. */
    if tokens[len(tokens)-1].Kind != pipeline.TokEOF {
        t.Errorf("final token kind = %v, want TokEOF",
            tokens[len(tokens)-1].Kind)
    }

    /* Categories the master-job file actually contains. The
     * remaining categories (/* * /, triple-single, +=) live in
     * dedicated sub-tests below.
     */
    wantKeywords := []string{
        "pipeline", "agent", "stages", "stage", "steps",
        "script", "parallel", "when", "expression",
        "withCredentials", "def",
    }
    for _, kw := range wantKeywords {
        if !containsKeyword(tokens, kw) {
            t.Errorf("master-job tokens missing keyword %q",
                kw)
        }
    }

    /* The master-job uses these operators outside string
     * literals: =, +, ., :, !, ==, !=, <, ++, ->. The PLAN.md
     * 18.11 list also names && and ||, but both appear in
     * master-job only INSIDE a single-quoted label string
     * ('master_linux_group || linux-cloud-node') where they
     * are string content rather than operator tokens. The
     * sibling TestLexer_Operators_AllListed test exercises &&
     * and || via a focused fixture so the full PLAN.md op
     * list is gated.
     */
    wantOps := []string{
        "=", "+", ".", ":", "!", "==", "!=", "<", "++", "->",
    }
    for _, op := range wantOps {
        if !containsOperator(tokens, op) {
            t.Errorf("master-job tokens missing operator %q",
                op)
        }
    }

    /* At least one of each string flavor the master-job uses. */
    if !containsStringKind(tokens, pipeline.StringSingle) {
        t.Errorf("missing single-quoted string token")
    }
    if !containsStringKind(tokens, pipeline.StringDouble) {
        t.Errorf("missing double-quoted string token")
    }
    if !containsStringKind(tokens, pipeline.StringTripleDouble) {
        t.Errorf("missing triple-double string token")
    }

    /* At least one number and one identifier (very low bar; if
     * either is missing the lexer is broken).
     */
    if !containsKind(tokens, pipeline.TokNumber) {
        t.Errorf("no number tokens in master-job")
    }
    if !containsKind(tokens, pipeline.TokIdent) {
        t.Errorf("no identifier tokens in master-job")
    }

    /* The master-job uses // comments. The lexer drops comment
     * bytes from the token stream entirely (comments do not
     * participate in the grammar); confirm the file still
     * tokenizes cleanly given the comment density.
     */
    if !strings.Contains(string(src), "//") {
        t.Skipf("master-job no longer contains // comments")
    }
}

func TestLexer_Keywords_OptionsPostAlwaysSuccessFailure(t *testing.T) {
    /* These keywords are not in the master-job but the PLAN
     * 18.11 spec calls them out. Cover them in a focused
     * fixture so the lexer surface is locked.
     */
    src := []byte("options { } post { always { } " +
        "success { } failure { } } label 'x'")
    tokens, err := pipeline.Tokenize(src)
    if err != nil {
        t.Fatalf("Tokenize: %v", err)
    }
    for _, kw := range []string{
        "options", "post", "always", "success", "failure",
        "label",
    } {
        if !containsKeyword(tokens, kw) {
            t.Errorf("missing keyword %q", kw)
        }
    }
}

func TestLexer_Operators_AllListed(t *testing.T) {
    src := []byte("a = 1 + 2 . b : c ? d && e || !f += 3")
    tokens, err := pipeline.Tokenize(src)
    if err != nil {
        t.Fatalf("Tokenize: %v", err)
    }
    for _, op := range []string{
        "=", "+", ".", ":", "?", "&&", "||", "!", "+=",
    } {
        if !containsOperator(tokens, op) {
            t.Errorf("missing operator %q", op)
        }
    }
}

func TestLexer_StringFlavors(t *testing.T) {
    src := []byte(
        "a = 'single' \n" +
            "b = \"double with $var and ${expr}\" \n" +
            "c = '''triple single literal''' \n" +
            "d = \"\"\"triple double with $var\"\"\" \n")
    tokens, err := pipeline.Tokenize(src)
    if err != nil {
        t.Fatalf("Tokenize: %v", err)
    }
    have := map[pipeline.StringKind]string{}
    for _, tok := range tokens {
        if tok.Kind == pipeline.TokString {
            have[tok.StringKind] = tok.Value
        }
    }
    if have[pipeline.StringSingle] != "single" {
        t.Errorf("single = %q", have[pipeline.StringSingle])
    }
    if have[pipeline.StringDouble] !=
        "double with $var and ${expr}" {
        t.Errorf("double = %q", have[pipeline.StringDouble])
    }
    if have[pipeline.StringTripleSingle] !=
        "triple single literal" {
        t.Errorf("triple single = %q",
            have[pipeline.StringTripleSingle])
    }
    if have[pipeline.StringTripleDouble] !=
        "triple double with $var" {
        t.Errorf("triple double = %q",
            have[pipeline.StringTripleDouble])
    }
}

func TestLexer_Comments(t *testing.T) {
    src := []byte("a = 1 // line comment\n" +
        "b = 2 /* block\n" +
        "       comment */ c = 3\n")
    tokens, err := pipeline.Tokenize(src)
    if err != nil {
        t.Fatalf("Tokenize: %v", err)
    }
    /* Lexer drops comment bytes; the token stream must contain
     * a, b, and c as identifiers and the assignments.
     */
    idents := 0
    for _, tok := range tokens {
        if tok.Kind == pipeline.TokIdent {
            idents++
        }
    }
    if idents < 3 {
        t.Errorf("expected at least 3 identifier tokens "+
            "(a, b, c), got %d", idents)
    }
    /* No token's Value should contain the comment text. */
    for _, tok := range tokens {
        if strings.Contains(tok.Value, "block") ||
            strings.Contains(tok.Value, "line comment") {
            t.Errorf("comment text leaked into token %+v", tok)
        }
    }
}

func TestLexer_NumberAndDef(t *testing.T) {
    src := []byte("def x = 42")
    tokens, err := pipeline.Tokenize(src)
    if err != nil {
        t.Fatalf("Tokenize: %v", err)
    }
    if !containsKeyword(tokens, "def") {
        t.Errorf("def keyword missing")
    }
    found := false
    for _, tok := range tokens {
        if tok.Kind == pipeline.TokNumber && tok.Value == "42" {
            found = true
        }
    }
    if !found {
        t.Errorf("number 42 missing from token stream")
    }
}

func TestLexer_PositionTracking(t *testing.T) {
    src := []byte("a\nb")
    tokens, err := pipeline.Tokenize(src)
    if err != nil {
        t.Fatalf("Tokenize: %v", err)
    }
    if tokens[0].Line != 1 || tokens[0].Col != 1 {
        t.Errorf("a token at line %d col %d, want 1,1",
            tokens[0].Line, tokens[0].Col)
    }
    /* Find the b identifier and confirm it is on line 2. */
    var bTok pipeline.Token
    for _, tok := range tokens {
        if tok.Kind == pipeline.TokIdent && tok.Value == "b" {
            bTok = tok
            break
        }
    }
    if bTok.Line != 2 || bTok.Col != 1 {
        t.Errorf("b token at line %d col %d, want 2,1",
            bTok.Line, bTok.Col)
    }
}

/* containsKeyword reports whether any token in stream is a
 * keyword with the given text.
 */
func containsKeyword(stream []pipeline.Token,
    name string) bool {
    for _, t := range stream {
        if t.Kind == pipeline.TokKeyword && t.Value == name {
            return true
        }
    }
    return false
}

func containsOperator(stream []pipeline.Token,
    op string) bool {
    for _, t := range stream {
        if t.Kind == pipeline.TokOperator && t.Value == op {
            return true
        }
    }
    return false
}

func containsKind(stream []pipeline.Token,
    k pipeline.TokenKind) bool {
    for _, t := range stream {
        if t.Kind == k {
            return true
        }
    }
    return false
}

func containsStringKind(stream []pipeline.Token,
    sk pipeline.StringKind) bool {
    for _, t := range stream {
        if t.Kind == pipeline.TokString && t.StringKind == sk {
            return true
        }
    }
    return false
}
