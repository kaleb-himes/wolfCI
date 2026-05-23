/* internal/pipeline/exec_script.go - PLAN.md 18.15.
 *
 * Tree-walking interpreter for the script-subset AST built by
 * parser_script.go. The declarative interpreter (18.14) gains
 * a `script` step dispatcher (in exec_declarative.go) that
 * parses a step's captured Block tokens with
 * ParseScriptTokens, then hands the AST to evalScriptBlock
 * with a fresh scriptRuntime.
 *
 * Native step surface for 18.15:
 *   echo <arg>           append a formatted message to the
 *                        step's output buffer.
 *   parallel <map>       run each map value (a closure) in
 *                        its own goroutine; aggregate results
 *                        so a single failing branch fails the
 *                        overall step while the other branches
 *                        still complete.
 *
 * Subsequent step-library phases (18.16-18.24) register more
 * native functions on the runtime; the dispatch pattern is the
 * same.
 *
 * Control flow is plumbed through Go's error return: special
 * error types (retSignal, brSignal, contSignal, throwSignal)
 * model return / break / continue / throw. The block-walking
 * loop checks the error type before treating it as a real
 * failure. throw inside a parallel branch propagates to the
 * parallel native function, which surfaces it as a throwSignal
 * back into the script body; the script step dispatcher then
 * downgrades the signal to BuildFailure so the build's error
 * channel stays reserved for infrastructure-level failures.
 *
 * Scope handling: every closure call creates a child sEnv on
 * top of the closure's captured env, so two parallel branches
 * never share a mutable scope. The runtime carries only
 * goroutine-safe shared state (executor reference, echo
 * buffer + mutex). Naked variable assignments resolve up the
 * env chain and write at the binding's defining scope; `def`
 * always defines in the current scope.
 */
package pipeline

import (
    "context"
    "fmt"
    "regexp"
    "strconv"
    "strings"
    "sync"

    "github.com/kaleb-himes/wolfCI/internal/credstore"
)

/* ----- value model ------------------------------------------ */

type scriptValue interface {
    sv()
}

type sNull struct{}

func (*sNull) sv() {}

type sBool struct{ v bool }

func (*sBool) sv() {}

type sNum struct{ v int64 }

func (*sNum) sv() {}

type sStr struct{ v string }

func (*sStr) sv() {}

type sList struct {
    items []scriptValue
}

func (*sList) sv() {}

/* sMap preserves insertion order so parallel-style iteration
 * is deterministic (same as Groovy's LinkedHashMap default).
 */
type sMap struct {
    keys   []string
    values map[string]scriptValue
}

func newMap() *sMap {
    return &sMap{values: map[string]scriptValue{}}
}

func (m *sMap) set(key string, v scriptValue) {
    if _, ok := m.values[key]; !ok {
        m.keys = append(m.keys, key)
    }
    m.values[key] = v
}

func (*sMap) sv() {}

type sClosure struct {
    params []Param
    body   *Block
    env    *sEnv
}

func (*sClosure) sv() {}

type sNative struct {
    name string
    fn   nativeFn
}

func (*sNative) sv() {}

/* sExcept models Groovy's Exception sub-tree for our subset.
 * The 18.13 parser emits NewExpr only for the constructor
 * form `new TypeName(args)`, so the runtime collapses every
 * constructor down to (Type, Message) - the message is the
 * first string arg if present. Richer exception types
 * (matching by `instanceof` in a catch) are still
 * representable via the Type field.
 */
type sExcept struct {
    typ string
    msg string
}

func (*sExcept) sv() {}

type nativeFn func(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error)

/* ----- environment ------------------------------------------ */

type sEnv struct {
    parent *sEnv
    mu     sync.Mutex /* protects vars within this env's scope */
    vars   map[string]scriptValue
}

func newEnv(parent *sEnv) *sEnv {
    return &sEnv{parent: parent,
        vars: map[string]scriptValue{}}
}

/* lookup walks the env chain returning the binding's defining
 * scope (or nil if undefined). */
func (e *sEnv) lookup(name string) (*sEnv, scriptValue, bool) {
    for cur := e; cur != nil; cur = cur.parent {
        cur.mu.Lock()
        v, ok := cur.vars[name]
        cur.mu.Unlock()
        if ok {
            return cur, v, true
        }
    }
    return nil, nil, false
}

/* define always writes in the current env (used by def). */
func (e *sEnv) define(name string, v scriptValue) {
    e.mu.Lock()
    defer e.mu.Unlock()
    e.vars[name] = v
}

/* assign writes at the binding's defining scope when one
 * exists; otherwise it defines a new binding in the topmost
 * (root) env, matching Groovy's loose script-binding rule. */
func (e *sEnv) assign(name string, v scriptValue) {
    if defScope, _, ok := e.lookup(name); ok {
        defScope.mu.Lock()
        defScope.vars[name] = v
        defScope.mu.Unlock()
        return
    }
    /* Walk to root and define there. */
    cur := e
    for cur.parent != nil {
        cur = cur.parent
    }
    cur.define(name, v)
}

/* ----- runtime ---------------------------------------------- */

type scriptRuntime struct {
    executor Executor
    globals  *sEnv

    echoMu  sync.Mutex
    echoBuf []string

    /* lastExitCode tracks the most recent `sh` invocation's
     * exit code so the declarative dispatcher in execStep can
     * surface it on the StepRun even when sh threw on a
     * non-zero exit (no returnStatus). Mutex-free because sh
     * never runs in parallel branches in 18.16 - parallel
     * runs closures, and closures invoke sh sequentially
     * within their own goroutine; if a future step calls
     * Sh concurrently from multiple branches we'll widen this
     * to a per-goroutine carrier. */
    lastExitCode int

    /* workspace / stashDir / artifactsDir mirror the same
     * fields on LocalExecutor for the 18.17 workspace step
     * library. They're read by cleanWs / dir / stash /
     * unstash / archiveArtifacts. Empty when the caller
     * supplied a bare &LocalExecutor{} (the 18.14-18.16
     * tests do this); the workspace-touching natives error
     * out with an actionable message in that case. */
    workspace    string
    stashDir     string
    artifactsDir string

    /* creds is the credential-store handle the 18.18+
     * withCredentials step uses to unseal secrets at binding
     * time. nil when the caller didn't wire one. */
    creds *credstore.Store

    /* dispatcher is the build-step seam from 18.22 onward.
     * nil when the executor did not supply one; the build
     * native errors out with an actionable message in that
     * case. */
    dispatcher BuildDispatcher

    /* nodeRouter resolves a label to an Executor for the
     * 18.24+ node step. nil disables nested-node routing;
     * the native errors out with an actionable message in
     * that case. */
    nodeRouter NodeRouter

    /* builds backs the 18.25+ currentBuild / previousBuild
     * builtins. nil disables those globals. */
    builds BuildInfoProvider

    /* initialEnv carries the LocalExecutor.InitialEnv map
     * from construction. registerInitialEnv reads it after
     * registerNatives so the `env` global lands AFTER the
     * step natives but BEFORE the first script statement
     * runs. */
    initialEnv map[string]string

    /* catchForced{Build,Stage} carry the 18.23 catchError
     * "this step / stage / build should be marked X" verdict
     * out of the script runtime so execStep can apply it
     * after the step body completes. Default BuildRunning
     * means "no catchError fired"; any other value is the
     * highest-severity verdict any catchError in this step
     * recorded (currently SUCCESS or FAILURE). The pair is
     * separate so a future BuildUnstable / BuildAborted
     * split can distinguish stage-only from build-wide
     * downgrades. */
    catchForcedBuild BuildStatus
    catchForcedStage BuildStatus

    /* secretMu guards envExtra and masks, which
     * withCredentials pushes onto a stack for the duration
     * of a closure body and pops on exit. Pop is keyed on
     * the saved length recorded at push, so nested
     * withCredentials blocks restore in LIFO order. */
    secretMu sync.Mutex
    /* envExtra is the list of KEY=value entries that the
     * runtime passes to executor.Sh for the wrapped block.
     * Outside withCredentials it stays empty. */
    envExtra []string
    /* masks holds substrings that nativeSh redacts from any
     * captured shell output before forwarding it to the
     * echo buffer. Same lifetime as envExtra. */
    masks []string
}

func newScriptRuntime(executor Executor) *scriptRuntime {
    rt := &scriptRuntime{executor: executor}
    rt.globals = newEnv(nil)
    /* Inherit workspace info from the LocalExecutor concrete
     * type when present. A custom Executor implementation
     * outside this package can satisfy this by wrapping a
     * LocalExecutor; richer plumbing (e.g. a remote-agent
     * executor) lands when that executor exists. */
    if le, ok := executor.(*LocalExecutor); ok {
        rt.workspace = le.Workspace
        rt.stashDir = le.StashDir
        rt.artifactsDir = le.ArtifactsDir
        rt.creds = le.Creds
        rt.dispatcher = le.Dispatcher
        rt.nodeRouter = le.NodeRouter
        rt.builds = le.BuildInfo
        /* 18.30 InitialEnv: define `env` as an sMap so
         * Groovy code can read env.<KEY>, and stamp the
         * same map onto the runtime's envExtra stack so
         * every sh step inherits the values via shell
         * expansion. Stamped here (not on every step) so
         * the layering interacts cleanly with the 18.18+
         * pushSecrets stack: caller frames live above
         * InitialEnv on the stack. */
        if len(le.InitialEnv) > 0 {
            rt.initialEnv = make(map[string]string,
                len(le.InitialEnv))
            for k, v := range le.InitialEnv {
                rt.initialEnv[k] = v
            }
        }
    }
    /* Non-LocalExecutor implementations can opt into nested-
     * node routing by exposing the router through this
     * interface. Tests use it to wire a label->Executor map
     * onto a fake executor without dragging LocalExecutor's
     * /bin/sh implementation into the test surface. */
    if nrp, ok := executor.(interface {
        NodeRouter() NodeRouter
    }); ok && rt.nodeRouter == nil {
        rt.nodeRouter = nrp.NodeRouter()
    }
    /* Same opt-in pattern for the currentBuild builtin so
     * non-LocalExecutor fakes can wire a BuildInfoProvider
     * without dragging the rest of LocalExecutor with them. */
    if bp, ok := executor.(interface {
        BuildInfo() BuildInfoProvider
    }); ok && rt.builds == nil {
        rt.builds = bp.BuildInfo()
    }
    rt.registerNatives()
    return rt
}

/* snapshotEnv returns a copy of the runtime's current
 * extra-env stack so the caller can pass it to executor.Sh
 * without holding the lock during the spawn. */
func (rt *scriptRuntime) snapshotEnv() []string {
    rt.secretMu.Lock()
    defer rt.secretMu.Unlock()
    if len(rt.envExtra) == 0 {
        return nil
    }
    out := make([]string, len(rt.envExtra))
    copy(out, rt.envExtra)
    return out
}

/* maskOutput replaces every active mask value with a fixed
 * '********' redaction. Empty masks are skipped so a binding
 * with an empty secret never wipes every byte of the log. */
func (rt *scriptRuntime) maskOutput(s string) string {
    rt.secretMu.Lock()
    masks := append([]string(nil), rt.masks...)
    rt.secretMu.Unlock()
    for _, m := range masks {
        if m == "" {
            continue
        }
        s = strings.ReplaceAll(s, m, "********")
    }
    return s
}

/* pushSecrets records the current env/masks lengths and
 * appends the supplied bindings. The returned snapshot is
 * passed to popSecrets so withCredentials can restore even
 * on early returns / panics via defer. */
type secretFrame struct {
    envLen   int
    masksLen int
}

func (rt *scriptRuntime) pushSecrets(env, masks []string) secretFrame {
    rt.secretMu.Lock()
    defer rt.secretMu.Unlock()
    frame := secretFrame{
        envLen:   len(rt.envExtra),
        masksLen: len(rt.masks),
    }
    rt.envExtra = append(rt.envExtra, env...)
    rt.masks = append(rt.masks, masks...)
    return frame
}

func (rt *scriptRuntime) popSecrets(frame secretFrame) {
    rt.secretMu.Lock()
    defer rt.secretMu.Unlock()
    rt.envExtra = rt.envExtra[:frame.envLen]
    rt.masks = rt.masks[:frame.masksLen]
}

/* applyCatchForced records a catchError verdict on the
 * runtime. Repeated calls keep the worst-of result so nested
 * catchError blocks compose without an upstream SUCCESS
 * swallowing an inner FAILURE. */
func (rt *scriptRuntime) applyCatchForced(build, stage BuildStatus) {
    if isMoreSevereStatus(build, rt.catchForcedBuild) {
        rt.catchForcedBuild = build
    }
    if isMoreSevereStatus(stage, rt.catchForcedStage) {
        rt.catchForcedStage = stage
    }
}

/* isMoreSevereStatus reports whether a is a worse outcome
 * than b. The ordering matches the wolfCI BuildStatus
 * enum's intent: Pending < Running < Success < Failure.
 * Used by applyCatchForced to keep the worst observed
 * verdict across nested catchError invocations. */
func isMoreSevereStatus(a, b BuildStatus) bool {
    return int(a) > int(b)
}

func (rt *scriptRuntime) appendEcho(msg string) {
    /* Mask any active secrets BEFORE they land in the buffer
     * so every echo path (native echo, sh output, future
     * step natives) goes through the same redaction. The
     * lock-order convention is secretMu first, then echoMu;
     * maskOutput drops secretMu before we acquire echoMu so
     * the two never overlap. */
    msg = rt.maskOutput(msg)
    rt.echoMu.Lock()
    defer rt.echoMu.Unlock()
    rt.echoBuf = append(rt.echoBuf, msg)
}

func (rt *scriptRuntime) outputString() string {
    rt.echoMu.Lock()
    defer rt.echoMu.Unlock()
    return strings.Join(rt.echoBuf, "\n")
}

func (rt *scriptRuntime) registerNatives() {
    /* parallel lives here because it's part of how the script
     * runtime drives execution (goroutine fan-out + sibling
     * lifecycle). The step-library natives (sh / echo /
     * sleep / error / script) are registered from
     * steps_core.go so the step surface stays grouped. */
    rt.globals.define("parallel", &sNative{name: "parallel",
        fn: nativeParallel})
    /* Common Java type identifiers pre-bound as string values
     * so `x instanceof Exception` and friends resolve at eval
     * time without each Jenkinsfile having to declare them.
     * instanceofCheck consults the string name; the binding's
     * actual runtime value is just the type's spelling. */
    for _, typeName := range []string{
        "Exception",
        "RuntimeException",
        "Throwable",
        "Error",
        "Collection",
        "List",
        "ArrayList",
        "Iterable",
        "Map",
        "HashMap",
        "LinkedHashMap",
        "String",
        "CharSequence",
        "GString",
        "Number",
        "Integer",
        "Long",
        "Boolean",
        "Object",
    } {
        rt.globals.define(typeName, &sStr{v: typeName})
    }
    registerCoreSteps(rt)
    /* 18.25 currentBuild / previousBuild builtins. The
     * registration is a no-op when the runtime has no
     * BuildInfoProvider wired, matching the rest of the
     * "seam wired = feature on" pattern. */
    registerCurrentBuild(rt)
    /* 18.30 InitialEnv: when the executor passed a map,
     * define `env` on globals as an sMap AND prime
     * rt.envExtra so sh steps inherit the same values
     * through their process env. The caller's withCredentials
     * frames still push on top of envExtra later, so the
     * layering matches the rest of the secret stack. */
    if len(rt.initialEnv) > 0 {
        envMap := newMap()
        for k, v := range rt.initialEnv {
            envMap.set(k, &sStr{v: v})
            rt.envExtra = append(rt.envExtra, k+"="+v)
        }
        rt.globals.define("env", envMap)
    }
}

/* ----- control-flow signals --------------------------------- */

type retSignal struct{ value scriptValue }

func (*retSignal) Error() string { return "script: return" }

type brSignal struct{}

func (*brSignal) Error() string { return "script: break" }

type contSignal struct{}

func (*contSignal) Error() string { return "script: continue" }

type throwSignal struct{ value scriptValue }

func (*throwSignal) Error() string { return "script: throw" }

/* ----- entry point ------------------------------------------ */

/* evalScriptBlock evaluates a parsed Block under a fresh
 * runtime and returns (output, err). A throw signal that
 * reaches the top is downgraded to a script-level error so
 * the declarative interpreter can map it to BuildFailure
 * without losing the exception's message. */
func evalScriptBlock(ctx context.Context, executor Executor,
    block *Block) (string, error) {
    rt := newScriptRuntime(executor)
    env := newEnv(rt.globals)
    err := evalBlock(ctx, rt, env, block)
    return rt.outputString(), err
}

func evalBlock(ctx context.Context, rt *scriptRuntime,
    env *sEnv, block *Block) error {
    if block == nil {
        return nil
    }
    for _, st := range block.Statements {
        if err := evalStmt(ctx, rt, env, st); err != nil {
            return err
        }
    }
    return nil
}

/* ----- statements ------------------------------------------- */

func evalStmt(ctx context.Context, rt *scriptRuntime,
    env *sEnv, st Stmt) error {
    switch s := st.(type) {
    case *VarDecl:
        var v scriptValue = &sNull{}
        if s.Init != nil {
            x, err := evalExpr(ctx, rt, env, s.Init)
            if err != nil {
                return err
            }
            v = x
        }
        env.define(s.Name, v)
        return nil
    case *AssignStmt:
        v, err := evalExpr(ctx, rt, env, s.Value)
        if err != nil {
            return err
        }
        return assignTo(ctx, rt, env, s.Target, v)
    case *ExprStmt:
        _, err := evalExpr(ctx, rt, env, s.X)
        return err
    case *ReturnStmt:
        var v scriptValue = &sNull{}
        if s.Value != nil {
            x, err := evalExpr(ctx, rt, env, s.Value)
            if err != nil {
                return err
            }
            v = x
        }
        return &retSignal{value: v}
    case *ThrowStmt:
        v, err := evalExpr(ctx, rt, env, s.Value)
        if err != nil {
            return err
        }
        return &throwSignal{value: v}
    case *BreakStmt:
        return &brSignal{}
    case *ContinueStmt:
        return &contSignal{}
    case *IfStmt:
        c, err := evalExpr(ctx, rt, env, s.Cond)
        if err != nil {
            return err
        }
        if truthy(c) {
            return evalStmt(ctx, rt, env, s.Then)
        }
        if s.Else != nil {
            return evalStmt(ctx, rt, env, s.Else)
        }
        return nil
    case *WhileStmt:
        for {
            c, err := evalExpr(ctx, rt, env, s.Cond)
            if err != nil {
                return err
            }
            if !truthy(c) {
                return nil
            }
            if err := evalStmt(ctx, rt, env, s.Body); err != nil {
                if _, ok := err.(*brSignal); ok {
                    return nil
                }
                if _, ok := err.(*contSignal); ok {
                    continue
                }
                return err
            }
        }
    case *ForInStmt:
        iter, err := evalExpr(ctx, rt, env, s.Iter)
        if err != nil {
            return err
        }
        return forEach(ctx, rt, env, iter, s.VarName, s.Body)
    case *BlockStmt:
        child := newEnv(env)
        return evalBlock(ctx, rt, child, s.Body)
    case *FuncDecl:
        cl := &sClosure{params: s.Params, body: s.Body,
            env: env}
        env.define(s.Name, cl)
        return nil
    case *TryStmt:
        bodyEnv := newEnv(env)
        bodyErr := evalBlock(ctx, rt, bodyEnv, s.Body)
        if ts, ok := bodyErr.(*throwSignal); ok &&
            len(s.Catches) > 0 {
            /* 18.15 takes the first matching catch (no type
             * filtering yet); typed catch matching lands in
             * a follow-on so jenkinsUtils.groovy's typed
             * catch can do proper instanceof routing. */
            cc := s.Catches[0]
            catchEnv := newEnv(env)
            catchEnv.define(cc.ParamName, ts.value)
            if catchErr := evalBlock(ctx, rt, catchEnv,
                cc.Body); catchErr != nil {
                bodyErr = catchErr
            } else {
                bodyErr = nil
            }
        }
        if s.Finally != nil {
            finEnv := newEnv(env)
            if finErr := evalBlock(ctx, rt, finEnv,
                s.Finally); finErr != nil {
                return finErr
            }
        }
        return bodyErr
    }
    return fmt.Errorf("pipeline.exec_script: unsupported "+
        "statement %T", st)
}

func forEach(ctx context.Context, rt *scriptRuntime,
    env *sEnv, iter scriptValue, varName string,
    body Stmt) error {
    var items []scriptValue
    switch v := iter.(type) {
    case *sList:
        items = v.items
    case *sMap:
        /* Iterating a map yields its keys (Groovy default
         * is map entries; for our subset, iterate keys to
         * keep the test surface small). */
        for _, k := range v.keys {
            items = append(items, &sStr{v: k})
        }
    default:
        return fmt.Errorf(
            "pipeline.exec_script: for-in iter not "+
                "iterable (%T)", iter)
    }
    for _, item := range items {
        loopEnv := newEnv(env)
        loopEnv.define(varName, item)
        if err := evalStmt(ctx, rt, loopEnv, body); err != nil {
            if _, ok := err.(*brSignal); ok {
                return nil
            }
            if _, ok := err.(*contSignal); ok {
                continue
            }
            return err
        }
    }
    return nil
}

/* assignTo updates an lvalue (IdentExpr / SubscriptExpr /
 * MemberExpr). The 18.15 subset covers the first two; member-
 * access assignment lands in a follow-on. */
func assignTo(ctx context.Context, rt *scriptRuntime,
    env *sEnv, target Expr, v scriptValue) error {
    switch t := target.(type) {
    case *IdentExpr:
        env.assign(t.Name, v)
        return nil
    case *SubscriptExpr:
        obj, err := evalExpr(ctx, rt, env, t.Object)
        if err != nil {
            return err
        }
        idx, err := evalExpr(ctx, rt, env, t.Index)
        if err != nil {
            return err
        }
        return subscriptSet(obj, idx, v)
    }
    return fmt.Errorf(
        "pipeline.exec_script: invalid assignment target %T",
        target)
}

func subscriptSet(obj scriptValue, idx scriptValue,
    v scriptValue) error {
    switch o := obj.(type) {
    case *sMap:
        o.set(stringify(idx), v)
        return nil
    case *sList:
        i, ok := idx.(*sNum)
        if !ok {
            return fmt.Errorf(
                "list subscript must be a number")
        }
        if i.v < 0 || int(i.v) >= len(o.items) {
            return fmt.Errorf(
                "list subscript out of range")
        }
        o.items[i.v] = v
        return nil
    }
    return fmt.Errorf(
        "pipeline.exec_script: subscript assignment on %T",
        obj)
}

/* ----- expressions ------------------------------------------ */

func evalExpr(ctx context.Context, rt *scriptRuntime,
    env *sEnv, e Expr) (scriptValue, error) {
    switch x := e.(type) {
    case *NullLit:
        return &sNull{}, nil
    case *BoolLit:
        return &sBool{v: x.Value}, nil
    case *NumberLit:
        n, err := strconv.ParseInt(x.Value, 10, 64)
        if err != nil {
            return nil, fmt.Errorf(
                "pipeline.exec_script: bad number %q",
                x.Value)
        }
        return &sNum{v: n}, nil
    case *StringLit:
        return &sStr{v: x.Value}, nil
    case *IdentExpr:
        _, v, ok := env.lookup(x.Name)
        if !ok {
            return nil, fmt.Errorf(
                "pipeline.exec_script: undefined identifier "+
                    "%q at %d:%d",
                x.Name, x.Pos.Line, x.Pos.Col)
        }
        return v, nil
    case *ListExpr:
        var items []scriptValue
        for _, el := range x.Elements {
            v, err := evalExpr(ctx, rt, env, el)
            if err != nil {
                return nil, err
            }
            items = append(items, v)
        }
        return &sList{items: items}, nil
    case *MapExpr:
        m := newMap()
        for _, en := range x.Entries {
            /* Bare-ident map keys are Groovy syntax for a
             * string-valued key matching the ident's spelling
             * (`[foo: 1]` is the same as `["foo": 1]`).
             * Evaluating the ident as a binding would
             * surprise callers and break unquoted keys that
             * happen to share names with the runtime's
             * predefined type identifiers (Exception, etc.).
             * StringLit and NumberLit keys evaluate normally
             * so `["x": 1]` and `[1: "a"]` still work. */
            var keyStr string
            if id, ok := en.Key.(*IdentExpr); ok {
                keyStr = id.Name
            } else {
                kv, err := evalExpr(ctx, rt, env, en.Key)
                if err != nil {
                    return nil, err
                }
                keyStr = stringify(kv)
            }
            vv, err := evalExpr(ctx, rt, env, en.Value)
            if err != nil {
                return nil, err
            }
            m.set(keyStr, vv)
        }
        return m, nil
    case *ClosureExpr:
        return &sClosure{params: x.Params, body: x.Body,
            env: env}, nil
    case *MemberExpr:
        obj, err := evalExpr(ctx, rt, env, x.Object)
        if err != nil {
            return nil, err
        }
        return memberAccess(obj, x.Name)
    case *SubscriptExpr:
        obj, err := evalExpr(ctx, rt, env, x.Object)
        if err != nil {
            return nil, err
        }
        idx, err := evalExpr(ctx, rt, env, x.Index)
        if err != nil {
            return nil, err
        }
        return subscriptGet(obj, idx)
    case *CallExpr:
        fn, err := evalExpr(ctx, rt, env, x.Fn)
        if err != nil {
            return nil, err
        }
        args, err := collectCallArgs(ctx, rt, env, x.Args,
            x.ClosureArg)
        if err != nil {
            return nil, err
        }
        return invokeCallable(ctx, rt, fn, args)
    case *CommandCallExpr:
        fn, err := evalExpr(ctx, rt, env, x.Fn)
        if err != nil {
            return nil, err
        }
        arg, err := evalExpr(ctx, rt, env, x.Arg)
        if err != nil {
            return nil, err
        }
        return invokeCallable(ctx, rt, fn,
            []scriptValue{arg})
    case *BinaryExpr:
        return evalBinary(ctx, rt, env, x)
    case *UnaryExpr:
        v, err := evalExpr(ctx, rt, env, x.X)
        if err != nil {
            return nil, err
        }
        switch x.Op {
        case "!":
            return &sBool{v: !truthy(v)}, nil
        case "-":
            if n, ok := v.(*sNum); ok {
                return &sNum{v: -n.v}, nil
            }
            return nil, fmt.Errorf(
                "pipeline.exec_script: unary - on non-number")
        }
    case *TernaryExpr:
        c, err := evalExpr(ctx, rt, env, x.Cond)
        if err != nil {
            return nil, err
        }
        if truthy(c) {
            return evalExpr(ctx, rt, env, x.Then)
        }
        return evalExpr(ctx, rt, env, x.Else)
    case *NewExpr:
        var msg string
        for _, a := range x.Args {
            v, err := evalExpr(ctx, rt, env, a.Value)
            if err != nil {
                return nil, err
            }
            if s, ok := v.(*sStr); ok {
                msg = s.v
                break
            }
        }
        return &sExcept{typ: x.Type, msg: msg}, nil
    case *AssignExpr:
        v, err := evalExpr(ctx, rt, env, x.Value)
        if err != nil {
            return nil, err
        }
        if err := assignTo(ctx, rt, env, x.Target,
            v); err != nil {
            return nil, err
        }
        return v, nil
    }
    return nil, fmt.Errorf(
        "pipeline.exec_script: unsupported expression %T", e)
}

func evalBinary(ctx context.Context, rt *scriptRuntime,
    env *sEnv, b *BinaryExpr) (scriptValue, error) {
    /* Short-circuit && and || before evaluating rhs. */
    if b.Op == "&&" || b.Op == "||" {
        l, err := evalExpr(ctx, rt, env, b.Lhs)
        if err != nil {
            return nil, err
        }
        lt := truthy(l)
        if b.Op == "&&" && !lt {
            return &sBool{v: false}, nil
        }
        if b.Op == "||" && lt {
            return &sBool{v: true}, nil
        }
        r, err := evalExpr(ctx, rt, env, b.Rhs)
        if err != nil {
            return nil, err
        }
        return &sBool{v: truthy(r)}, nil
    }
    l, err := evalExpr(ctx, rt, env, b.Lhs)
    if err != nil {
        return nil, err
    }
    r, err := evalExpr(ctx, rt, env, b.Rhs)
    if err != nil {
        return nil, err
    }
    switch b.Op {
    case "+":
        if ls, ok := l.(*sStr); ok {
            return &sStr{v: ls.v + stringify(r)}, nil
        }
        if rs, ok := r.(*sStr); ok {
            return &sStr{v: stringify(l) + rs.v}, nil
        }
        if ln, lok := l.(*sNum); lok {
            if rn, rok := r.(*sNum); rok {
                return &sNum{v: ln.v + rn.v}, nil
            }
        }
    case "-":
        if ln, lok := l.(*sNum); lok {
            if rn, rok := r.(*sNum); rok {
                return &sNum{v: ln.v - rn.v}, nil
            }
        }
    case "*":
        if ln, lok := l.(*sNum); lok {
            if rn, rok := r.(*sNum); rok {
                return &sNum{v: ln.v * rn.v}, nil
            }
        }
    case "/":
        if ln, lok := l.(*sNum); lok {
            if rn, rok := r.(*sNum); rok {
                if rn.v == 0 {
                    return nil, fmt.Errorf(
                        "pipeline.exec_script: divide by zero")
                }
                return &sNum{v: ln.v / rn.v}, nil
            }
        }
    case "==":
        return &sBool{v: equals(l, r)}, nil
    case "!=":
        return &sBool{v: !equals(l, r)}, nil
    case "<", ">", "<=", ">=":
        return compareNums(l, r, b.Op)
    case "instanceof":
        return instanceofCheck(l, r), nil
    }
    return nil, fmt.Errorf(
        "pipeline.exec_script: unsupported binary op %q on "+
            "%T and %T", b.Op, l, r)
}

func compareNums(l, r scriptValue, op string) (scriptValue,
    error) {
    ln, lok := l.(*sNum)
    rn, rok := r.(*sNum)
    if !lok || !rok {
        return nil, fmt.Errorf(
            "pipeline.exec_script: %s requires numbers", op)
    }
    var ok bool
    switch op {
    case "<":
        ok = ln.v < rn.v
    case ">":
        ok = ln.v > rn.v
    case "<=":
        ok = ln.v <= rn.v
    case ">=":
        ok = ln.v >= rn.v
    }
    return &sBool{v: ok}, nil
}

func equals(l, r scriptValue) bool {
    if _, lok := l.(*sNull); lok {
        if _, rok := r.(*sNull); rok {
            return true
        }
        return false
    }
    if _, rok := r.(*sNull); rok {
        return false
    }
    switch lv := l.(type) {
    case *sBool:
        if rv, ok := r.(*sBool); ok {
            return lv.v == rv.v
        }
    case *sNum:
        if rv, ok := r.(*sNum); ok {
            return lv.v == rv.v
        }
    case *sStr:
        if rv, ok := r.(*sStr); ok {
            return lv.v == rv.v
        }
    }
    return false
}

/* instanceofCheck evaluates `l instanceof r`. The RHS is the
 * type identifier the parser captured; the runtime exposes
 * common Java type names as sStr globals (Exception,
 * RuntimeException, Throwable, Collection, List, Map, String,
 * Integer, Number, Boolean) so the natural Jenkinsfile syntax
 * `x instanceof Exception` resolves at evalExpr-time without
 * the caller having to pre-bind anything.
 *
 * Coverage rules:
 *   - sExcept: matches "Exception", "Throwable", or any string
 *     equal to its typ field. RuntimeException matches against
 *     either its typ or the literal "RuntimeException" since
 *     the helper code routes generically on that name.
 *   - sList:   matches "Collection", "List", "ArrayList",
 *     "Iterable" - all idiomatic Java aliases for an iterable
 *     sequence; jenkinsUtils.shouldTestRetry uses
 *     "Collection" specifically.
 *   - sMap:    matches "Map", "HashMap", "LinkedHashMap".
 *   - sStr:    matches "String", "CharSequence".
 *   - sNum:    matches "Number", "Integer", "Long".
 *   - sBool:   matches "Boolean".
 *
 * Anything else surfaces as false; richer subtyping rules land
 * if a future Jenkinsfile needs them. */
func instanceofCheck(l, r scriptValue) scriptValue {
    rs, ok := r.(*sStr)
    if !ok {
        return &sBool{v: false}
    }
    typeName := rs.v
    switch lv := l.(type) {
    case *sExcept:
        if lv.typ == typeName {
            return &sBool{v: true}
        }
        switch typeName {
        case "Exception", "Throwable", "RuntimeException":
            return &sBool{v: true}
        }
    case *sList:
        switch typeName {
        case "Collection", "List", "ArrayList", "Iterable":
            return &sBool{v: true}
        }
    case *sMap:
        switch typeName {
        case "Map", "HashMap", "LinkedHashMap":
            return &sBool{v: true}
        }
    case *sStr:
        switch typeName {
        case "String", "CharSequence", "GString":
            return &sBool{v: true}
        }
    case *sNum:
        switch typeName {
        case "Number", "Integer", "Long":
            return &sBool{v: true}
        }
    case *sBool:
        if typeName == "Boolean" {
            return &sBool{v: true}
        }
    }
    return &sBool{v: false}
}

func memberAccess(obj scriptValue, name string) (scriptValue,
    error) {
    switch o := obj.(type) {
    case *sMap:
        if v, ok := o.values[name]; ok {
            return v, nil
        }
        return &sNull{}, nil
    case *sExcept:
        return exceptMember(o, name)
    case *sStr:
        return stringMember(o, name)
    case *sList:
        return listMember(o, name)
    }
    return &sNull{}, nil
}

/* exceptMember dispatches member access on a script exception
 * value. The supported surface mirrors what jenkinsUtils.groovy
 * style helpers actually call: `message` / `getMessage()`
 * (the human-readable text the catch handler logs),
 * `toString()` (the "Type: msg" form Java's Throwable.toString
 * produces - matches what `text = jobOrException.toString()`
 * in shouldTestRetry searches against), and the bare `type`
 * shortcut for the few helpers that route on exception type. */
func exceptMember(ex *sExcept, name string) (scriptValue, error) {
    switch name {
    case "message":
        return &sStr{v: ex.msg}, nil
    case "type":
        return &sStr{v: ex.typ}, nil
    case "getMessage":
        msg := ex.msg
        return &sNative{name: "getMessage",
            fn: func(ctx context.Context, rt *scriptRuntime,
                _ []scriptValue) (scriptValue, error) {
                return &sStr{v: msg}, nil
            }}, nil
    case "toString":
        msg := ex.msg
        typ := ex.typ
        return &sNative{name: "toString",
            fn: func(ctx context.Context, rt *scriptRuntime,
                _ []scriptValue) (scriptValue, error) {
                if msg != "" {
                    return &sStr{v: typ + ": " + msg}, nil
                }
                return &sStr{v: typ}, nil
            }}, nil
    }
    return &sNull{}, nil
}

/* stringMember dispatches method-style access on a string
 * value (e.g. `name.replaceAll("/", "_")`). Returns an sNative
 * the surrounding CallExpr invokes; the closure captures the
 * receiver so the resulting native does not depend on dispatch
 * threading the receiver through args.
 *
 * Methods covered (smallest set the master-job + jenkinsUtils
 * helpers actually use):
 *
 *   replaceAll(regex, replacement)  Java-style regex replace
 *   contains(substring)             plain substring check
 *   toString()                      identity, for Groovy
 *                                   `${x.toString()}` paths
 *   length()                        rune count
 *   startsWith / endsWith / trim    small ergonomics for the
 *                                   step libraries to come
 */
func stringMember(s *sStr, name string) (scriptValue, error) {
    v := s.v
    switch name {
    case "replaceAll":
        return &sNative{name: "replaceAll",
            fn: func(ctx context.Context, rt *scriptRuntime,
                args []scriptValue) (scriptValue, error) {
                if len(args) != 2 {
                    return nil, fmt.Errorf(
                        "String.replaceAll: expected 2 args, "+
                            "got %d", len(args))
                }
                pat, ok := args[0].(*sStr)
                if !ok {
                    return nil, fmt.Errorf(
                        "String.replaceAll: regex arg must "+
                            "be a string (got %T)", args[0])
                }
                repl, ok := args[1].(*sStr)
                if !ok {
                    return nil, fmt.Errorf(
                        "String.replaceAll: replacement arg "+
                            "must be a string (got %T)",
                        args[1])
                }
                re, err := regexp.Compile(pat.v)
                if err != nil {
                    return nil, fmt.Errorf(
                        "String.replaceAll: bad regex %q: %w",
                        pat.v, err)
                }
                return &sStr{
                    v: re.ReplaceAllString(v, repl.v)}, nil
            }}, nil
    case "contains":
        return &sNative{name: "contains",
            fn: func(ctx context.Context, rt *scriptRuntime,
                args []scriptValue) (scriptValue, error) {
                if len(args) != 1 {
                    return nil, fmt.Errorf(
                        "String.contains: expected 1 arg")
                }
                ss, ok := args[0].(*sStr)
                if !ok {
                    return nil, fmt.Errorf(
                        "String.contains: arg must be a " +
                            "string")
                }
                return &sBool{
                    v: strings.Contains(v, ss.v)}, nil
            }}, nil
    case "startsWith":
        return &sNative{name: "startsWith",
            fn: func(ctx context.Context, rt *scriptRuntime,
                args []scriptValue) (scriptValue, error) {
                ss, _ := args[0].(*sStr)
                if ss == nil {
                    return &sBool{v: false}, nil
                }
                return &sBool{
                    v: strings.HasPrefix(v, ss.v)}, nil
            }}, nil
    case "endsWith":
        return &sNative{name: "endsWith",
            fn: func(ctx context.Context, rt *scriptRuntime,
                args []scriptValue) (scriptValue, error) {
                ss, _ := args[0].(*sStr)
                if ss == nil {
                    return &sBool{v: false}, nil
                }
                return &sBool{
                    v: strings.HasSuffix(v, ss.v)}, nil
            }}, nil
    case "trim":
        return &sNative{name: "trim",
            fn: func(ctx context.Context, rt *scriptRuntime,
                _ []scriptValue) (scriptValue, error) {
                return &sStr{v: strings.TrimSpace(v)}, nil
            }}, nil
    case "toString":
        return &sNative{name: "toString",
            fn: func(ctx context.Context, rt *scriptRuntime,
                _ []scriptValue) (scriptValue, error) {
                return &sStr{v: v}, nil
            }}, nil
    case "length", "size":
        n := int64(len([]rune(v)))
        return &sNative{name: name,
            fn: func(ctx context.Context, rt *scriptRuntime,
                _ []scriptValue) (scriptValue, error) {
                return &sNum{v: n}, nil
            }}, nil
    }
    return &sNull{}, nil
}

/* listMember dispatches method-style access on a list value.
 * Coverage focuses on the Jenkins helper surface jenkinsUtils
 * actually reaches for: `join` for the rawLog concat path
 * inside shouldTestRetry, plus the size / toString /
 * isEmpty pair so list-shaped log results carry the obvious
 * accessors. */
func listMember(l *sList, name string) (scriptValue, error) {
    items := l.items
    switch name {
    case "each":
        return &sNative{name: "each",
            fn: func(ctx context.Context, rt *scriptRuntime,
                args []scriptValue) (scriptValue, error) {
                if len(args) == 0 {
                    return nil, fmt.Errorf(
                        "List.each: missing closure")
                }
                cl, ok := args[len(args)-1].(*sClosure)
                if !ok {
                    return nil, fmt.Errorf(
                        "List.each: arg must be a closure")
                }
                for _, it := range items {
                    if _, err := invokeClosure(ctx, rt,
                        cl, []scriptValue{it}); err != nil {
                        return nil, err
                    }
                }
                return &sNull{}, nil
            }}, nil
    case "collect":
        return &sNative{name: "collect",
            fn: func(ctx context.Context, rt *scriptRuntime,
                args []scriptValue) (scriptValue, error) {
                if len(args) == 0 {
                    return nil, fmt.Errorf(
                        "List.collect: missing closure")
                }
                cl, ok := args[len(args)-1].(*sClosure)
                if !ok {
                    return nil, fmt.Errorf(
                        "List.collect: arg must be a closure")
                }
                outItems := make([]scriptValue, 0,
                    len(items))
                for _, it := range items {
                    v, err := invokeClosure(ctx, rt, cl,
                        []scriptValue{it})
                    if err != nil {
                        return nil, err
                    }
                    outItems = append(outItems, v)
                }
                return &sList{items: outItems}, nil
            }}, nil
    case "join":
        return &sNative{name: "join",
            fn: func(ctx context.Context, rt *scriptRuntime,
                args []scriptValue) (scriptValue, error) {
                sep := ""
                if len(args) >= 1 {
                    if s, ok := args[0].(*sStr); ok {
                        sep = s.v
                    }
                }
                parts := make([]string, 0, len(items))
                for _, it := range items {
                    parts = append(parts, stringify(it))
                }
                return &sStr{
                    v: strings.Join(parts, sep)}, nil
            }}, nil
    case "size", "length":
        n := int64(len(items))
        return &sNative{name: name,
            fn: func(ctx context.Context, rt *scriptRuntime,
                _ []scriptValue) (scriptValue, error) {
                return &sNum{v: n}, nil
            }}, nil
    case "isEmpty":
        empty := len(items) == 0
        return &sNative{name: "isEmpty",
            fn: func(ctx context.Context, rt *scriptRuntime,
                _ []scriptValue) (scriptValue, error) {
                return &sBool{v: empty}, nil
            }}, nil
    case "toString":
        s := stringify(l)
        return &sNative{name: "toString",
            fn: func(ctx context.Context, rt *scriptRuntime,
                _ []scriptValue) (scriptValue, error) {
                return &sStr{v: s}, nil
            }}, nil
    }
    return &sNull{}, nil
}

func subscriptGet(obj scriptValue, idx scriptValue) (
    scriptValue, error) {
    switch o := obj.(type) {
    case *sMap:
        if v, ok := o.values[stringify(idx)]; ok {
            return v, nil
        }
        return &sNull{}, nil
    case *sList:
        i, ok := idx.(*sNum)
        if !ok {
            return nil, fmt.Errorf(
                "list subscript must be a number")
        }
        if i.v < 0 || int(i.v) >= len(o.items) {
            return &sNull{}, nil
        }
        return o.items[i.v], nil
    }
    return nil, fmt.Errorf(
        "pipeline.exec_script: subscript on %T", obj)
}

func truthy(v scriptValue) bool {
    switch x := v.(type) {
    case *sNull:
        return false
    case *sBool:
        return x.v
    case *sNum:
        return x.v != 0
    case *sStr:
        return x.v != ""
    case *sList:
        return len(x.items) > 0
    case *sMap:
        return len(x.keys) > 0
    }
    return true
}

func stringify(v scriptValue) string {
    switch x := v.(type) {
    case nil:
        return "null"
    case *sNull:
        return "null"
    case *sBool:
        if x.v {
            return "true"
        }
        return "false"
    case *sNum:
        return strconv.FormatInt(x.v, 10)
    case *sStr:
        return x.v
    case *sList:
        parts := make([]string, 0, len(x.items))
        for _, it := range x.items {
            parts = append(parts, stringify(it))
        }
        return "[" + strings.Join(parts, ", ") + "]"
    case *sMap:
        parts := make([]string, 0, len(x.keys))
        for _, k := range x.keys {
            parts = append(parts,
                k+":"+stringify(x.values[k]))
        }
        return "[" + strings.Join(parts, ", ") + "]"
    case *sExcept:
        if x.msg != "" {
            return x.typ + ": " + x.msg
        }
        return x.typ
    case *sClosure:
        return "<closure>"
    case *sNative:
        return "<native " + x.name + ">"
    }
    return "?"
}

/* ----- callable dispatch ------------------------------------ */

func invokeCallable(ctx context.Context, rt *scriptRuntime,
    fn scriptValue, args []scriptValue) (scriptValue, error) {
    switch f := fn.(type) {
    case *sNative:
        return f.fn(ctx, rt, args)
    case *sClosure:
        return invokeClosure(ctx, rt, f, args)
    }
    return nil, fmt.Errorf(
        "pipeline.exec_script: value is not callable (%T)",
        fn)
}

func invokeClosure(ctx context.Context, rt *scriptRuntime,
    cl *sClosure, args []scriptValue) (scriptValue, error) {
    child := newEnv(cl.env)
    if len(cl.params) == 0 && len(args) == 1 {
        child.define("it", args[0])
    } else {
        for i, p := range cl.params {
            var v scriptValue = &sNull{}
            if i < len(args) {
                v = args[i]
            }
            child.define(p.Name, v)
        }
    }
    /* Groovy closures + Groovy `def`-functions implicitly
     * return the value of their last expression statement.
     * Walk the body so the last ExprStmt's expression is
     * captured directly, while ReturnStmt / throw / break /
     * continue still short-circuit via their signal errors.
     * A body that ends with a non-expression statement (an
     * if/while without a trailing expression, an assignment
     * etc.) keeps the old behavior and returns sNull. */
    if cl.body == nil || len(cl.body.Statements) == 0 {
        return &sNull{}, nil
    }
    last := len(cl.body.Statements) - 1
    for i, st := range cl.body.Statements {
        if i == last {
            if es, ok := st.(*ExprStmt); ok {
                v, err := evalExpr(ctx, rt, child, es.X)
                if err != nil {
                    if r, ok := err.(*retSignal); ok {
                        return r.value, nil
                    }
                    return nil, err
                }
                return v, nil
            }
        }
        if err := evalStmt(ctx, rt, child, st); err != nil {
            if r, ok := err.(*retSignal); ok {
                return r.value, nil
            }
            return nil, err
        }
    }
    return &sNull{}, nil
}

/* ----- call-arg collection ---------------------------------- */

/* collectCallArgs evaluates a CallExpr's arg list and any
 * trailing closure into the value slice that invokeCallable
 * expects. Groovy's named-arg convention: any args with a
 * non-empty Name are gathered into a single map that becomes
 * args[0]; positional args follow, and the trailing closure
 * (if present) is appended as the last positional arg. This
 * is the same shape Jenkins step functions receive, which
 * lets the step-library natives in steps_core.go inspect
 * args[0] uniformly (string for the naked form, map for the
 * named-args form).
 */
func collectCallArgs(ctx context.Context, rt *scriptRuntime,
    env *sEnv, callArgs []CallArg,
    closureArg *ClosureExpr) ([]scriptValue, error) {
    var named *sMap
    var positional []scriptValue
    for _, a := range callArgs {
        v, err := evalExpr(ctx, rt, env, a.Value)
        if err != nil {
            return nil, err
        }
        if a.Name != "" {
            if named == nil {
                named = newMap()
            }
            named.set(a.Name, v)
        } else {
            positional = append(positional, v)
        }
    }
    var out []scriptValue
    if named != nil {
        out = append(out, named)
    }
    out = append(out, positional...)
    if closureArg != nil {
        cv, err := evalExpr(ctx, rt, env, closureArg)
        if err != nil {
            return nil, err
        }
        out = append(out, cv)
    }
    return out, nil
}

/* ----- native functions ------------------------------------- */

/* nativeParallel runs each map value as a closure on its own
 * goroutine. The first error from any branch is preserved so
 * the script step can surface it; all branches always run to
 * completion (no early cancellation), matching Jenkins'
 * parallel semantics where a failure is reported but sibling
 * branches still finish.
 */
func nativeParallel(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if len(args) != 1 {
        return nil, fmt.Errorf(
            "parallel: expected 1 argument, got %d", len(args))
    }
    m, ok := args[0].(*sMap)
    if !ok {
        return nil, fmt.Errorf(
            "parallel: argument must be a map (got %T)",
            args[0])
    }
    if len(m.keys) == 0 {
        return &sNull{}, nil
    }
    var wg sync.WaitGroup
    var errMu sync.Mutex
    var firstErr error
    for _, k := range m.keys {
        cl, ok := m.values[k].(*sClosure)
        if !ok {
            return nil, fmt.Errorf(
                "parallel: map value for %q must be a "+
                    "closure (got %T)", k, m.values[k])
        }
        wg.Add(1)
        go func(name string, closure *sClosure) {
            defer wg.Done()
            _, err := invokeClosure(ctx, rt, closure, nil)
            if err != nil {
                errMu.Lock()
                if firstErr == nil {
                    firstErr = err
                }
                errMu.Unlock()
            }
        }(k, cl)
    }
    wg.Wait()
    if firstErr != nil {
        /* Preserve a throw signal so the script step turns
         * it into BuildFailure without losing the exception
         * value. Other errors propagate as is. */
        return nil, firstErr
    }
    return &sNull{}, nil
}
