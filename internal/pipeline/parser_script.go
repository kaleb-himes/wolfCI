/* internal/pipeline/parser_script.go - PLAN.md 18.13.
 *
 * Parses the Groovy script-subset that the declarative parser
 * (parser_declarative.go) captures as raw []Token slices.
 * Supports the constructs PLAN.md 18.13 calls out:
 *
 *   - Function declarations: `def f(params) { ... }` (typed
 *     and untyped parameters, default values).
 *   - Local declarations: `def x = expr`, `Type x = expr`.
 *   - Assignment statements: `x = expr`, `obj.f = expr`,
 *     `xs[i] = expr`, compound assigns (+=, -=).
 *   - List + map literals: `[a, b, c]`, `[k: v, k2: v2]`,
 *     `[:]`, with newline-insensitive parsing inside [] and ()
 *     so multi-line literals (and Groovy's leading-comma style)
 *     work.
 *   - Closures: `{ -> ... }`, `{ x -> ... }`, `{ x, y -> ... }`,
 *     bodyless `{ it * 2 }`. Closures as trailing arguments to
 *     method calls (xs.each { ... }) are recognised by the
 *     postfix-call machinery.
 *   - Member access (a.b.c), subscript (xs[i]), method calls
 *     (a.b(c, d)) including labelled args (k: v).
 *   - Command-style calls (`echo "..."`) for the one Jenkins
 *     convention jenkinsUtils.groovy actually uses; recognised
 *     at statement level when the parsed expression is an ident
 *     or member-access and the next token starts an expression.
 *   - Control flow: if / else (single-statement or block body),
 *     for-in (`for (x in xs)`) and C-style `for (a; b; c)`,
 *     while, try / catch (typed catch params) / finally.
 *   - return / throw / break / continue, return without value.
 *   - Annotations: `@NonCPS` on a declaration; the annotation
 *     name is recorded on the FuncDecl and otherwise has no
 *     parser-time effect.
 *   - Expression precedence (low -> high): assign, ternary
 *     (?:), logical-or, logical-and, equality (== !=),
 *     relational (< > <= >= instanceof), additive (+ -),
 *     multiplicative (* /), unary (! -), postfix (. ( [ {).
 *   - Primary: NUMBER, STRING (any of the 4 lexer flavors),
 *     true/false/null, IDENT, parenthesized expr, list/map
 *     literal, closure, `new TypeName(args)`.
 *
 * Newline-significance rule: inside () and [] (tracked by
 * bracketDepth), newlines are pure whitespace; at depth 0,
 * newlines (and semicolons) are statement terminators. {} does
 * not change bracketDepth because closures and blocks both
 * contain statement sequences.
 *
 * Out of scope for 18.13 (will surface as parse errors if a
 * Jenkinsfile reaches for them): chained assignment
 * `a = b = c`, multi-variable single-def `def a, b = 1`, safe
 * navigation `a?.b`, elvis `a ?: b`, Groovy ranges `1..10`,
 * spread operator `*xs`, generics in type names.
 */
package pipeline

import "fmt"

/* ----- AST ---------------------------------------------------- */

/* ScriptFile is the top-level AST returned by ParseScript. */
type ScriptFile struct {
    Block *Block
}

/* Block is an ordered list of statements. The top-level script
 * is a Block; so are function bodies, closure bodies, and the
 * bodies of if/else/for/while/try/catch/finally.
 */
type Block struct {
    Statements []Stmt
    Pos        Position
}

/* Stmt is the discriminated union of statement nodes. The
 * stmtNode marker method enforces the closed set; callers use
 * a type switch to dispatch.
 */
type Stmt interface {
    stmtNode()
    StmtPos() Position
}

/* Expr is the discriminated union of expression nodes. */
type Expr interface {
    exprNode()
    ExprPos() Position
}

/* Param is one function/closure parameter. Type is "" for an
 * untyped param; Default is nil when the param has no default.
 */
type Param struct {
    Type    string
    Name    string
    Default Expr
}

/* FuncDecl is `def f(params) { body }` at statement level. */
type FuncDecl struct {
    Name        string
    Params      []Param
    Body        *Block
    Annotations []string
    Pos         Position
}

func (*FuncDecl) stmtNode()           {}
func (s *FuncDecl) StmtPos() Position { return s.Pos }

/* VarDecl is `def x [= expr]` or `Type x [= expr]`. Type is ""
 * for `def`. Init is nil when there is no initialiser.
 */
type VarDecl struct {
    Type string
    Name string
    Init Expr
    Pos  Position
}

func (*VarDecl) stmtNode()           {}
func (s *VarDecl) StmtPos() Position { return s.Pos }

/* AssignStmt is `target op value` where op is one of =, +=, -=.
 * Target must be an lvalue (IdentExpr, MemberExpr, SubscriptExpr).
 */
type AssignStmt struct {
    Target Expr
    Op     string
    Value  Expr
    Pos    Position
}

func (*AssignStmt) stmtNode()           {}
func (s *AssignStmt) StmtPos() Position { return s.Pos }

/* ExprStmt wraps a bare expression as a statement. */
type ExprStmt struct {
    X   Expr
    Pos Position
}

func (*ExprStmt) stmtNode()           {}
func (s *ExprStmt) StmtPos() Position { return s.Pos }

/* IfStmt is `if (cond) then [else elseStmt]`. Either branch
 * may be a single statement OR a {} block (wrapped as
 * BlockStmt).
 */
type IfStmt struct {
    Cond Expr
    Then Stmt
    Else Stmt /* nil when there's no else */
    Pos  Position
}

func (*IfStmt) stmtNode()           {}
func (s *IfStmt) StmtPos() Position { return s.Pos }

/* ForInStmt is Groovy's `for (var in iter) body`. */
type ForInStmt struct {
    VarName string
    Iter    Expr
    Body    Stmt
    Pos     Position
}

func (*ForInStmt) stmtNode()           {}
func (s *ForInStmt) StmtPos() Position { return s.Pos }

/* ForCStmt is C-style `for (init; cond; step) body`. */
type ForCStmt struct {
    Init Stmt /* a VarDecl, AssignStmt, ExprStmt, or nil */
    Cond Expr /* may be nil */
    Step Expr /* may be nil */
    Body Stmt
    Pos  Position
}

func (*ForCStmt) stmtNode()           {}
func (s *ForCStmt) StmtPos() Position { return s.Pos }

/* WhileStmt is `while (cond) body`. */
type WhileStmt struct {
    Cond Expr
    Body Stmt
    Pos  Position
}

func (*WhileStmt) stmtNode()           {}
func (s *WhileStmt) StmtPos() Position { return s.Pos }

/* TryStmt is `try { body } [catch (T name) { ... }]*
 * [finally { ... }]`. */
type TryStmt struct {
    Body    *Block
    Catches []CatchClause
    Finally *Block /* nil when absent */
    Pos     Position
}

func (*TryStmt) stmtNode()           {}
func (s *TryStmt) StmtPos() Position { return s.Pos }

/* CatchClause is `catch (Type name) { body }`. Type may be ""
 * if the catch parameter is untyped.
 */
type CatchClause struct {
    ParamType string
    ParamName string
    Body      *Block
    Pos       Position
}

/* ReturnStmt is `return [expr]`. Value is nil when there's no
 * value.
 */
type ReturnStmt struct {
    Value Expr
    Pos   Position
}

func (*ReturnStmt) stmtNode()           {}
func (s *ReturnStmt) StmtPos() Position { return s.Pos }

/* ThrowStmt is `throw expr`. */
type ThrowStmt struct {
    Value Expr
    Pos   Position
}

func (*ThrowStmt) stmtNode()           {}
func (s *ThrowStmt) StmtPos() Position { return s.Pos }

/* BreakStmt is `break`. */
type BreakStmt struct {
    Pos Position
}

func (*BreakStmt) stmtNode()           {}
func (s *BreakStmt) StmtPos() Position { return s.Pos }

/* ContinueStmt is `continue`. */
type ContinueStmt struct {
    Pos Position
}

func (*ContinueStmt) stmtNode()           {}
func (s *ContinueStmt) StmtPos() Position { return s.Pos }

/* BlockStmt wraps a bare {} block used as a statement (e.g.
 * the body of an if when written with braces). */
type BlockStmt struct {
    Body *Block
    Pos  Position
}

func (*BlockStmt) stmtNode()           {}
func (s *BlockStmt) StmtPos() Position { return s.Pos }

/* NumberLit is a decimal integer literal. */
type NumberLit struct {
    Value string
    Pos   Position
}

func (*NumberLit) exprNode()           {}
func (e *NumberLit) ExprPos() Position { return e.Pos }

/* StringLit is a string literal. Kind preserves the source's
 * quote style for the interpreter; Value is the lexer's
 * post-quote-stripping body (interpolation markers preserved).
 */
type StringLit struct {
    Value string
    Kind  StringKind
    Pos   Position
}

func (*StringLit) exprNode()           {}
func (e *StringLit) ExprPos() Position { return e.Pos }

/* BoolLit is `true` or `false`. */
type BoolLit struct {
    Value bool
    Pos   Position
}

func (*BoolLit) exprNode()           {}
func (e *BoolLit) ExprPos() Position { return e.Pos }

/* NullLit is `null`. */
type NullLit struct {
    Pos Position
}

func (*NullLit) exprNode()           {}
func (e *NullLit) ExprPos() Position { return e.Pos }

/* IdentExpr is a bare identifier reference. */
type IdentExpr struct {
    Name string
    Pos  Position
}

func (*IdentExpr) exprNode()           {}
func (e *IdentExpr) ExprPos() Position { return e.Pos }

/* MemberExpr is `obj.name`. */
type MemberExpr struct {
    Object Expr
    Name   string
    Pos    Position
}

func (*MemberExpr) exprNode()           {}
func (e *MemberExpr) ExprPos() Position { return e.Pos }

/* SubscriptExpr is `obj[index]`. */
type SubscriptExpr struct {
    Object Expr
    Index  Expr
    Pos    Position
}

func (*SubscriptExpr) exprNode()           {}
func (e *SubscriptExpr) ExprPos() Position { return e.Pos }

/* CallArg is one argument to a CallExpr. Name is "" for a
 * positional argument; otherwise the labelled-arg name.
 */
type CallArg struct {
    Name  string
    Value Expr
}

/* CallExpr is `fn(args) [closure]`. ClosureArg is non-nil when
 * the call has a trailing closure block. */
type CallExpr struct {
    Fn         Expr
    Args       []CallArg
    ClosureArg *ClosureExpr
    Pos        Position
}

func (*CallExpr) exprNode()           {}
func (e *CallExpr) ExprPos() Position { return e.Pos }

/* CommandCallExpr is `ident expr` form (e.g. `echo "..."`). */
type CommandCallExpr struct {
    Fn  Expr
    Arg Expr
    Pos Position
}

func (*CommandCallExpr) exprNode()           {}
func (e *CommandCallExpr) ExprPos() Position { return e.Pos }

/* BinaryExpr is a binary infix operator application. Op
 * stores the operator text (e.g. "+", "&&", "instanceof").
 */
type BinaryExpr struct {
    Op  string
    Lhs Expr
    Rhs Expr
    Pos Position
}

func (*BinaryExpr) exprNode()           {}
func (e *BinaryExpr) ExprPos() Position { return e.Pos }

/* UnaryExpr is a prefix unary operator application. */
type UnaryExpr struct {
    Op  string
    X   Expr
    Pos Position
}

func (*UnaryExpr) exprNode()           {}
func (e *UnaryExpr) ExprPos() Position { return e.Pos }

/* TernaryExpr is `cond ? then : else`. */
type TernaryExpr struct {
    Cond Expr
    Then Expr
    Else Expr
    Pos  Position
}

func (*TernaryExpr) exprNode()           {}
func (e *TernaryExpr) ExprPos() Position { return e.Pos }

/* AssignExpr is `target op value` as an expression. Used when
 * an assignment appears inside a larger expression (rare in
 * the 18.13 subset; AssignStmt is the more common form).
 */
type AssignExpr struct {
    Target Expr
    Op     string
    Value  Expr
    Pos    Position
}

func (*AssignExpr) exprNode()           {}
func (e *AssignExpr) ExprPos() Position { return e.Pos }

/* ListExpr is `[e1, e2, ...]`. */
type ListExpr struct {
    Elements []Expr
    Pos      Position
}

func (*ListExpr) exprNode()           {}
func (e *ListExpr) ExprPos() Position { return e.Pos }

/* MapEntry is one key:value entry in a MapExpr. */
type MapEntry struct {
    Key   Expr
    Value Expr
}

/* MapExpr is `[k: v, ...]` or `[:]` (empty). */
type MapExpr struct {
    Entries []MapEntry
    Pos     Position
}

func (*MapExpr) exprNode()           {}
func (e *MapExpr) ExprPos() Position { return e.Pos }

/* ClosureExpr is `{ params -> body }` or `{ body }`. Params is
 * empty when no `->` is present (Groovy's implicit `it` rule). */
type ClosureExpr struct {
    Params []Param
    Body   *Block
    Pos    Position
}

func (*ClosureExpr) exprNode()           {}
func (e *ClosureExpr) ExprPos() Position { return e.Pos }

/* NewExpr is `new TypeName(args)`. */
type NewExpr struct {
    Type string
    Args []CallArg
    Pos  Position
}

func (*NewExpr) exprNode()           {}
func (e *NewExpr) ExprPos() Position { return e.Pos }

/* ----- ParseScript entry ------------------------------------- */

/* ParseScript tokenises src and parses it as a Groovy script
 * body. The resulting ScriptFile.Block holds the top-level
 * statements (function declarations, assignments, expression
 * statements, and control flow).
 */
func ParseScript(src []byte) (*ScriptFile, error) {
    tokens, err := Tokenize(src)
    if err != nil {
        return nil, err
    }
    return ParseScriptTokens(tokens)
}

/* ParseScriptTokens is the entry point used by the declarative
 * parser (and 18.15's interpreter) when the source has already
 * been tokenised - e.g. when the input is the raw token slice
 * captured from a script {} block inside a Jenkinsfile.
 */
func ParseScriptTokens(tokens []Token) (*ScriptFile, error) {
    p := &scriptParser{tokens: tokens}
    block, err := p.parseTopBlock()
    if err != nil {
        return nil, err
    }
    return &ScriptFile{Block: block}, nil
}

/* ----- parser state ------------------------------------------- */

type scriptParser struct {
    tokens []Token
    pos    int
    /* bracketDepth counts open () and [] only. Inside them,
     * newlines are whitespace; outside, they terminate
     * statements. {} does NOT affect bracketDepth because
     * blocks and closures contain statement sequences. */
    bracketDepth int
}

func (p *scriptParser) peek() Token {
    return p.tokens[p.pos]
}

func (p *scriptParser) lookAhead(n int) Token {
    /* nlSensitive=false: skip newlines while looking ahead so
     * that constructs like `def f(\n  a,\n  b)` work even when
     * the cursor is currently on a newline. */
    saved := p.pos
    defer func() { p.pos = saved }()
    for i := 0; i < n; i++ {
        for p.tokens[p.pos].Kind == TokNewline {
            if p.tokens[p.pos].Kind == TokEOF {
                break
            }
            p.pos++
        }
        if p.tokens[p.pos].Kind == TokEOF {
            return p.tokens[p.pos]
        }
        p.pos++
    }
    for p.tokens[p.pos].Kind == TokNewline {
        if p.tokens[p.pos].Kind == TokEOF {
            break
        }
        p.pos++
    }
    return p.tokens[p.pos]
}

func (p *scriptParser) advance() Token {
    t := p.tokens[p.pos]
    if t.Kind != TokEOF {
        p.pos++
    }
    return t
}

func (p *scriptParser) atEOF() bool {
    return p.tokens[p.pos].Kind == TokEOF
}

func (p *scriptParser) enterBracket() {
    p.bracketDepth++
}

func (p *scriptParser) exitBracket() {
    if p.bracketDepth > 0 {
        p.bracketDepth--
    }
}

/* skipNL advances past any newline or semicolon at the cursor.
 * Use between statements, around delimiters, and after opening
 * tokens that introduce a sequence.
 */
func (p *scriptParser) skipNL() {
    for {
        k := p.peek().Kind
        if k != TokNewline && k != TokSemicolon {
            return
        }
        p.advance()
    }
}

/* skipWS advances past newlines that are not statement
 * terminators - i.e. when we are inside () or []. Outside, it
 * is a no-op. The script parser calls this after consuming any
 * '(' or '[' (and similarly before checking for the matching
 * close) so multi-line argument lists and list literals parse
 * uniformly.
 */
func (p *scriptParser) skipWS() {
    if p.bracketDepth == 0 {
        return
    }
    for p.peek().Kind == TokNewline {
        p.advance()
    }
}

/* errorf returns a parse error pinned to the current token. */
func (p *scriptParser) errorf(format string,
    args ...interface{}) error {
    tok := p.peek()
    return fmt.Errorf(
        "pipeline.ParseScript: %s at %d:%d (got %q)",
        fmt.Sprintf(format, args...), tok.Line, tok.Col,
        tok.Value)
}

/* ----- top-level + statements ------------------------------- */

func (p *scriptParser) parseTopBlock() (*Block, error) {
    block := &Block{Pos: Position{Line: 1, Col: 1}}
    for {
        p.skipNL()
        if p.atEOF() {
            return block, nil
        }
        st, err := p.parseTopStmt()
        if err != nil {
            return nil, err
        }
        if st != nil {
            block.Statements = append(block.Statements, st)
        }
    }
}

func (p *scriptParser) parseTopStmt() (Stmt, error) {
    /* Collect annotations preceding the next declaration. */
    var annotations []string
    for p.peek().Kind == TokAnnotation {
        annotations = append(annotations, p.advance().Value)
        p.skipNL()
    }
    /* Disambiguate function declaration vs everything else.
     * Function decl shape: `def IDENT (`. */
    if p.peek().Kind == TokKeyword && p.peek().Value == "def" {
        if p.lookAhead(1).Kind == TokIdent &&
            p.lookAhead(2).Kind == TokLParen {
            return p.parseFuncDecl(annotations)
        }
    }
    return p.parseStmt()
}

func (p *scriptParser) parseStmt() (Stmt, error) {
    p.skipNL()
    tok := p.peek()
    if tok.Kind == TokKeyword {
        switch tok.Value {
        case "if":
            return p.parseIf()
        case "for":
            return p.parseFor()
        case "while":
            return p.parseWhile()
        case "try":
            return p.parseTry()
        case "return":
            return p.parseReturn()
        case "throw":
            return p.parseThrow()
        case "break":
            t := p.advance()
            return &BreakStmt{Pos: Position{Line: t.Line,
                Col: t.Col}}, nil
        case "continue":
            t := p.advance()
            return &ContinueStmt{Pos: Position{Line: t.Line,
                Col: t.Col}}, nil
        case "def":
            return p.parseDefStmt()
        }
    }
    if tok.Kind == TokLBrace {
        return p.parseBlockStmt()
    }
    /* Typed varDecl heuristic: IDENT IDENT = expr. */
    if tok.Kind == TokIdent &&
        p.lookAhead(1).Kind == TokIdent &&
        p.lookAhead(2).Kind == TokOperator &&
        p.lookAhead(2).Value == "=" {
        return p.parseTypedVarDecl()
    }
    return p.parseExprOrAssignStmt()
}

func (p *scriptParser) parseBlockStmt() (Stmt, error) {
    openTok := p.advance() /* '{' */
    body, err := p.parseBlock()
    if err != nil {
        return nil, err
    }
    return &BlockStmt{Body: body,
        Pos: Position{Line: openTok.Line,
            Col: openTok.Col}}, nil
}

/* parseBlock parses statements up to the matching '}'. Caller
 * has consumed the opening '{'. */
func (p *scriptParser) parseBlock() (*Block, error) {
    block := &Block{Pos: Position{Line: p.peek().Line,
        Col: p.peek().Col}}
    for {
        p.skipNL()
        if p.peek().Kind == TokRBrace {
            p.advance()
            return block, nil
        }
        if p.atEOF() {
            return nil, p.errorf("unterminated block")
        }
        st, err := p.parseStmt()
        if err != nil {
            return nil, err
        }
        if st != nil {
            block.Statements = append(block.Statements, st)
        }
    }
}

/* parseFuncDecl parses `def NAME (params) { body }`. The
 * cursor is on 'def' and we know peek+1=IDENT, peek+2=LParen
 * (verified by parseTopStmt).
 */
func (p *scriptParser) parseFuncDecl(
    annotations []string) (*FuncDecl, error) {
    head := p.advance() /* 'def' */
    nameTok := p.advance()
    p.advance() /* '(' */
    p.enterBracket()
    p.skipWS()
    params, err := p.parseParamList()
    if err != nil {
        return nil, err
    }
    p.skipWS()
    if p.peek().Kind != TokRParen {
        return nil, p.errorf("expected ) closing param list")
    }
    p.advance()
    p.exitBracket()
    p.skipNL()
    if p.peek().Kind != TokLBrace {
        return nil, p.errorf(
            "expected { opening function body")
    }
    p.advance()
    body, err := p.parseBlock()
    if err != nil {
        return nil, err
    }
    return &FuncDecl{
        Name:        nameTok.Value,
        Params:      params,
        Body:        body,
        Annotations: annotations,
        Pos:         Position{Line: head.Line, Col: head.Col},
    }, nil
}

/* parseParamList parses zero or more comma-separated params
 * inside (). The caller has consumed '(' and entered the
 * bracket; this function does NOT consume ')'.
 */
func (p *scriptParser) parseParamList() ([]Param, error) {
    var params []Param
    p.skipWS()
    if p.peek().Kind == TokRParen {
        return params, nil
    }
    for {
        param, err := p.parseParam()
        if err != nil {
            return nil, err
        }
        params = append(params, param)
        p.skipWS()
        if p.peek().Kind == TokComma {
            p.advance()
            p.skipWS()
            continue
        }
        return params, nil
    }
}

/* parseParam parses `[Type] Name [= default]`. */
func (p *scriptParser) parseParam() (Param, error) {
    first := p.peek()
    if first.Kind != TokIdent {
        return Param{}, p.errorf("expected parameter name")
    }
    p.advance()
    /* If followed by another ident, the first was a type. */
    var typ, name string
    if p.peek().Kind == TokIdent {
        typ = first.Value
        name = p.advance().Value
    } else {
        name = first.Value
    }
    var def Expr
    if p.peek().Kind == TokOperator && p.peek().Value == "=" {
        p.advance()
        e, err := p.parseExpr()
        if err != nil {
            return Param{}, err
        }
        def = e
    }
    return Param{Type: typ, Name: name, Default: def}, nil
}

/* parseDefStmt handles `def NAME [= expr]` at statement level.
 * `def NAME (` was already routed to parseFuncDecl by
 * parseTopStmt; inside non-top contexts `def NAME (` is also a
 * funcDecl (Groovy allows nested function declarations).
 */
func (p *scriptParser) parseDefStmt() (Stmt, error) {
    if p.lookAhead(1).Kind == TokIdent &&
        p.lookAhead(2).Kind == TokLParen {
        return p.parseFuncDecl(nil)
    }
    head := p.advance() /* 'def' */
    nameTok := p.peek()
    if nameTok.Kind != TokIdent {
        return nil, p.errorf("expected name after 'def'")
    }
    p.advance()
    decl := &VarDecl{Type: "", Name: nameTok.Value,
        Pos: Position{Line: head.Line, Col: head.Col}}
    if p.peek().Kind == TokOperator && p.peek().Value == "=" {
        p.advance()
        v, err := p.parseExpr()
        if err != nil {
            return nil, err
        }
        decl.Init = v
    }
    return decl, nil
}

/* parseTypedVarDecl parses `Type NAME = expr`. Caller has
 * verified the IDENT IDENT = lookahead.
 */
func (p *scriptParser) parseTypedVarDecl() (Stmt, error) {
    typTok := p.advance() /* Type ident */
    nameTok := p.advance()
    /* '=' */
    p.advance()
    v, err := p.parseExpr()
    if err != nil {
        return nil, err
    }
    return &VarDecl{
        Type: typTok.Value,
        Name: nameTok.Value,
        Init: v,
        Pos:  Position{Line: typTok.Line, Col: typTok.Col},
    }, nil
}

/* parseExprOrAssignStmt parses an expression and, if the result
 * is followed by a stmt-level assignment operator, wraps it as
 * an AssignStmt; otherwise as an ExprStmt. Also detects the
 * command-style invocation `IDENT [.member]* expr` where the
 * trailing expr is a single argument to the bare-name call.
 */
func (p *scriptParser) parseExprOrAssignStmt() (Stmt, error) {
    startTok := p.peek()
    /* Parse postfix (no operators), then decide. */
    lhs, err := p.parsePostfix()
    if err != nil {
        return nil, err
    }
    /* Assignment? */
    if p.peek().Kind == TokOperator {
        switch p.peek().Value {
        case "=", "+=", "-=":
            op := p.advance().Value
            rhs, err := p.parseExpr()
            if err != nil {
                return nil, err
            }
            return &AssignStmt{
                Target: lhs,
                Op:     op,
                Value:  rhs,
                Pos: Position{Line: startTok.Line,
                    Col: startTok.Col},
            }, nil
        }
    }
    /* Command-style invocation? Triggered when the postfix is
     * a bare name or member access (no parens applied) and the
     * next token starts an expression. */
    if isCommandHead(lhs) && startsExpr(p.peek()) &&
        p.bracketDepth == 0 {
        arg, err := p.parseExpr()
        if err != nil {
            return nil, err
        }
        return &ExprStmt{X: &CommandCallExpr{Fn: lhs, Arg: arg,
            Pos: lhs.ExprPos()},
            Pos: Position{Line: startTok.Line,
                Col: startTok.Col}}, nil
    }
    /* Otherwise: continue parsing the rest of the expression
     * (binary operators, ternary) and wrap as ExprStmt. */
    fullExpr, err := p.parseExprFrom(lhs)
    if err != nil {
        return nil, err
    }
    /* After the expression, check once more for assignment
     * (covers cases where the LHS was a more complex
     * subexpression). */
    if p.peek().Kind == TokOperator {
        switch p.peek().Value {
        case "=", "+=", "-=":
            op := p.advance().Value
            rhs, err := p.parseExpr()
            if err != nil {
                return nil, err
            }
            return &AssignStmt{
                Target: fullExpr,
                Op:     op,
                Value:  rhs,
                Pos: Position{Line: startTok.Line,
                    Col: startTok.Col},
            }, nil
        }
    }
    return &ExprStmt{X: fullExpr,
        Pos: Position{Line: startTok.Line,
            Col: startTok.Col}}, nil
}

/* isCommandHead reports whether e is a target the command-style
 * invocation rule can fire on. Bare idents and member-access
 * chains qualify; CallExpr / SubscriptExpr / literals do not.
 */
func isCommandHead(e Expr) bool {
    switch e.(type) {
    case *IdentExpr, *MemberExpr:
        return true
    }
    return false
}

/* startsExpr reports whether tok could begin a primary
 * expression. Used by command-style detection.
 */
func startsExpr(tok Token) bool {
    switch tok.Kind {
    case TokNumber, TokString, TokIdent, TokLBracket,
        TokLBrace, TokLParen:
        return true
    case TokKeyword:
        switch tok.Value {
        case "true", "false", "null", "new":
            return true
        }
    case TokOperator:
        switch tok.Value {
        case "!", "-":
            return true
        }
    }
    return false
}

/* ----- control-flow statements ------------------------------ */

func (p *scriptParser) parseIf() (Stmt, error) {
    head := p.advance() /* 'if' */
    if p.peek().Kind != TokLParen {
        return nil, p.errorf("expected ( after 'if'")
    }
    p.advance()
    p.enterBracket()
    p.skipWS()
    cond, err := p.parseExpr()
    if err != nil {
        return nil, err
    }
    p.skipWS()
    if p.peek().Kind != TokRParen {
        return nil, p.errorf("expected ) closing if condition")
    }
    p.advance()
    p.exitBracket()
    p.skipNL()
    thenStmt, err := p.parseStmtOrBlock()
    if err != nil {
        return nil, err
    }
    /* Look for else, possibly after a newline. */
    saved := p.pos
    p.skipNL()
    var elseStmt Stmt
    if p.peek().Kind == TokKeyword && p.peek().Value == "else" {
        p.advance()
        p.skipNL()
        elseStmt, err = p.parseStmtOrBlock()
        if err != nil {
            return nil, err
        }
    } else {
        p.pos = saved
    }
    return &IfStmt{Cond: cond, Then: thenStmt, Else: elseStmt,
        Pos: Position{Line: head.Line, Col: head.Col}}, nil
}

/* parseStmtOrBlock returns either a BlockStmt (if the next
 * token is '{') or a single statement. Used by if/for/while.
 */
func (p *scriptParser) parseStmtOrBlock() (Stmt, error) {
    if p.peek().Kind == TokLBrace {
        return p.parseBlockStmt()
    }
    return p.parseStmt()
}

func (p *scriptParser) parseFor() (Stmt, error) {
    head := p.advance() /* 'for' */
    if p.peek().Kind != TokLParen {
        return nil, p.errorf("expected ( after 'for'")
    }
    p.advance()
    p.enterBracket()
    p.skipWS()
    /* Decide for-in vs C-style by peeking IDENT then 'in'. */
    if p.peek().Kind == TokIdent &&
        p.lookAhead(1).Kind == TokKeyword &&
        p.lookAhead(1).Value == "in" {
        varTok := p.advance()
        p.advance() /* 'in' */
        p.skipWS()
        iter, err := p.parseExpr()
        if err != nil {
            return nil, err
        }
        p.skipWS()
        if p.peek().Kind != TokRParen {
            return nil, p.errorf(
                "expected ) closing for-in header")
        }
        p.advance()
        p.exitBracket()
        p.skipNL()
        body, err := p.parseStmtOrBlock()
        if err != nil {
            return nil, err
        }
        return &ForInStmt{VarName: varTok.Value, Iter: iter,
            Body: body,
            Pos: Position{Line: head.Line,
                Col: head.Col}}, nil
    }
    /* C-style: init; cond; step. */
    var initStmt Stmt
    if p.peek().Kind != TokSemicolon {
        s, err := p.parseStmt()
        if err != nil {
            return nil, err
        }
        initStmt = s
    }
    if p.peek().Kind != TokSemicolon {
        return nil, p.errorf("expected ; after for init")
    }
    p.advance()
    p.skipWS()
    var cond Expr
    if p.peek().Kind != TokSemicolon {
        e, err := p.parseExpr()
        if err != nil {
            return nil, err
        }
        cond = e
    }
    if p.peek().Kind != TokSemicolon {
        return nil, p.errorf("expected ; after for cond")
    }
    p.advance()
    p.skipWS()
    var step Expr
    if p.peek().Kind != TokRParen {
        e, err := p.parseExpr()
        if err != nil {
            return nil, err
        }
        step = e
    }
    if p.peek().Kind != TokRParen {
        return nil, p.errorf(
            "expected ) closing for header")
    }
    p.advance()
    p.exitBracket()
    p.skipNL()
    body, err := p.parseStmtOrBlock()
    if err != nil {
        return nil, err
    }
    return &ForCStmt{Init: initStmt, Cond: cond, Step: step,
        Body: body,
        Pos: Position{Line: head.Line, Col: head.Col}}, nil
}

func (p *scriptParser) parseWhile() (Stmt, error) {
    head := p.advance() /* 'while' */
    if p.peek().Kind != TokLParen {
        return nil, p.errorf("expected ( after 'while'")
    }
    p.advance()
    p.enterBracket()
    p.skipWS()
    cond, err := p.parseExpr()
    if err != nil {
        return nil, err
    }
    p.skipWS()
    if p.peek().Kind != TokRParen {
        return nil, p.errorf(
            "expected ) closing while condition")
    }
    p.advance()
    p.exitBracket()
    p.skipNL()
    body, err := p.parseStmtOrBlock()
    if err != nil {
        return nil, err
    }
    return &WhileStmt{Cond: cond, Body: body,
        Pos: Position{Line: head.Line, Col: head.Col}}, nil
}

func (p *scriptParser) parseTry() (Stmt, error) {
    head := p.advance() /* 'try' */
    p.skipNL()
    if p.peek().Kind != TokLBrace {
        return nil, p.errorf("expected { after 'try'")
    }
    p.advance()
    body, err := p.parseBlock()
    if err != nil {
        return nil, err
    }
    tryStmt := &TryStmt{Body: body,
        Pos: Position{Line: head.Line, Col: head.Col}}
    for {
        saved := p.pos
        p.skipNL()
        if p.peek().Kind != TokKeyword {
            p.pos = saved
            break
        }
        if p.peek().Value == "catch" {
            cc, err := p.parseCatch()
            if err != nil {
                return nil, err
            }
            tryStmt.Catches = append(tryStmt.Catches, cc)
            continue
        }
        if p.peek().Value == "finally" {
            p.advance()
            p.skipNL()
            if p.peek().Kind != TokLBrace {
                return nil, p.errorf(
                    "expected { after 'finally'")
            }
            p.advance()
            fin, err := p.parseBlock()
            if err != nil {
                return nil, err
            }
            tryStmt.Finally = fin
            break
        }
        p.pos = saved
        break
    }
    return tryStmt, nil
}

func (p *scriptParser) parseCatch() (CatchClause, error) {
    head := p.advance() /* 'catch' */
    if p.peek().Kind != TokLParen {
        return CatchClause{}, p.errorf(
            "expected ( after 'catch'")
    }
    p.advance()
    p.enterBracket()
    p.skipWS()
    first := p.peek()
    if first.Kind != TokIdent {
        return CatchClause{}, p.errorf(
            "expected catch parameter")
    }
    p.advance()
    var typ, name string
    if p.peek().Kind == TokIdent {
        typ = first.Value
        name = p.advance().Value
    } else {
        name = first.Value
    }
    p.skipWS()
    if p.peek().Kind != TokRParen {
        return CatchClause{}, p.errorf(
            "expected ) closing catch parameter")
    }
    p.advance()
    p.exitBracket()
    p.skipNL()
    if p.peek().Kind != TokLBrace {
        return CatchClause{}, p.errorf(
            "expected { after catch (...)")
    }
    p.advance()
    body, err := p.parseBlock()
    if err != nil {
        return CatchClause{}, err
    }
    return CatchClause{ParamType: typ, ParamName: name,
        Body: body,
        Pos:  Position{Line: head.Line, Col: head.Col}}, nil
}

func (p *scriptParser) parseReturn() (Stmt, error) {
    head := p.advance() /* 'return' */
    rs := &ReturnStmt{Pos: Position{Line: head.Line,
        Col: head.Col}}
    /* If the next token starts an expression, parse it. Else
     * the return has no value (return; return\n). */
    if !isStmtEnd(p.peek()) && startsExpr(p.peek()) {
        v, err := p.parseExpr()
        if err != nil {
            return nil, err
        }
        rs.Value = v
    }
    return rs, nil
}

func (p *scriptParser) parseThrow() (Stmt, error) {
    head := p.advance() /* 'throw' */
    v, err := p.parseExpr()
    if err != nil {
        return nil, err
    }
    return &ThrowStmt{Value: v,
        Pos: Position{Line: head.Line, Col: head.Col}}, nil
}

/* ----- expressions ------------------------------------------ */

/* parseExpr parses an expression with full precedence. */
func (p *scriptParser) parseExpr() (Expr, error) {
    lhs, err := p.parsePostfix()
    if err != nil {
        return nil, err
    }
    return p.parseExprFrom(lhs)
}

/* parseExprFrom continues an expression starting from a
 * pre-parsed postfix LHS. Used by parseExprOrAssignStmt to
 * peek at the LHS for assignment / command-style before
 * climbing operators.
 */
func (p *scriptParser) parseExprFrom(lhs Expr) (Expr, error) {
    return p.parseTernary(lhs)
}

func (p *scriptParser) parseTernary(lhs Expr) (Expr, error) {
    cond, err := p.parseLogicalOr(lhs)
    if err != nil {
        return nil, err
    }
    if p.peek().Kind == TokOperator && p.peek().Value == "?" {
        p.advance()
        p.skipWS()
        thenExpr, err := p.parseExpr()
        if err != nil {
            return nil, err
        }
        p.skipWS()
        if p.peek().Kind != TokOperator ||
            p.peek().Value != ":" {
            return nil, p.errorf(
                "expected : in ternary expression")
        }
        p.advance()
        p.skipWS()
        elseExpr, err := p.parseExpr()
        if err != nil {
            return nil, err
        }
        return &TernaryExpr{Cond: cond, Then: thenExpr,
            Else: elseExpr, Pos: cond.ExprPos()}, nil
    }
    return cond, nil
}

func (p *scriptParser) parseLogicalOr(lhs Expr) (Expr, error) {
    e, err := p.parseLogicalAnd(lhs)
    if err != nil {
        return nil, err
    }
    for p.peek().Kind == TokOperator &&
        p.peek().Value == "||" {
        p.advance()
        p.skipWS()
        rhsP, err := p.parsePostfix()
        if err != nil {
            return nil, err
        }
        rhs, err := p.parseLogicalAnd(rhsP)
        if err != nil {
            return nil, err
        }
        e = &BinaryExpr{Op: "||", Lhs: e, Rhs: rhs,
            Pos: e.ExprPos()}
    }
    return e, nil
}

func (p *scriptParser) parseLogicalAnd(lhs Expr) (Expr, error) {
    e, err := p.parseEquality(lhs)
    if err != nil {
        return nil, err
    }
    for p.peek().Kind == TokOperator &&
        p.peek().Value == "&&" {
        p.advance()
        p.skipWS()
        rhsP, err := p.parsePostfix()
        if err != nil {
            return nil, err
        }
        rhs, err := p.parseEquality(rhsP)
        if err != nil {
            return nil, err
        }
        e = &BinaryExpr{Op: "&&", Lhs: e, Rhs: rhs,
            Pos: e.ExprPos()}
    }
    return e, nil
}

func (p *scriptParser) parseEquality(lhs Expr) (Expr, error) {
    e, err := p.parseRelational(lhs)
    if err != nil {
        return nil, err
    }
    for p.peek().Kind == TokOperator &&
        (p.peek().Value == "==" || p.peek().Value == "!=") {
        op := p.advance().Value
        p.skipWS()
        rhsP, err := p.parsePostfix()
        if err != nil {
            return nil, err
        }
        rhs, err := p.parseRelational(rhsP)
        if err != nil {
            return nil, err
        }
        e = &BinaryExpr{Op: op, Lhs: e, Rhs: rhs,
            Pos: e.ExprPos()}
    }
    return e, nil
}

func (p *scriptParser) parseRelational(lhs Expr) (Expr, error) {
    e, err := p.parseAdditive(lhs)
    if err != nil {
        return nil, err
    }
    for {
        tok := p.peek()
        if tok.Kind == TokOperator {
            switch tok.Value {
            case "<", ">", "<=", ">=":
                p.advance()
                p.skipWS()
                rhsP, err := p.parsePostfix()
                if err != nil {
                    return nil, err
                }
                rhs, err := p.parseAdditive(rhsP)
                if err != nil {
                    return nil, err
                }
                e = &BinaryExpr{Op: tok.Value, Lhs: e, Rhs: rhs,
                    Pos: e.ExprPos()}
                continue
            }
        }
        if tok.Kind == TokKeyword && tok.Value == "instanceof" {
            p.advance()
            p.skipWS()
            rhsP, err := p.parsePostfix()
            if err != nil {
                return nil, err
            }
            rhs, err := p.parseAdditive(rhsP)
            if err != nil {
                return nil, err
            }
            e = &BinaryExpr{Op: "instanceof", Lhs: e, Rhs: rhs,
                Pos: e.ExprPos()}
            continue
        }
        return e, nil
    }
}

func (p *scriptParser) parseAdditive(lhs Expr) (Expr, error) {
    e, err := p.parseMultiplicative(lhs)
    if err != nil {
        return nil, err
    }
    for p.peek().Kind == TokOperator &&
        (p.peek().Value == "+" || p.peek().Value == "-") {
        op := p.advance().Value
        p.skipWS()
        rhsP, err := p.parsePostfix()
        if err != nil {
            return nil, err
        }
        rhs, err := p.parseMultiplicative(rhsP)
        if err != nil {
            return nil, err
        }
        e = &BinaryExpr{Op: op, Lhs: e, Rhs: rhs,
            Pos: e.ExprPos()}
    }
    return e, nil
}

func (p *scriptParser) parseMultiplicative(
    lhs Expr) (Expr, error) {
    e := lhs
    for p.peek().Kind == TokOperator &&
        (p.peek().Value == "*" || p.peek().Value == "/") {
        op := p.advance().Value
        p.skipWS()
        rhsP, err := p.parsePostfix()
        if err != nil {
            return nil, err
        }
        e = &BinaryExpr{Op: op, Lhs: e, Rhs: rhsP,
            Pos: e.ExprPos()}
    }
    return e, nil
}

/* parsePostfix parses a unary-prefix application, a primary,
 * and any postfix operators (member access, call, subscript,
 * trailing closure).
 */
func (p *scriptParser) parsePostfix() (Expr, error) {
    /* Unary prefix: ! or - on the same line. */
    if p.peek().Kind == TokOperator &&
        (p.peek().Value == "!" || p.peek().Value == "-") {
        opTok := p.advance()
        x, err := p.parsePostfix()
        if err != nil {
            return nil, err
        }
        return &UnaryExpr{Op: opTok.Value, X: x,
            Pos: Position{Line: opTok.Line,
                Col: opTok.Col}}, nil
    }
    primary, err := p.parsePrimary()
    if err != nil {
        return nil, err
    }
    return p.parsePostfixOps(primary)
}

/* parsePostfixOps applies any sequence of .ident, (args),
 * [index], or trailing closure on top of the supplied base
 * expression.
 */
func (p *scriptParser) parsePostfixOps(
    base Expr) (Expr, error) {
    for {
        tok := p.peek()
        if tok.Kind == TokOperator && tok.Value == "." {
            p.advance()
            p.skipWS()
            name := p.peek()
            if name.Kind != TokIdent &&
                name.Kind != TokKeyword {
                return nil, p.errorf(
                    "expected identifier after .")
            }
            p.advance()
            member := &MemberExpr{Object: base,
                Name: name.Value,
                Pos:  base.ExprPos()}
            base = member
            continue
        }
        if tok.Kind == TokLParen {
            opener := p.advance()
            p.enterBracket()
            p.skipWS()
            args, err := p.parseArgList()
            if err != nil {
                return nil, err
            }
            p.skipWS()
            if p.peek().Kind != TokRParen {
                return nil, p.errorf("expected ) closing call")
            }
            p.advance()
            p.exitBracket()
            call := &CallExpr{Fn: base, Args: args,
                Pos: Position{Line: opener.Line,
                    Col: opener.Col}}
            base = call
            /* Trailing closure on call: f(...) { ... }. */
            saved := p.pos
            if p.bracketDepth > 0 {
                p.skipWS()
            }
            if p.peek().Kind == TokLBrace {
                cl, err := p.parseClosureOrFail()
                if err != nil {
                    return nil, err
                }
                call.ClosureArg = cl
            } else {
                p.pos = saved
            }
            continue
        }
        if tok.Kind == TokLBracket {
            opener := p.advance()
            p.enterBracket()
            p.skipWS()
            idx, err := p.parseExpr()
            if err != nil {
                return nil, err
            }
            p.skipWS()
            if p.peek().Kind != TokRBracket {
                return nil, p.errorf(
                    "expected ] closing subscript")
            }
            p.advance()
            p.exitBracket()
            base = &SubscriptExpr{Object: base, Index: idx,
                Pos: Position{Line: opener.Line,
                    Col: opener.Col}}
            continue
        }
        if tok.Kind == TokLBrace {
            /* Trailing closure on a bare name / member access:
             * xs.each { ... }. Only fire when base is an ident
             * or member expr - otherwise the brace is some
             * other context (block-stmt) handled by the
             * statement parser.
             */
            if _, ok := base.(*IdentExpr); !ok {
                if _, ok2 := base.(*MemberExpr); !ok2 {
                    return base, nil
                }
            }
            cl, err := p.parseClosureOrFail()
            if err != nil {
                return nil, err
            }
            base = &CallExpr{Fn: base, Args: nil,
                ClosureArg: cl, Pos: base.ExprPos()}
            continue
        }
        return base, nil
    }
}

/* parseArgList parses zero or more comma-separated arguments
 * for a CallExpr. Each arg is either positional (an expr) or
 * labelled (IDENT ":" expr). The caller has consumed '(' and
 * entered the bracket; this does NOT consume ')'.
 */
func (p *scriptParser) parseArgList() ([]CallArg, error) {
    var args []CallArg
    p.skipWS()
    if p.peek().Kind == TokRParen {
        return args, nil
    }
    for {
        arg, err := p.parseOneArg()
        if err != nil {
            return nil, err
        }
        args = append(args, arg)
        p.skipWS()
        if p.peek().Kind == TokComma {
            p.advance()
            p.skipWS()
            continue
        }
        return args, nil
    }
}

func (p *scriptParser) parseOneArg() (CallArg, error) {
    /* Labelled arg: IDENT ":" expr. */
    if p.peek().Kind == TokIdent &&
        p.lookAhead(1).Kind == TokOperator &&
        p.lookAhead(1).Value == ":" {
        nameTok := p.advance()
        p.advance() /* ':' */
        p.skipWS()
        v, err := p.parseExpr()
        if err != nil {
            return CallArg{}, err
        }
        return CallArg{Name: nameTok.Value, Value: v}, nil
    }
    v, err := p.parseExpr()
    if err != nil {
        return CallArg{}, err
    }
    return CallArg{Value: v}, nil
}

/* ----- primary expressions ---------------------------------- */

func (p *scriptParser) parsePrimary() (Expr, error) {
    p.skipWS()
    tok := p.peek()
    switch tok.Kind {
    case TokNumber:
        p.advance()
        return &NumberLit{Value: tok.Value,
            Pos: Position{Line: tok.Line, Col: tok.Col}}, nil
    case TokString:
        p.advance()
        return &StringLit{Value: tok.Value,
            Kind: tok.StringKind,
            Pos:  Position{Line: tok.Line, Col: tok.Col}}, nil
    case TokKeyword:
        switch tok.Value {
        case "true":
            p.advance()
            return &BoolLit{Value: true,
                Pos: Position{Line: tok.Line,
                    Col: tok.Col}}, nil
        case "false":
            p.advance()
            return &BoolLit{Value: false,
                Pos: Position{Line: tok.Line,
                    Col: tok.Col}}, nil
        case "null":
            p.advance()
            return &NullLit{Pos: Position{Line: tok.Line,
                Col: tok.Col}}, nil
        case "new":
            return p.parseNew()
        }
        /* Other keywords (e.g. when used as identifiers in
         * member access) are permitted via the ident path. */
        p.advance()
        return &IdentExpr{Name: tok.Value,
            Pos: Position{Line: tok.Line, Col: tok.Col}}, nil
    case TokIdent:
        p.advance()
        return &IdentExpr{Name: tok.Value,
            Pos: Position{Line: tok.Line, Col: tok.Col}}, nil
    case TokLParen:
        p.advance()
        p.enterBracket()
        p.skipWS()
        e, err := p.parseExpr()
        if err != nil {
            return nil, err
        }
        p.skipWS()
        if p.peek().Kind != TokRParen {
            return nil, p.errorf(
                "expected ) closing parenthesised expr")
        }
        p.advance()
        p.exitBracket()
        return e, nil
    case TokLBracket:
        return p.parseListOrMap()
    case TokLBrace:
        return p.parseClosure()
    }
    return nil, p.errorf("unexpected token in expression")
}

func (p *scriptParser) parseNew() (Expr, error) {
    head := p.advance() /* 'new' */
    nameTok := p.peek()
    if nameTok.Kind != TokIdent {
        return nil, p.errorf("expected type name after 'new'")
    }
    p.advance()
    if p.peek().Kind != TokLParen {
        return nil, p.errorf("expected ( after new TypeName")
    }
    p.advance()
    p.enterBracket()
    p.skipWS()
    args, err := p.parseArgList()
    if err != nil {
        return nil, err
    }
    p.skipWS()
    if p.peek().Kind != TokRParen {
        return nil, p.errorf("expected ) closing new(...)")
    }
    p.advance()
    p.exitBracket()
    return &NewExpr{Type: nameTok.Value, Args: args,
        Pos: Position{Line: head.Line, Col: head.Col}}, nil
}

/* parseListOrMap disambiguates between [a, b, c], [k: v, ...],
 * and [:] (empty map). The cursor is on the opening '['.
 */
func (p *scriptParser) parseListOrMap() (Expr, error) {
    opener := p.advance() /* '[' */
    p.enterBracket()
    p.skipWS()
    pos := Position{Line: opener.Line, Col: opener.Col}
    /* Empty list. */
    if p.peek().Kind == TokRBracket {
        p.advance()
        p.exitBracket()
        return &ListExpr{Pos: pos}, nil
    }
    /* Empty map: [:]. */
    if p.peek().Kind == TokOperator && p.peek().Value == ":" {
        p.advance()
        p.skipWS()
        if p.peek().Kind != TokRBracket {
            return nil, p.errorf(
                "expected ] after [: empty map marker")
        }
        p.advance()
        p.exitBracket()
        return &MapExpr{Pos: pos}, nil
    }
    /* Peek to decide map vs list. A map starts with KEY ":" .
     * KEY may be IDENT, STRING, or NUMBER. */
    if isMapKey(p.peek()) &&
        p.lookAhead(1).Kind == TokOperator &&
        p.lookAhead(1).Value == ":" {
        return p.parseMapBody(pos)
    }
    return p.parseListBody(pos)
}

func isMapKey(tok Token) bool {
    return tok.Kind == TokIdent || tok.Kind == TokString ||
        tok.Kind == TokNumber || tok.Kind == TokKeyword
}

func (p *scriptParser) parseListBody(
    pos Position) (*ListExpr, error) {
    list := &ListExpr{Pos: pos}
    for {
        p.skipWS()
        e, err := p.parseExpr()
        if err != nil {
            return nil, err
        }
        list.Elements = append(list.Elements, e)
        p.skipWS()
        if p.peek().Kind == TokComma {
            p.advance()
            p.skipWS()
            /* Allow a trailing comma before ']'. */
            if p.peek().Kind == TokRBracket {
                p.advance()
                p.exitBracket()
                return list, nil
            }
            continue
        }
        if p.peek().Kind == TokRBracket {
            p.advance()
            p.exitBracket()
            return list, nil
        }
        return nil, p.errorf("expected , or ] in list literal")
    }
}

func (p *scriptParser) parseMapBody(
    pos Position) (*MapExpr, error) {
    m := &MapExpr{Pos: pos}
    for {
        p.skipWS()
        keyTok := p.peek()
        if !isMapKey(keyTok) {
            return nil, p.errorf("expected map key")
        }
        p.advance()
        var key Expr
        switch keyTok.Kind {
        case TokString:
            key = &StringLit{Value: keyTok.Value,
                Kind: keyTok.StringKind,
                Pos: Position{Line: keyTok.Line,
                    Col: keyTok.Col}}
        case TokNumber:
            key = &NumberLit{Value: keyTok.Value,
                Pos: Position{Line: keyTok.Line,
                    Col: keyTok.Col}}
        default:
            /* Treat ident/keyword keys as bare ident expressions
             * even when the spelling matches a keyword like
             * "true" - the Groovy convention is that map keys
             * named after keywords are valid. */
            key = &IdentExpr{Name: keyTok.Value,
                Pos: Position{Line: keyTok.Line,
                    Col: keyTok.Col}}
        }
        if p.peek().Kind != TokOperator || p.peek().Value != ":" {
            return nil, p.errorf("expected : after map key")
        }
        p.advance()
        p.skipWS()
        v, err := p.parseExpr()
        if err != nil {
            return nil, err
        }
        m.Entries = append(m.Entries, MapEntry{Key: key,
            Value: v})
        p.skipWS()
        if p.peek().Kind == TokComma {
            p.advance()
            continue
        }
        if p.peek().Kind == TokRBracket {
            p.advance()
            p.exitBracket()
            return m, nil
        }
        return nil, p.errorf("expected , or ] in map literal")
    }
}

/* parseClosure parses `{ [params ->] body }`. The cursor is on
 * the opening '{'. */
func (p *scriptParser) parseClosure() (*ClosureExpr, error) {
    return p.parseClosureOrFail()
}

func (p *scriptParser) parseClosureOrFail() (*ClosureExpr,
    error) {
    opener := p.advance() /* '{' */
    cl := &ClosureExpr{Pos: Position{Line: opener.Line,
        Col: opener.Col}}
    /* Look ahead for closure params: IDENT (',' IDENT)* '->'.
     * If the lookahead does not end in '->', the brace contents
     * are the body directly. */
    if p.hasClosureParams() {
        params, err := p.parseClosureParams()
        if err != nil {
            return nil, err
        }
        cl.Params = params
    }
    body, err := p.parseBlock()
    if err != nil {
        return nil, err
    }
    cl.Body = body
    return cl, nil
}

/* hasClosureParams looks ahead from the current cursor to see
 * if the next tokens are IDENT (, IDENT)* '->' before any
 * statement-terminator at depth 0. Returns true if so.
 */
func (p *scriptParser) hasClosureParams() bool {
    saved := p.pos
    defer func() { p.pos = saved }()
    p.skipNL()
    if p.peek().Kind == TokOperator && p.peek().Value == "->" {
        return true
    }
    /* Need at least one ident. */
    if p.peek().Kind != TokIdent {
        return false
    }
    p.advance()
    /* Optionally typed: IDENT IDENT. */
    if p.peek().Kind == TokIdent {
        p.advance()
    }
    for p.peek().Kind == TokComma {
        p.advance()
        p.skipNL()
        if p.peek().Kind != TokIdent {
            return false
        }
        p.advance()
        if p.peek().Kind == TokIdent {
            p.advance()
        }
    }
    p.skipNL()
    return p.peek().Kind == TokOperator &&
        p.peek().Value == "->"
}

func (p *scriptParser) parseClosureParams() ([]Param, error) {
    p.skipNL()
    if p.peek().Kind == TokOperator && p.peek().Value == "->" {
        p.advance()
        return nil, nil
    }
    var params []Param
    for {
        param, err := p.parseParam()
        if err != nil {
            return nil, err
        }
        params = append(params, param)
        p.skipNL()
        if p.peek().Kind == TokComma {
            p.advance()
            p.skipNL()
            continue
        }
        break
    }
    if p.peek().Kind != TokOperator || p.peek().Value != "->" {
        return nil, p.errorf(
            "expected -> after closure params")
    }
    p.advance()
    return params, nil
}
