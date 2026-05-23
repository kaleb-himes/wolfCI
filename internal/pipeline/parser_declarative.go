/* internal/pipeline/parser_declarative.go - PLAN.md 18.12.
 *
 * Parses the declarative top level of a Jenkinsfile-shaped
 * Groovy source into an AST that the Phase 18.14+ interpreter
 * consumes. Deeper constructs (Groovy expressions inside step
 * args, the body of script {} closures, the body of
 * expression {} guards inside when {}) are NOT parsed here.
 * They are captured as raw []Token slices so the script-subset
 * parser added in 18.13 can deal with them.
 *
 * Supported shape (18.12 scope):
 *
 *   <preamble: bare def declarations, etc>
 *   pipeline {
 *       agent { label '<str>' }   // or 'agent any', 'agent none'
 *       options {
 *           timeout(time: 30, unit: 'MINUTES')
 *           retry(2)
 *       }
 *       stages {
 *           stage('Name') {
 *               when { expression { ... } }   // optional
 *               steps { <stepCall>* }         // optional
 *               parallel { stage(...) { ... } ... }   // optional
 *               post { <handler> { <stepCall>* } ... } // optional
 *           }
 *           ...
 *       }
 *       post { ... }   // optional, pipeline-level
 *   }
 *
 * Not in 18.12 (per the task description):
 *   - Nested stages (a stage that itself contains stages {}).
 *   - Agent forms other than label/any/none (docker, kubernetes,
 *     dockerfile - deferred).
 *   - When conditions other than expression {} (branch, anyOf,
 *     etc. - deferred).
 *
 * Step parsing is shallow on purpose: a step is recorded as
 * (name, ArgsKind, ArgTokens, HasBlock, Block) where the raw
 * token slices preserve enough information for the 18.13 script
 * parser to reparse them when execution needs the semantics.
 */
package pipeline

import "fmt"

/* Position identifies an AST node's start point in the source.
 * Both fields are 1-indexed (lexer convention).
 */
type Position struct {
    Line int
    Col  int
}

/* ArgsKind tags the surface form of a step call's arguments.
 *
 *   ArgsNone:  the step has no args at all (e.g. `script { }`
 *              or `cleanWs`).
 *   ArgsParen: parenthesised args, possibly with a trailing
 *              closure block (e.g. `sh(returnStatus: true,
 *              script: '...')` or `withCredentials([...]) { }`).
 *   ArgsNaked: space-separated args without parens
 *              (e.g. `echo 'hi'`, `parallel tests`).
 */
type ArgsKind int

const (
    ArgsNone ArgsKind = iota
    ArgsParen
    ArgsNaked
)

/* PipelineFile is the top-level AST returned by Parse. It
 * carries any tokens before the pipeline block (Preamble) and
 * the pipeline block itself.
 */
type PipelineFile struct {
    Preamble []Token
    Pipeline *PipelineBlock
}

/* PipelineBlock is the body of `pipeline { ... }`. Any of its
 * sections may be nil if absent.
 */
type PipelineBlock struct {
    Agent   *AgentBlock
    Options *OptionsBlock
    Stages  *StagesBlock
    Post    *PostBlock
    Pos     Position
}

/* AgentBlock represents `agent any`, `agent none`, or
 * `agent { label '<str>' }`. Exactly one of Any/None/(Label
 * set) is true after a successful parse.
 */
type AgentBlock struct {
    Any   bool
    None  bool
    Label string
    Pos   Position
}

/* OptionsBlock is the body of `options { ... }`. Each option
 * is a method call; 18.12 captures the call name and the raw
 * args tokens so the interpreter can decode the args later.
 */
type OptionsBlock struct {
    Calls []Call
    Pos   Position
}

/* Call is a generic name-plus-raw-arg-tokens record used by
 * OptionsBlock (and reusable for any context where we want to
 * defer arg parsing to 18.13).
 */
type Call struct {
    Name      string
    ArgTokens []Token
    Pos       Position
}

/* StagesBlock is the ordered list of stages inside
 * `stages { ... }`.
 */
type StagesBlock struct {
    Stages []*Stage
    Pos    Position
}

/* Stage represents one `stage('Name') { ... }` block. A stage
 * may have at most one of Steps/Parallel set (the two forms
 * are mutually exclusive at parse time in real Jenkins; this
 * parser does not enforce that, but it does NOT merge them).
 */
type Stage struct {
    Name     string
    When     *WhenBlock
    Steps    *StepsBlock
    Parallel *ParallelBlock
    Post     *PostBlock
    Pos      Position
}

/* WhenBlock represents `when { expression { ... } }`. The body
 * of the expression is captured raw for the script-subset
 * parser to evaluate at runtime.
 */
type WhenBlock struct {
    ExpressionBody []Token
    Pos            Position
}

/* StepsBlock is the body of `steps { ... }` - an ordered list
 * of step calls.
 */
type StepsBlock struct {
    Steps []StepCall
    Pos   Position
}

/* StepCall is one step invocation inside a steps block or a
 * post handler. The interpreter dispatches on Name; the raw
 * ArgTokens and Block are reparsed by the script-subset parser
 * when the step's semantics need them.
 */
type StepCall struct {
    Name      string
    ArgsKind  ArgsKind
    ArgTokens []Token
    HasBlock  bool
    Block     []Token
    Pos       Position
}

/* ParallelBlock represents a stage-direct
 * `parallel { stage(...) { ... } ... }` container. Each branch
 * is a Stage; nested parallels (parallel inside a parallel
 * branch's body) are allowed because parseStage recurses.
 */
type ParallelBlock struct {
    Branches []*Stage
    Pos      Position
}

/* PostBlock represents `post { ... }`. Each handler keyword
 * (always/success/failure/...) maps to a slice of step calls
 * to run when the condition matches.
 */
type PostBlock struct {
    Always     []StepCall
    Success    []StepCall
    Failure    []StepCall
    Unstable   []StepCall
    Aborted    []StepCall
    Changed    []StepCall
    Fixed      []StepCall
    Regression []StepCall
    Cleanup    []StepCall
    Pos        Position
}

/* Parse tokenises src and returns its declarative-pipeline AST.
 * On a malformed input it returns an error pinned to (line, col)
 * of the offending token.
 */
func Parse(src []byte) (*PipelineFile, error) {
    tokens, err := Tokenize(src)
    if err != nil {
        return nil, err
    }
    p := &parser{tokens: tokens, pos: 0}
    file := &PipelineFile{}
    for !p.atEOF() {
        tok := p.peek()
        if tok.Kind == TokKeyword && tok.Value == "pipeline" {
            block, err := p.parsePipelineBlock()
            if err != nil {
                return nil, err
            }
            file.Pipeline = block
            break
        }
        file.Preamble = append(file.Preamble, p.advance())
    }
    if file.Pipeline == nil {
        return nil, fmt.Errorf(
            "pipeline.Parse: no pipeline block found in source")
    }
    return file, nil
}

/* parser is the per-call cursor state. */
type parser struct {
    tokens []Token
    pos    int
}

func (p *parser) peek() Token {
    return p.tokens[p.pos]
}

func (p *parser) advance() Token {
    t := p.tokens[p.pos]
    if t.Kind != TokEOF {
        p.pos++
    }
    return t
}

func (p *parser) atEOF() bool {
    return p.tokens[p.pos].Kind == TokEOF
}

/* skipNewlines advances past any sequence of newline or
 * semicolon tokens at the current cursor. Other tokens stop
 * the skip.
 */
func (p *parser) skipNewlines() {
    for {
        k := p.peek().Kind
        if k != TokNewline && k != TokSemicolon {
            return
        }
        p.advance()
    }
}

/* captureUntil collects tokens up to the matching closer at
 * balanced (paren, brace, bracket) depth 0. The opener token is
 * already consumed by the caller and is passed in only for the
 * error message. The closer token is consumed on success.
 */
func (p *parser) captureUntil(closer TokenKind, opener Token,
    label string) ([]Token, error) {
    paren, brace, brack := 0, 0, 0
    var out []Token
    for !p.atEOF() {
        tok := p.peek()
        if paren == 0 && brace == 0 && brack == 0 &&
            tok.Kind == closer {
            p.advance()
            return out, nil
        }
        switch tok.Kind {
        case TokLParen:
            paren++
        case TokRParen:
            if paren > 0 {
                paren--
            }
        case TokLBrace:
            brace++
        case TokRBrace:
            if brace > 0 {
                brace--
            }
        case TokLBracket:
            brack++
        case TokRBracket:
            if brack > 0 {
                brack--
            }
        }
        out = append(out, p.advance())
    }
    return nil, fmt.Errorf(
        "pipeline.Parse: unterminated %s starting at %d:%d",
        label, opener.Line, opener.Col)
}

/* captureNakedArgs reads tokens up to the next newline or
 * semicolon AT depth 0 (so a closure or list spanning lines is
 * absorbed correctly). The terminating newline/semicolon and
 * a closing '}' belonging to a parent block are NOT consumed.
 */
func (p *parser) captureNakedArgs() []Token {
    paren, brace, brack := 0, 0, 0
    var out []Token
    for !p.atEOF() {
        tok := p.peek()
        if paren == 0 && brace == 0 && brack == 0 {
            if tok.Kind == TokNewline ||
                tok.Kind == TokSemicolon ||
                tok.Kind == TokRBrace {
                return out
            }
        }
        switch tok.Kind {
        case TokLParen:
            paren++
        case TokRParen:
            if paren > 0 {
                paren--
            }
        case TokLBrace:
            brace++
        case TokRBrace:
            if brace > 0 {
                brace--
            }
        case TokLBracket:
            brack++
        case TokRBracket:
            if brack > 0 {
                brack--
            }
        }
        out = append(out, p.advance())
    }
    return out
}

/* expectLBrace advances past the next '{' or returns an error
 * pinned to the current token. Whitespace newlines between the
 * caller's previous token and the brace are skipped.
 */
func (p *parser) expectLBrace(ctx string) (Token, error) {
    p.skipNewlines()
    tok := p.peek()
    if tok.Kind != TokLBrace {
        return Token{}, fmt.Errorf(
            "pipeline.Parse: expected { after %s at %d:%d",
            ctx, tok.Line, tok.Col)
    }
    return p.advance(), nil
}

/* parsePipelineBlock parses `pipeline { ... }`. The cursor must
 * be on the 'pipeline' keyword.
 */
func (p *parser) parsePipelineBlock() (*PipelineBlock, error) {
    head := p.advance() /* 'pipeline' */
    openTok, err := p.expectLBrace("pipeline")
    if err != nil {
        return nil, err
    }
    pb := &PipelineBlock{Pos: Position{Line: head.Line,
        Col: head.Col}}
    for {
        p.skipNewlines()
        if p.peek().Kind == TokRBrace {
            p.advance()
            return pb, nil
        }
        if p.atEOF() {
            return nil, fmt.Errorf(
                "pipeline.Parse: unterminated pipeline block "+
                    "starting at %d:%d (no matching })",
                openTok.Line, openTok.Col)
        }
        sec := p.peek()
        if sec.Kind != TokKeyword && sec.Kind != TokIdent {
            return nil, fmt.Errorf(
                "pipeline.Parse: expected pipeline section "+
                    "at %d:%d, got %q",
                sec.Line, sec.Col, sec.Value)
        }
        switch sec.Value {
        case "agent":
            p.advance()
            ab, err := p.parseAgent()
            if err != nil {
                return nil, err
            }
            pb.Agent = ab
        case "options":
            p.advance()
            ob, err := p.parseOptions()
            if err != nil {
                return nil, err
            }
            pb.Options = ob
        case "stages":
            p.advance()
            ss, err := p.parseStages()
            if err != nil {
                return nil, err
            }
            pb.Stages = ss
        case "post":
            p.advance()
            postb, err := p.parsePost()
            if err != nil {
                return nil, err
            }
            pb.Post = postb
        default:
            return nil, fmt.Errorf(
                "pipeline.Parse: unknown pipeline section %q "+
                    "at %d:%d (18.12 supports agent, options, "+
                    "stages, post)",
                sec.Value, sec.Line, sec.Col)
        }
    }
}

/* parseAgent parses the body of `agent ...` (caller consumed
 * 'agent'). Supports `agent any`, `agent none`, and
 * `agent { label '<str>' }` only; other forms surface as
 * errors so 18.12 fails loudly when a Jenkinsfile reaches for
 * an unimplemented agent type.
 */
func (p *parser) parseAgent() (*AgentBlock, error) {
    p.skipNewlines()
    head := p.peek()
    ab := &AgentBlock{Pos: Position{Line: head.Line,
        Col: head.Col}}
    if head.Kind == TokIdent || head.Kind == TokKeyword {
        switch head.Value {
        case "any":
            p.advance()
            ab.Any = true
            return ab, nil
        case "none":
            p.advance()
            ab.None = true
            return ab, nil
        }
    }
    if head.Kind != TokLBrace {
        return nil, fmt.Errorf(
            "pipeline.Parse: expected agent { or "+
                "'any'/'none' at %d:%d, got %q",
            head.Line, head.Col, head.Value)
    }
    p.advance() /* '{' */
    p.skipNewlines()
    lab := p.peek()
    if lab.Kind != TokKeyword || lab.Value != "label" {
        return nil, fmt.Errorf(
            "pipeline.Parse: expected 'label' inside agent { } "+
                "at %d:%d (18.12 supports the label form only)",
            lab.Line, lab.Col)
    }
    p.advance() /* 'label' */
    p.skipNewlines()
    val := p.peek()
    if val.Kind != TokString {
        return nil, fmt.Errorf(
            "pipeline.Parse: expected string after 'label' "+
                "at %d:%d", val.Line, val.Col)
    }
    p.advance()
    ab.Label = val.Value
    p.skipNewlines()
    if p.peek().Kind != TokRBrace {
        return nil, fmt.Errorf(
            "pipeline.Parse: expected } to close agent at %d:%d",
            p.peek().Line, p.peek().Col)
    }
    p.advance()
    return ab, nil
}

/* parseOptions parses `options { ... }` (caller consumed
 * 'options'). Each child is an ident or keyword followed by
 * paren'd args (or no args at all). Anything else is an error.
 */
func (p *parser) parseOptions() (*OptionsBlock, error) {
    openTok, err := p.expectLBrace("options")
    if err != nil {
        return nil, err
    }
    ob := &OptionsBlock{Pos: Position{Line: openTok.Line,
        Col: openTok.Col}}
    for {
        p.skipNewlines()
        if p.peek().Kind == TokRBrace {
            p.advance()
            return ob, nil
        }
        if p.atEOF() {
            return nil, fmt.Errorf(
                "pipeline.Parse: unterminated options block "+
                    "starting at %d:%d",
                openTok.Line, openTok.Col)
        }
        head := p.peek()
        if head.Kind != TokIdent && head.Kind != TokKeyword {
            return nil, fmt.Errorf(
                "pipeline.Parse: expected option call inside "+
                    "options block at %d:%d, got %q",
                head.Line, head.Col, head.Value)
        }
        p.advance()
        call := Call{Name: head.Value,
            Pos: Position{Line: head.Line, Col: head.Col}}
        if p.peek().Kind == TokLParen {
            opener := p.advance()
            body, err := p.captureUntil(TokRParen, opener, "(")
            if err != nil {
                return nil, err
            }
            call.ArgTokens = body
        }
        ob.Calls = append(ob.Calls, call)
    }
}

/* parseStages parses `stages { stage(...) { } ... }` (caller
 * consumed 'stages'). Only stage(...) children are accepted.
 */
func (p *parser) parseStages() (*StagesBlock, error) {
    openTok, err := p.expectLBrace("stages")
    if err != nil {
        return nil, err
    }
    sb := &StagesBlock{Pos: Position{Line: openTok.Line,
        Col: openTok.Col}}
    for {
        p.skipNewlines()
        if p.peek().Kind == TokRBrace {
            p.advance()
            return sb, nil
        }
        if p.atEOF() {
            return nil, fmt.Errorf(
                "pipeline.Parse: unterminated stages block "+
                    "starting at %d:%d",
                openTok.Line, openTok.Col)
        }
        head := p.peek()
        if head.Kind != TokKeyword || head.Value != "stage" {
            return nil, fmt.Errorf(
                "pipeline.Parse: stages block expects only "+
                    "stage(...) children, got %q at %d:%d",
                head.Value, head.Line, head.Col)
        }
        p.advance()
        st, err := p.parseStage()
        if err != nil {
            return nil, err
        }
        sb.Stages = append(sb.Stages, st)
    }
}

/* parseStage parses `('Name') { ... }` (caller consumed
 * 'stage'). Inside the body we accept when / steps / post /
 * parallel children; anything else is an error pinned to the
 * offending token.
 */
func (p *parser) parseStage() (*Stage, error) {
    if p.peek().Kind != TokLParen {
        tok := p.peek()
        return nil, fmt.Errorf(
            "pipeline.Parse: expected ( after 'stage' at "+
                "%d:%d, got %q", tok.Line, tok.Col, tok.Value)
    }
    p.advance() /* '(' */
    p.skipNewlines()
    nameTok := p.peek()
    if nameTok.Kind != TokString {
        return nil, fmt.Errorf(
            "pipeline.Parse: expected string for stage name "+
                "at %d:%d, got %q",
            nameTok.Line, nameTok.Col, nameTok.Value)
    }
    p.advance()
    p.skipNewlines()
    if p.peek().Kind != TokRParen {
        tok := p.peek()
        return nil, fmt.Errorf(
            "pipeline.Parse: expected ) after stage name at "+
                "%d:%d, got %q", tok.Line, tok.Col, tok.Value)
    }
    p.advance()
    if _, err := p.expectLBrace("stage"); err != nil {
        return nil, err
    }
    stage := &Stage{Name: nameTok.Value,
        Pos: Position{Line: nameTok.Line, Col: nameTok.Col}}
    for {
        p.skipNewlines()
        if p.peek().Kind == TokRBrace {
            p.advance()
            return stage, nil
        }
        if p.atEOF() {
            return nil, fmt.Errorf(
                "pipeline.Parse: unterminated stage %q "+
                    "starting at %d:%d", stage.Name,
                stage.Pos.Line, stage.Pos.Col)
        }
        head := p.peek()
        if head.Kind != TokKeyword && head.Kind != TokIdent {
            return nil, fmt.Errorf(
                "pipeline.Parse: expected stage child at "+
                    "%d:%d, got %q",
                head.Line, head.Col, head.Value)
        }
        switch head.Value {
        case "when":
            p.advance()
            wb, err := p.parseWhen()
            if err != nil {
                return nil, err
            }
            stage.When = wb
        case "steps":
            p.advance()
            if _, err := p.expectLBrace("steps"); err != nil {
                return nil, err
            }
            steps, err := p.parseStepList("steps")
            if err != nil {
                return nil, err
            }
            stage.Steps = &StepsBlock{
                Steps: steps,
                Pos:   Position{Line: head.Line, Col: head.Col},
            }
        case "parallel":
            p.advance()
            par, err := p.parseParallelStageChild(head)
            if err != nil {
                return nil, err
            }
            stage.Parallel = par
        case "post":
            p.advance()
            pst, err := p.parsePost()
            if err != nil {
                return nil, err
            }
            stage.Post = pst
        default:
            return nil, fmt.Errorf(
                "pipeline.Parse: unknown stage child %q at "+
                    "%d:%d (18.12 supports when, steps, "+
                    "parallel, post)",
                head.Value, head.Line, head.Col)
        }
    }
}

/* parseWhen parses `when { expression { ... } }` (caller
 * consumed 'when'). Only the expression form is implemented;
 * other when conditions are out of scope.
 */
func (p *parser) parseWhen() (*WhenBlock, error) {
    openTok, err := p.expectLBrace("when")
    if err != nil {
        return nil, err
    }
    p.skipNewlines()
    head := p.peek()
    if head.Kind != TokKeyword || head.Value != "expression" {
        return nil, fmt.Errorf(
            "pipeline.Parse: expected 'expression' inside "+
                "when {} at %d:%d (18.12 supports the "+
                "expression form only)",
            head.Line, head.Col)
    }
    p.advance() /* 'expression' */
    bodyOpen, err := p.expectLBrace("expression")
    if err != nil {
        return nil, err
    }
    body, err := p.captureUntil(TokRBrace, bodyOpen, "{")
    if err != nil {
        return nil, err
    }
    wb := &WhenBlock{ExpressionBody: body,
        Pos: Position{Line: openTok.Line, Col: openTok.Col}}
    p.skipNewlines()
    if p.peek().Kind != TokRBrace {
        return nil, fmt.Errorf(
            "pipeline.Parse: expected } to close when at "+
                "%d:%d", p.peek().Line, p.peek().Col)
    }
    p.advance()
    return wb, nil
}

/* parseParallelStageChild parses a stage-direct
 * `parallel { stage(...) { } ... }` block (caller consumed
 * 'parallel'). The argless form is the only one accepted at
 * stage-child position; a `parallel <arg>` form belongs inside
 * a steps {} block (parsed by parseStepList -> parseStepCall).
 */
func (p *parser) parseParallelStageChild(
    parTok Token) (*ParallelBlock, error) {
    openTok, err := p.expectLBrace("parallel")
    if err != nil {
        return nil, err
    }
    pb := &ParallelBlock{Pos: Position{Line: parTok.Line,
        Col: parTok.Col}}
    for {
        p.skipNewlines()
        if p.peek().Kind == TokRBrace {
            p.advance()
            return pb, nil
        }
        if p.atEOF() {
            return nil, fmt.Errorf(
                "pipeline.Parse: unterminated parallel block "+
                    "starting at %d:%d",
                openTok.Line, openTok.Col)
        }
        head := p.peek()
        if head.Kind != TokKeyword || head.Value != "stage" {
            return nil, fmt.Errorf(
                "pipeline.Parse: parallel block expects only "+
                    "stage(...) children, got %q at %d:%d",
                head.Value, head.Line, head.Col)
        }
        p.advance()
        st, err := p.parseStage()
        if err != nil {
            return nil, err
        }
        pb.Branches = append(pb.Branches, st)
    }
}

/* parsePost parses `post { <handler> { <stepCall>* } ... }`
 * (caller consumed 'post'). Known handler names are
 * always/success/failure/unstable/aborted/changed/fixed/
 * regression/cleanup; anything else is rejected at parse time.
 */
func (p *parser) parsePost() (*PostBlock, error) {
    openTok, err := p.expectLBrace("post")
    if err != nil {
        return nil, err
    }
    pb := &PostBlock{Pos: Position{Line: openTok.Line,
        Col: openTok.Col}}
    for {
        p.skipNewlines()
        if p.peek().Kind == TokRBrace {
            p.advance()
            return pb, nil
        }
        if p.atEOF() {
            return nil, fmt.Errorf(
                "pipeline.Parse: unterminated post block "+
                    "starting at %d:%d",
                openTok.Line, openTok.Col)
        }
        head := p.peek()
        if head.Kind != TokKeyword && head.Kind != TokIdent {
            return nil, fmt.Errorf(
                "pipeline.Parse: expected post handler "+
                    "(always/success/failure/...) at %d:%d, "+
                    "got %q",
                head.Line, head.Col, head.Value)
        }
        p.advance()
        if _, err := p.expectLBrace(head.Value); err != nil {
            return nil, err
        }
        steps, err := p.parseStepList(head.Value)
        if err != nil {
            return nil, err
        }
        switch head.Value {
        case "always":
            pb.Always = steps
        case "success":
            pb.Success = steps
        case "failure":
            pb.Failure = steps
        case "unstable":
            pb.Unstable = steps
        case "aborted":
            pb.Aborted = steps
        case "changed":
            pb.Changed = steps
        case "fixed":
            pb.Fixed = steps
        case "regression":
            pb.Regression = steps
        case "cleanup":
            pb.Cleanup = steps
        default:
            return nil, fmt.Errorf(
                "pipeline.Parse: unknown post handler %q at "+
                    "%d:%d", head.Value,
                head.Line, head.Col)
        }
    }
}

/* parseStepList consumes a sequence of step calls up to the
 * matching '}' (caller consumed the opening '{'). The label is
 * used only in the unterminated-block error.
 */
func (p *parser) parseStepList(label string) ([]StepCall,
    error) {
    var out []StepCall
    for {
        p.skipNewlines()
        if p.peek().Kind == TokRBrace {
            p.advance()
            return out, nil
        }
        if p.atEOF() {
            return nil, fmt.Errorf(
                "pipeline.Parse: unterminated %s block", label)
        }
        sc, err := p.parseStepCall()
        if err != nil {
            return nil, err
        }
        out = append(out, sc)
    }
}

/* parseStepCall reads one step at the cursor. The first token
 * must be a name (ident or keyword). After the name we look at
 * the next token to decide ArgsKind:
 *
 *   '('  -> paren'd args, captured up to the matching ')'
 *   '{'  -> no args, the brace starts a trailing closure block
 *   newline/semicolon/'}' (statement end) -> no args, no block
 *   anything else -> naked args up to the next stmt end
 *
 * A trailing closure block is captured raw whenever a '{'
 * appears immediately after the args (or after intervening
 * newlines but with no other intervening tokens).
 */
func (p *parser) parseStepCall() (StepCall, error) {
    nameTok := p.advance()
    if nameTok.Kind != TokIdent && nameTok.Kind != TokKeyword {
        return StepCall{}, fmt.Errorf(
            "pipeline.Parse: expected step name at %d:%d, "+
                "got %q",
            nameTok.Line, nameTok.Col, nameTok.Value)
    }
    sc := StepCall{
        Name:     nameTok.Value,
        ArgsKind: ArgsNone,
        Pos: Position{Line: nameTok.Line,
            Col: nameTok.Col},
    }
    switch {
    case p.peek().Kind == TokLParen:
        opener := p.advance()
        body, err := p.captureUntil(TokRParen, opener, "(")
        if err != nil {
            return StepCall{}, err
        }
        sc.ArgsKind = ArgsParen
        sc.ArgTokens = body
    case isStmtEnd(p.peek()) || p.peek().Kind == TokLBrace:
        /* No args; either statement end or directly into a
         * trailing block. Leave ArgsKind = ArgsNone. */
    default:
        sc.ArgsKind = ArgsNaked
        sc.ArgTokens = p.captureNakedArgs()
    }
    /* Look for a trailing closure block. Allow the '{' to live
     * on the next line after the args; Groovy idiomatically
     * accepts both forms.
     */
    saved := p.pos
    for p.peek().Kind == TokNewline {
        p.advance()
    }
    if p.peek().Kind == TokLBrace {
        opener := p.advance()
        body, err := p.captureUntil(TokRBrace, opener, "{")
        if err != nil {
            return StepCall{}, err
        }
        sc.HasBlock = true
        sc.Block = body
    } else {
        p.pos = saved
    }
    return sc, nil
}

/* isStmtEnd reports whether t is a statement terminator at
 * step-call depth 0. Used by parseStepCall to detect "this step
 * has no args at all".
 */
func isStmtEnd(t Token) bool {
    return t.Kind == TokNewline ||
        t.Kind == TokSemicolon ||
        t.Kind == TokEOF ||
        t.Kind == TokRBrace
}
