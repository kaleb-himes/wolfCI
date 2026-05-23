/* Package pipeline implements wolfCI's Groovy-subset pipeline
 * DSL: the lexer, the parser, and the interpreter that runs a
 * declarative-pipeline Jenkinsfile such as the master-job
 * PRB.Jenkinsfile under third_party/testing/Jenkins/.
 *
 * Phase 18.11 ships the lexer only. Tokenize takes Jenkinsfile
 * source bytes and returns a token stream the Phase 18.12-18.13
 * parsers consume.
 *
 * The lexer recognizes:
 *
 *   - Keywords: the declarative-pipeline + script-block subset
 *     listed in PLAN.md 18.11 (pipeline, agent, label, options,
 *     stages, stage, steps, script, parallel, when, expression,
 *     withCredentials, post, always, success, failure, def).
 *   - Operators: =, +, ., :, ?, &&, ||, !, +=
 *   - Strings: single 'x', double "x", triple-single '''x''',
 *     triple-double """x""". Strings are returned with their
 *     surrounding quotes stripped; the body is preserved
 *     verbatim including any $-interpolation markers, which
 *     the interpreter (18.14, 18.15) expands at evaluation
 *     time against its variable scope. Escape sequences inside
 *     strings are NOT expanded by the lexer.
 *   - Numbers: decimal integers only. Floats and hex literals
 *     are out of scope until a Jenkinsfile we need to run uses
 *     them.
 *   - Identifiers: ASCII letters, digits (not leading), and
 *     underscore. Matches Groovy's identifier rules closely
 *     enough for the subset wolfCI runs.
 *   - Comments: // line and slash-star block. Comment bytes are
 *     skipped; no comment token appears in the stream.
 *   - Punctuation: parens, braces, brackets, commas,
 *     semicolons, and newlines (some grammar rules in 18.12 are
 *     newline-sensitive so we keep newlines visible).
 *
 * Position tracking: every Token carries the 1-indexed (line,
 * col) of its first byte so parse errors can point at the
 * input.
 */
package pipeline

import (
    "fmt"
    "unicode"
)

/* TokenKind enumerates the lexer's token categories. */
type TokenKind int

const (
    TokIdent TokenKind = iota
    TokKeyword
    TokString
    TokNumber
    TokOperator
    TokLBrace
    TokRBrace
    TokLParen
    TokRParen
    TokLBracket
    TokRBracket
    TokComma
    TokSemicolon
    TokNewline
    TokAnnotation
    TokEOF
)

/* StringKind distinguishes the four string syntaxes the lexer
 * accepts. The parser/interpreter dispatches on this to decide
 * whether to run $-interpolation at eval time.
 */
type StringKind int

const (
    StringNotApplicable StringKind = iota
    StringSingle
    StringDouble
    StringTripleSingle
    StringTripleDouble
)

/* Token is one lexer output. Value is the raw text:
 *   - For TokIdent/TokKeyword: the identifier characters.
 *   - For TokString: the quote-stripped body (interpolation
 *     markers preserved).
 *   - For TokNumber: the digit run.
 *   - For TokOperator: the operator symbols.
 *   - For other token kinds: empty.
 * StringKind is valid only when Kind == TokString.
 */
type Token struct {
    Kind       TokenKind
    Value      string
    StringKind StringKind
    Line       int
    Col        int
}

/* keywords is the lookup table the lexer consults after
 * recognizing an identifier. The set matches the subset PLAN.md
 * 18.11 calls out; adding a keyword later is one line.
 */
var keywords = map[string]struct{}{
    "pipeline":        {},
    "agent":           {},
    "label":           {},
    "options":         {},
    "stages":          {},
    "stage":           {},
    "steps":           {},
    "script":          {},
    "parallel":        {},
    "when":            {},
    "expression":      {},
    "withCredentials": {},
    "post":            {},
    "always":          {},
    "success":         {},
    "failure":         {},
    "def":             {},
    /* Phase 18.13 script-subset keywords. The Groovy reserved
     * words the script-block parser needs to recognise as
     * statement / expression heads rather than identifiers.
     */
    "return":     {},
    "if":         {},
    "else":       {},
    "for":        {},
    "while":      {},
    "in":         {},
    "try":        {},
    "catch":      {},
    "finally":    {},
    "throw":      {},
    "break":      {},
    "continue":   {},
    "null":       {},
    "true":       {},
    "false":      {},
    "new":        {},
    "instanceof": {},
}

/* Tokenize converts src into a token stream terminated by a
 * single TokEOF. Returns an error on an unterminated string or
 * an unterminated block comment.
 */
func Tokenize(src []byte) ([]Token, error) {
    l := &lexer{
        src:  src,
        pos:  0,
        line: 1,
        col:  1,
    }
    var out []Token
    for {
        tok, err := l.next()
        if err != nil {
            return nil, err
        }
        out = append(out, tok)
        if tok.Kind == TokEOF {
            return out, nil
        }
    }
}

/* lexer is the scan-state machine. Methods advance pos and
 * keep line/col in sync. The single-character read path uses
 * peek/advance; the multi-character paths (strings, comments)
 * track the start position so the emitted token's (Line, Col)
 * points at the first byte of the lexeme.
 */
type lexer struct {
    src  []byte
    pos  int
    line int
    col  int
}

func (l *lexer) eof() bool {
    return l.pos >= len(l.src)
}

func (l *lexer) peek() byte {
    if l.eof() {
        return 0
    }
    return l.src[l.pos]
}

func (l *lexer) peekAt(offset int) byte {
    if l.pos+offset >= len(l.src) {
        return 0
    }
    return l.src[l.pos+offset]
}

/* advance consumes one byte and updates line/col. A newline
 * resets col to 1 and bumps line.
 */
func (l *lexer) advance() byte {
    if l.eof() {
        return 0
    }
    c := l.src[l.pos]
    l.pos++
    if c == '\n' {
        l.line++
        l.col = 1
    } else {
        l.col++
    }
    return c
}

/* skipSpaceAndComments consumes any run of whitespace bytes
 * (excluding newline, which is a token in this grammar) and
 * comments. Comments may span lines; an unterminated /* ... is
 * an error.
 */
func (l *lexer) skipSpaceAndComments() error {
    for !l.eof() {
        c := l.peek()
        if c == ' ' || c == '\t' || c == '\r' {
            l.advance()
            continue
        }
        if c == '/' && l.peekAt(1) == '/' {
            /* line comment runs to next newline (the newline
             * itself stays in the stream).
             */
            for !l.eof() && l.peek() != '\n' {
                l.advance()
            }
            continue
        }
        if c == '/' && l.peekAt(1) == '*' {
            startLine, startCol := l.line, l.col
            l.advance() /* '/' */
            l.advance() /* '*' */
            closed := false
            for !l.eof() {
                if l.peek() == '*' && l.peekAt(1) == '/' {
                    l.advance() /* '*' */
                    l.advance() /* '/' */
                    closed = true
                    break
                }
                l.advance()
            }
            if !closed {
                return fmt.Errorf(
                    "pipeline.Tokenize: unterminated block "+
                        "comment starting at %d:%d",
                    startLine, startCol)
            }
            continue
        }
        break
    }
    return nil
}

/* next returns the next non-skipped token, or an error on a
 * malformed input. Returns a TokEOF when src is exhausted.
 */
func (l *lexer) next() (Token, error) {
    if err := l.skipSpaceAndComments(); err != nil {
        return Token{}, err
    }
    if l.eof() {
        return Token{Kind: TokEOF, Line: l.line,
            Col: l.col}, nil
    }
    line, col := l.line, l.col
    c := l.peek()

    /* Newline is its own token kind so the parser can use it
     * to delimit statements where Groovy expects it (no
     * semicolon needed in most Groovy constructs).
     */
    if c == '\n' {
        l.advance()
        return Token{Kind: TokNewline, Line: line,
            Col: col}, nil
    }

    /* Identifiers / keywords. */
    if isIdentStart(c) {
        return l.readIdent(line, col), nil
    }

    /* Numbers. */
    if c >= '0' && c <= '9' {
        return l.readNumber(line, col), nil
    }

    /* Strings. The triple variants are detected by peeking
     * three quote chars in a row.
     */
    if c == '\'' || c == '"' {
        return l.readString(line, col)
    }

    /* Annotations. Groovy uses '@AnnotationName' to attach
     * metadata to a declaration (e.g. '@NonCPS'). The lexer
     * emits TokAnnotation with the bare name (no leading '@')
     * so the script-subset parser added in 18.13 can skip
     * past annotations without misreading their bytes.
     */
    if c == '@' && isIdentStart(l.peekAt(1)) {
        l.advance() /* '@' */
        return l.readAnnotation(line, col), nil
    }

    /* Punctuation + operators. */
    return l.readPunctOrOperator(line, col)
}

/* readAnnotation reads an identifier following an '@' marker.
 * The '@' has already been consumed; the returned token's
 * Value is the annotation name with no leading '@'.
 */
func (l *lexer) readAnnotation(line, col int) Token {
    start := l.pos
    for !l.eof() && isIdentPart(l.peek()) {
        l.advance()
    }
    return Token{Kind: TokAnnotation,
        Value: string(l.src[start:l.pos]),
        Line:  line, Col: col}
}

func isIdentStart(c byte) bool {
    return c == '_' ||
        (c >= 'a' && c <= 'z') ||
        (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
    return isIdentStart(c) ||
        (c >= '0' && c <= '9')
}

func (l *lexer) readIdent(line, col int) Token {
    start := l.pos
    for !l.eof() && isIdentPart(l.peek()) {
        l.advance()
    }
    text := string(l.src[start:l.pos])
    kind := TokIdent
    if _, ok := keywords[text]; ok {
        kind = TokKeyword
    }
    return Token{Kind: kind, Value: text,
        Line: line, Col: col}
}

func (l *lexer) readNumber(line, col int) Token {
    start := l.pos
    for !l.eof() && l.peek() >= '0' && l.peek() <= '9' {
        l.advance()
    }
    return Token{Kind: TokNumber,
        Value: string(l.src[start:l.pos]),
        Line:  line, Col: col}
}

/* readString handles all four string flavors. Triple variants
 * are detected by peeking three quote chars; a triple string
 * ends on the matching triple sequence. Single/double strings
 * end on the first matching unescaped quote.
 */
func (l *lexer) readString(line, col int) (Token, error) {
    quote := l.peek()
    /* triple? */
    if l.peekAt(1) == quote && l.peekAt(2) == quote {
        /* consume the three opening quotes */
        l.advance()
        l.advance()
        l.advance()
        return l.readTripleString(line, col, quote)
    }
    /* single-line string */
    l.advance() /* opening quote */
    start := l.pos
    for !l.eof() {
        c := l.peek()
        if c == '\\' {
            /* escape: skip the backslash and whatever follows.
             * The lexer keeps the bytes verbatim in Value; the
             * interpreter expands escapes at eval time.
             */
            l.advance()
            if !l.eof() {
                l.advance()
            }
            continue
        }
        if c == quote {
            value := string(l.src[start:l.pos])
            l.advance() /* closing quote */
            sk := StringSingle
            if quote == '"' {
                sk = StringDouble
            }
            return Token{Kind: TokString,
                Value:      value,
                StringKind: sk,
                Line:       line, Col: col}, nil
        }
        if c == '\n' {
            return Token{}, fmt.Errorf(
                "pipeline.Tokenize: newline in "+
                    "single-line string at %d:%d",
                line, col)
        }
        l.advance()
    }
    return Token{}, fmt.Errorf(
        "pipeline.Tokenize: unterminated string at %d:%d",
        line, col)
}

func (l *lexer) readTripleString(line, col int,
    quote byte) (Token, error) {
    start := l.pos
    for !l.eof() {
        if l.peek() == quote &&
            l.peekAt(1) == quote &&
            l.peekAt(2) == quote {
            value := string(l.src[start:l.pos])
            l.advance()
            l.advance()
            l.advance()
            sk := StringTripleSingle
            if quote == '"' {
                sk = StringTripleDouble
            }
            return Token{Kind: TokString,
                Value:      value,
                StringKind: sk,
                Line:       line, Col: col}, nil
        }
        l.advance()
    }
    return Token{}, fmt.Errorf(
        "pipeline.Tokenize: unterminated triple-quoted "+
            "string at %d:%d", line, col)
}

/* readPunctOrOperator dispatches the remaining symbol bytes. */
func (l *lexer) readPunctOrOperator(
    line, col int) (Token, error) {
    c := l.peek()
    switch c {
    case '{':
        l.advance()
        return Token{Kind: TokLBrace, Value: "{",
            Line: line, Col: col}, nil
    case '}':
        l.advance()
        return Token{Kind: TokRBrace, Value: "}",
            Line: line, Col: col}, nil
    case '(':
        l.advance()
        return Token{Kind: TokLParen, Value: "(",
            Line: line, Col: col}, nil
    case ')':
        l.advance()
        return Token{Kind: TokRParen, Value: ")",
            Line: line, Col: col}, nil
    case '[':
        l.advance()
        return Token{Kind: TokLBracket, Value: "[",
            Line: line, Col: col}, nil
    case ']':
        l.advance()
        return Token{Kind: TokRBracket, Value: "]",
            Line: line, Col: col}, nil
    case ',':
        l.advance()
        return Token{Kind: TokComma, Value: ",",
            Line: line, Col: col}, nil
    case ';':
        l.advance()
        return Token{Kind: TokSemicolon, Value: ";",
            Line: line, Col: col}, nil
    }

    /* Multi-char operators. The order matters: check the
     * longest prefixes first so e.g. "==" wins over a bare "="
     * and "->" wins over a bare "-".
     */
    type twoChar struct {
        a, b byte
        op   string
    }
    /* The PLAN.md 18.11 op list is a subset; the master-job
     * PRB.Jenkinsfile actually uses ->, ==, !=, <, ++ on top.
     * Add the full closure / comparison / increment set so the
     * lexer covers every Jenkinsfile under
     * third_party/testing/Jenkins without churn. - by itself
     * is reserved for future arithmetic; today it always pairs
     * with another - to form -- (decrement) or > to form -> .
     */
    twoChars := []twoChar{
        {'+', '=', "+="},
        {'-', '=', "-="},
        {'=', '=', "=="},
        {'!', '=', "!="},
        {'<', '=', "<="},
        {'>', '=', ">="},
        {'&', '&', "&&"},
        {'|', '|', "||"},
        {'+', '+', "++"},
        {'-', '-', "--"},
        {'-', '>', "->"},
    }
    for _, tc := range twoChars {
        if c == tc.a && l.peekAt(1) == tc.b {
            l.advance()
            l.advance()
            return Token{Kind: TokOperator, Value: tc.op,
                Line: line, Col: col}, nil
        }
    }

    /* Single-char operators. Includes the PLAN.md set
     * (= + . : ? !) plus the comparison ops < > the Groovy
     * subset needs for if/while bodies, plus * and / for
     * arithmetic (master-job does not use them but other
     * Jenkinsfiles will), plus - for unary minus and
     * subtraction.
     */
    switch c {
    case '=', '+', '-', '.', ':', '?', '!',
        '<', '>', '*', '/':
        l.advance()
        return Token{Kind: TokOperator, Value: string(c),
            Line: line, Col: col}, nil
    }

    /* Anything else is a lex error. Include the rune so the
     * error message is actionable (a stray emdash, smart quote,
     * etc. is the typical culprit).
     */
    return Token{}, fmt.Errorf(
        "pipeline.Tokenize: unexpected character %q at %d:%d",
        rune(c), line, col)
}

/* compile-time check: unicode is referenced so the import does
 * not become unused if we drop a check above. Kept for future
 * non-ASCII identifier support if a Jenkinsfile we need to run
 * uses one.
 */
var _ = unicode.IsLetter
