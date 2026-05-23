/* internal/pipeline/steps_creds.go - PLAN.md 18.18.
 *
 * Credential bindings step library: withCredentials and the
 * `string(...)` binding-descriptor constructor it consumes.
 * The 18.18 surface is the secret-text path only (matching
 * the master-job Jenkinsfile's only use of withCredentials);
 * ssh-private-key and username-password bindings land in
 * follow-on phases.
 *
 * Usage shape:
 *
 *   withCredentials([
 *       string(credentialsId: 'c1', variable: 'TOKEN'),
 *       string(credentialsId: 'c2', variable: 'OTHER'),
 *   ]) {
 *       sh 'curl -H "Authorization: bearer $TOKEN" ...'
 *   }
 *
 * The interpreter sees `string(...)` as an ordinary call
 * that constructs a tagged map (descriptor); withCredentials
 * receives the list of descriptors, queries the runtime's
 * credstore.Store for each, unseals the SecretText payload,
 * and pushes env/mask entries onto the runtime's secret
 * stack for the lifetime of the trailing closure. The push
 * order matches the descriptor order in the list. On exit
 * (normal or via error) popSecrets restores the prior stack
 * via defer so nested withCredentials blocks compose
 * correctly.
 *
 * Log masking happens centrally in appendEcho, so every
 * code path that writes to the build log (sh's combined
 * stdout, native echo, future step natives) gets redacted
 * by the same maskOutput pass.
 */
package pipeline

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/kaleb-himes/wolfCI/internal/credstore"
)

/* registerCredsSteps installs the 18.18 credential-bindings
 * step library on the supplied runtime. Called from
 * registerCoreSteps so callers see one entry point. */
func registerCredsSteps(rt *scriptRuntime) {
    rt.globals.define("string",
        &sNative{name: "string", fn: nativeStringCred})
    rt.globals.define("withCredentials",
        &sNative{name: "withCredentials",
            fn: nativeWithCredentials})
}

/* nativeStringCred is the Groovy `string(credentialsId: ...,
 * variable: ...)` constructor used inside the withCredentials
 * bindings list. Returns a tagged sMap that
 * nativeWithCredentials inspects to decide which credstore
 * record to unseal and which env variable to expose. */
func nativeStringCred(ctx context.Context, rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if len(args) == 0 {
        return nil, fmt.Errorf(
            "string: no arguments " +
                "(expected credentialsId and variable)")
    }
    m, ok := args[0].(*sMap)
    if !ok {
        return nil, fmt.Errorf(
            "string: expected map of named args (got %T)",
            args[0])
    }
    out := newMap()
    out.set("_credType", &sStr{v: "string"})
    if v, ok := m.values["credentialsId"]; ok {
        out.set("credentialsId", v)
    }
    if v, ok := m.values["variable"]; ok {
        out.set("variable", v)
    }
    return out, nil
}

/* nativeWithCredentials loads each binding descriptor from
 * the runtime's credstore, pushes the resulting env entries
 * and mask substrings onto the secret stack for the duration
 * of the trailing closure, and runs the closure. The defer
 * ensures the stack is restored even when the closure throws
 * or returns abnormally - secrets never linger past the
 * block. */
func nativeWithCredentials(ctx context.Context,
    rt *scriptRuntime,
    args []scriptValue) (scriptValue, error) {
    if len(args) < 2 {
        return nil, fmt.Errorf(
            "withCredentials: expected a bindings list and " +
                "a closure body")
    }
    bindings, ok := args[0].(*sList)
    if !ok {
        return nil, fmt.Errorf(
            "withCredentials: first arg must be a list of "+
                "binding descriptors (got %T)", args[0])
    }
    cl, ok := args[len(args)-1].(*sClosure)
    if !ok {
        return nil, fmt.Errorf(
            "withCredentials: last arg must be a closure")
    }
    if rt.creds == nil {
        return nil, fmt.Errorf(
            "withCredentials: credstore not configured on " +
                "executor")
    }
    var envEntries []string
    var maskValues []string
    for _, item := range bindings.items {
        m, ok := item.(*sMap)
        if !ok {
            return nil, fmt.Errorf(
                "withCredentials: binding descriptor must " +
                    "be a map (use string(credentialsId: ...))")
        }
        credType, _ := m.values["_credType"].(*sStr)
        if credType == nil {
            return nil, fmt.Errorf(
                "withCredentials: binding descriptor missing " +
                    "_credType (use string(...) to construct " +
                    "it)")
        }
        if credType.v != "string" {
            return nil, fmt.Errorf(
                "withCredentials: unsupported binding type " +
                    "%q (18.18 supports secret-text bindings "+
                    "only via the `string(...)` form)",
                credType.v)
        }
        credID, _ := m.values["credentialsId"].(*sStr)
        variable, _ := m.values["variable"].(*sStr)
        if credID == nil || variable == nil {
            return nil, fmt.Errorf(
                "withCredentials: binding missing " +
                    "credentialsId or variable")
        }
        rec, err := rt.creds.Get(credID.v)
        if err != nil {
            return nil, fmt.Errorf(
                "withCredentials: get %q: %w", credID.v, err)
        }
        if rec.Type != credstore.TypeSecretText {
            return nil, fmt.Errorf(
                "withCredentials: binding %q wants "+
                    "secret-text but credential is %q",
                credID.v, rec.Type)
        }
        var payload credstore.SecretTextPayload
        if err := json.Unmarshal(rec.Payload,
            &payload); err != nil {
            return nil, fmt.Errorf(
                "withCredentials: unmarshal %q payload: %w",
                credID.v, err)
        }
        envEntries = append(envEntries,
            variable.v+"="+payload.Secret)
        maskValues = append(maskValues, payload.Secret)
    }
    frame := rt.pushSecrets(envEntries, maskValues)
    defer rt.popSecrets(frame)
    if _, err := invokeClosure(ctx, rt, cl, nil); err != nil {
        return nil, err
    }
    return &sNull{}, nil
}
