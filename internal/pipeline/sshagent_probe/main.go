/* internal/pipeline/sshagent_probe/main.go - test helper used by
 * the PLAN.md 18.20 sshagent integration test
 * (internal/pipeline/steps_ssh_test.go).
 *
 * The probe stands in for the OpenSSH client that a real
 * `git clone git@...` would invoke: it consults $SSH_AUTH_SOCK,
 * talks the SSH agent protocol (RFC-draft-miller-ssh-agent) over
 * the unix socket the wolfCI sshagent step exposed, asserts the
 * expected identity is present, exercises a SIGN_REQUEST against
 * it, and either exits 0 + writes "OK\n<comment>\n" to the
 * witness path, or exits 1 with a diagnostic.
 *
 * Usage:
 *
 *   sshagent-probe <expected-comment> <witness-path>
 *
 * The probe reads SSH_AUTH_SOCK from its environment; the
 * wolfCI sshagent step injects it via the runtime's env stack.
 * Keeping the probe in a sibling main package (rather than
 * inline-string + exec) makes the gating logic legible in source
 * control and keeps the test free of binary-protocol literals.
 *
 * NO openssh ssh-agent involvement: this probe never touches
 * the host's ssh-agent binary or socket - it speaks directly to
 * whatever process is listening at $SSH_AUTH_SOCK.
 */
package main

import (
    "bytes"
    "encoding/binary"
    "fmt"
    "io"
    "net"
    "os"
    "time"

    "github.com/wolfssl/go-wolfssl/wolfssh"
)

func main() {
    if len(os.Args) != 3 {
        fmt.Fprintln(os.Stderr, "usage: sshagent-probe "+
            "<expected-comment> <witness-path>")
        os.Exit(2)
    }
    expectComment := os.Args[1]
    witness := os.Args[2]
    sock := os.Getenv("SSH_AUTH_SOCK")
    if sock == "" {
        fmt.Fprintln(os.Stderr,
            "probe: SSH_AUTH_SOCK unset")
        os.Exit(1)
    }
    conn, err := net.DialTimeout("unix", sock, 5*time.Second)
    if err != nil {
        fmt.Fprintf(os.Stderr,
            "probe: dial %s: %v\n", sock, err)
        os.Exit(1)
    }
    defer conn.Close()
    _ = conn.SetDeadline(time.Now().Add(10 * time.Second))

    /* REQUEST_IDENTITIES round trip. */
    req := []byte{wolfssh.AgentMsgRequestIdentities}
    if err := writeFramed(conn, req); err != nil {
        fmt.Fprintf(os.Stderr,
            "probe: write list req: %v\n", err)
        os.Exit(1)
    }
    resp, err := readFramed(conn)
    if err != nil {
        fmt.Fprintf(os.Stderr,
            "probe: read list resp: %v\n", err)
        os.Exit(1)
    }
    if len(resp) < 5 ||
        resp[0] != wolfssh.AgentMsgIdentitiesAnswer {
        fmt.Fprintf(os.Stderr,
            "probe: list resp short / wrong msgID: %v\n",
            resp)
        os.Exit(1)
    }
    numKeys := binary.BigEndian.Uint32(resp[1:5])
    body := resp[5:]

    var found bool
    var foundBlob []byte
    var foundComment string
    for i := uint32(0); i < numKeys; i++ {
        blob, rest, err := wolfssh.ReadSSHString(body)
        if err != nil {
            fmt.Fprintf(os.Stderr,
                "probe: parse keyblob %d: %v\n", i, err)
            os.Exit(1)
        }
        comment, rest2, err := wolfssh.ReadSSHString(rest)
        if err != nil {
            fmt.Fprintf(os.Stderr,
                "probe: parse comment %d: %v\n", i, err)
            os.Exit(1)
        }
        body = rest2
        if string(comment) == expectComment {
            found = true
            foundBlob = append([]byte{}, blob...)
            foundComment = string(comment)
            break
        }
    }
    if !found {
        fmt.Fprintf(os.Stderr,
            "probe: identity with comment %q not found "+
                "among %d keys\n",
            expectComment, numKeys)
        os.Exit(1)
    }

    /* SIGN_REQUEST round trip - proves the agent actually
     * holds the private side, not just the public listing. */
    challenge := []byte("wolfci-sshagent-probe-challenge")
    var signReq bytes.Buffer
    signReq.WriteByte(wolfssh.AgentMsgSignRequest)
    signReq.Write(wolfssh.EncodeSSHString(foundBlob))
    signReq.Write(wolfssh.EncodeSSHString(challenge))
    var flags [4]byte
    signReq.Write(flags[:])
    if err := writeFramed(conn, signReq.Bytes()); err != nil {
        fmt.Fprintf(os.Stderr,
            "probe: write sign req: %v\n", err)
        os.Exit(1)
    }
    signResp, err := readFramed(conn)
    if err != nil {
        fmt.Fprintf(os.Stderr,
            "probe: read sign resp: %v\n", err)
        os.Exit(1)
    }
    if len(signResp) < 1 ||
        signResp[0] != wolfssh.AgentMsgSignResponse {
        fmt.Fprintf(os.Stderr,
            "probe: sign resp wrong msgID: %v\n", signResp)
        os.Exit(1)
    }

    if err := os.WriteFile(witness,
        []byte("OK\n"+foundComment+"\n"), 0o600); err != nil {
        fmt.Fprintf(os.Stderr,
            "probe: write witness: %v\n", err)
        os.Exit(1)
    }
    /* Echo the comment + first line to stdout too so the
     * surrounding `sh` step's captured log carries it. */
    fmt.Println("OK")
    fmt.Println(foundComment)
}

/* writeFramed writes a uint32-big-endian-length-prefixed
 * payload. */
func writeFramed(w io.Writer, body []byte) error {
    var hdr [4]byte
    binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
    if _, err := w.Write(hdr[:]); err != nil {
        return err
    }
    _, err := w.Write(body)
    return err
}

/* readFramed reads a uint32-big-endian-length-prefixed
 * payload. */
func readFramed(r io.Reader) ([]byte, error) {
    var hdr [4]byte
    if _, err := io.ReadFull(r, hdr[:]); err != nil {
        return nil, err
    }
    length := binary.BigEndian.Uint32(hdr[:])
    if length == 0 || length > 1<<20 {
        return nil, fmt.Errorf(
            "framed body length %d out of range", length)
    }
    body := make([]byte, length)
    if _, err := io.ReadFull(r, body); err != nil {
        return nil, err
    }
    return body, nil
}
