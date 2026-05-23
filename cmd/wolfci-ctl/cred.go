package main

/* cmd/wolfci-ctl/cred.go - wolfci-ctl cred subcommand (PLAN.md
 * 18.4). Three verbs:
 *
 *   cred add  --id X --type secret-text|ssh-private-key|
 *               username-password [--label ...] [--username ...]
 *               (reads the secret/password/private-key bytes
 *                from stdin)
 *   cred list
 *   cred rm   --id X
 *
 * All three read the credstore configuration from a server.yaml
 * named via --server-config (default config-files/server.yaml).
 * The two ServerConfig fields the cred path needs are
 * CredentialMasterSecret (hex-encoded) and CredentialDir; the
 * other server fields are ignored on the wolfci-ctl side.
 *
 * The cred subcommand operates on local files only - it does
 * NOT talk to the wolfCI server over gRPC. The operator is
 * expected to run wolfci-ctl on the same host as the server (or
 * a host that has the config-files/ tree mounted) when adding
 * credentials. This mirrors the Jenkins "credentials.xml lives
 * on the controller" model and keeps the master secret off any
 * network.
 */

import (
    "encoding/hex"
    "encoding/json"
    "errors"
    "flag"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "time"

    "github.com/kaleb-himes/wolfCI/internal/credstore"
    "github.com/kaleb-himes/wolfCI/internal/server"
)

/* runCredGroup dispatches `wolfci-ctl cred <verb>`. */
func runCredGroup(args []string, stdout, stderr *os.File) int {
    if len(args) == 0 {
        fmt.Fprintln(stderr, "usage: wolfci-ctl cred <verb>")
        fmt.Fprintln(stderr, "verbs: add, list, rm")
        return 2
    }
    switch args[0] {
    case "add":
        return runCredAdd(args[1:], stdout, stderr)
    case "list":
        return runCredList(args[1:], stdout, stderr)
    case "rm":
        return runCredRm(args[1:], stdout, stderr)
    default:
        fmt.Fprintf(stderr,
            "wolfci-ctl: unknown cred verb %q\n", args[0])
        return 2
    }
}

/* defaultServerConfigPath returns config-files/server.yaml
 * relative to the current working directory. The cred
 * subcommands are run from the wolfCI install root, so this is
 * the same path cmd/wolfci uses.
 */
func defaultServerConfigPath() string {
    return filepath.Join("config-files", "server.yaml")
}

/* openStoreFromServerConfig loads server.yaml and returns a
 * configured credstore.Store. Errors carry enough context for
 * the operator to fix the YAML (which field is wrong, where the
 * file lives).
 */
func openStoreFromServerConfig(
    path string) (*credstore.Store, error) {

    if path == "" {
        path = defaultServerConfigPath()
    }
    cfg, err := server.LoadServerConfig(path)
    if err != nil {
        return nil, fmt.Errorf(
            "load %s: %w", path, err)
    }
    if cfg.CredentialMasterSecret == "" {
        return nil, fmt.Errorf(
            "%s: credential_master_secret is empty", path)
    }
    if cfg.CredentialDir == "" {
        return nil, fmt.Errorf(
            "%s: credential_dir is empty", path)
    }
    master, err := hex.DecodeString(cfg.CredentialMasterSecret)
    if err != nil {
        return nil, fmt.Errorf(
            "%s: credential_master_secret is not hex: %w",
            path, err)
    }
    if len(master) < 16 {
        return nil, fmt.Errorf(
            "%s: credential_master_secret is %d bytes; "+
                "want at least 16 bytes of entropy",
            path, len(master))
    }
    return credstore.NewStore(cfg.CredentialDir, master)
}

/* runCredAdd implements `wolfci-ctl cred add`. */
func runCredAdd(args []string, stdout, stderr *os.File) int {
    fs := flag.NewFlagSet("cred add", flag.ContinueOnError)
    fs.SetOutput(stderr)
    var (
        serverConfig string
        id           string
        credType     string
        label        string
        username     string
    )
    fs.StringVar(&serverConfig, "server-config", "",
        "path to server.yaml (default config-files/server.yaml)")
    fs.StringVar(&id, "id", "",
        "credential id (filename-safe identifier)")
    fs.StringVar(&credType, "type", "",
        "secret-text | ssh-private-key | username-password")
    fs.StringVar(&label, "label", "",
        "human-readable label for the cred list output")
    fs.StringVar(&username, "username", "",
        "username (required when --type username-password)")
    if err := fs.Parse(args); err != nil {
        return 2
    }
    if id == "" || credType == "" {
        fmt.Fprintln(stderr,
            "wolfci-ctl cred add: --id and --type are required")
        return 2
    }

    store, err := openStoreFromServerConfig(serverConfig)
    if err != nil {
        fmt.Fprintf(stderr, "wolfci-ctl: %v\n", err)
        return 1
    }

    payload, err := readPayloadFromStdin(
        credstore.CredType(credType), username)
    if err != nil {
        fmt.Fprintf(stderr, "wolfci-ctl cred add: %v\n", err)
        return 2
    }
    payloadBytes, err := json.Marshal(payload)
    if err != nil {
        fmt.Fprintf(stderr,
            "wolfci-ctl cred add: marshal payload: %v\n", err)
        return 1
    }
    record := &credstore.Record{
        Type:      credstore.CredType(credType),
        Payload:   payloadBytes,
        CreatedAt: time.Now().UTC(),
        Label:     label,
    }
    if err := store.Add(id, record); err != nil {
        fmt.Fprintf(stderr,
            "wolfci-ctl cred add: %v\n", err)
        return 1
    }
    fmt.Fprintf(stdout, "added %s (%s)\n", id, credType)
    return 0
}

/* readPayloadFromStdin builds the typed payload struct for
 * credType by reading the secret bytes from stdin. The bytes are
 * consumed once; subsequent runs in the same process will see an
 * already-drained stdin.
 *
 * For secret-text and ssh-private-key the entire stdin body is
 * the value (with the trailing newline stripped for the
 * single-line secret-text case; ssh-private-key keeps the body
 * verbatim because PEM blobs span multiple lines).
 *
 * For username-password the stdin body is the password (newline
 * stripped) and the --username flag carries the username.
 */
func readPayloadFromStdin(credType credstore.CredType,
    username string) (any, error) {

    body, err := io.ReadAll(os.Stdin)
    if err != nil {
        return nil, fmt.Errorf("read stdin: %w", err)
    }
    switch credType {
    case credstore.TypeSecretText:
        text := strings.TrimRight(string(body), "\r\n")
        if text == "" {
            return nil, errors.New(
                "stdin is empty; secret-text requires a value")
        }
        return credstore.SecretTextPayload{Secret: text}, nil

    case credstore.TypeSshPrivateKey:
        if len(body) == 0 {
            return nil, errors.New(
                "stdin is empty; ssh-private-key requires PEM " +
                    "key bytes")
        }
        return credstore.SshPrivateKeyPayload{
            PrivateKey: string(body),
        }, nil

    case credstore.TypeUsernamePassword:
        if username == "" {
            return nil, errors.New(
                "--username is required for " +
                    "--type username-password")
        }
        text := strings.TrimRight(string(body), "\r\n")
        if text == "" {
            return nil, errors.New(
                "stdin is empty; username-password requires " +
                    "the password on stdin")
        }
        return credstore.UsernamePasswordPayload{
            Username: username,
            Password: text,
        }, nil

    default:
        return nil, fmt.Errorf(
            "unknown --type %q (want secret-text | "+
                "ssh-private-key | username-password)",
            credType)
    }
}

/* runCredList implements `wolfci-ctl cred list`. */
func runCredList(args []string, stdout, stderr *os.File) int {
    fs := flag.NewFlagSet("cred list", flag.ContinueOnError)
    fs.SetOutput(stderr)
    var serverConfig string
    fs.StringVar(&serverConfig, "server-config", "",
        "path to server.yaml (default config-files/server.yaml)")
    if err := fs.Parse(args); err != nil {
        return 2
    }
    store, err := openStoreFromServerConfig(serverConfig)
    if err != nil {
        fmt.Fprintf(stderr, "wolfci-ctl: %v\n", err)
        return 1
    }
    entries, err := store.List()
    if err != nil {
        fmt.Fprintf(stderr,
            "wolfci-ctl cred list: %v\n", err)
        return 1
    }
    sort.Slice(entries, func(i, j int) bool {
        return entries[i].ID < entries[j].ID
    })
    if len(entries) == 0 {
        fmt.Fprintln(stdout,
            "(no credentials)")
        return 0
    }
    /* Two-column "id  type  label" table with hard-coded
     * column widths so a long label does not push the type
     * column off the screen.
     */
    for _, e := range entries {
        fmt.Fprintf(stdout, "%-32s %-20s %s\n",
            e.ID, e.Type, e.Label)
    }
    return 0
}

/* runCredRm implements `wolfci-ctl cred rm`. */
func runCredRm(args []string, stdout, stderr *os.File) int {
    fs := flag.NewFlagSet("cred rm", flag.ContinueOnError)
    fs.SetOutput(stderr)
    var (
        serverConfig string
        id           string
    )
    fs.StringVar(&serverConfig, "server-config", "",
        "path to server.yaml (default config-files/server.yaml)")
    fs.StringVar(&id, "id", "",
        "credential id to delete")
    if err := fs.Parse(args); err != nil {
        return 2
    }
    if id == "" {
        fmt.Fprintln(stderr,
            "wolfci-ctl cred rm: --id is required")
        return 2
    }
    store, err := openStoreFromServerConfig(serverConfig)
    if err != nil {
        fmt.Fprintf(stderr, "wolfci-ctl: %v\n", err)
        return 1
    }
    if err := store.Delete(id); err != nil {
        fmt.Fprintf(stderr,
            "wolfci-ctl cred rm: %v\n", err)
        return 1
    }
    fmt.Fprintf(stdout, "removed %s\n", id)
    return 0
}
