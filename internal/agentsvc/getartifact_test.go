package agentsvc_test

/* Phase 15.5 - AgentService.GetArtifact streaming RPC.
 *
 * The downstream-on-different-node case: an agent assigned a
 * build that has WOLFCI_INPUTS pointing at the upstream's
 * artifacts dir, but that dir lives on the wolfCI server (or
 * on a different agent) - the agent fetches it via this RPC.
 *
 * Tests run over plain insecure gRPC for the same reason as
 * the existing AgentService tests - mTLS is asserted by the
 * Phase 5.2c gating tests; here we focus on the handler's
 * own logic.
 */

import (
    "bytes"
    "context"
    "errors"
    "io"
    "net"
    "os"
    "path/filepath"
    "testing"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/credentials/insecure"
    "google.golang.org/grpc/metadata"
    "google.golang.org/grpc/status"

    wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
    "github.com/kaleb-himes/wolfCI/internal/agentsvc"
)

func TestGetArtifact_StreamsFile(t *testing.T) {
    dir, svc, conn := standUpGetArtifactServer(t)
    defer conn.Close()

    /* Plant builds/up/5/artifacts/bundle.bin. */
    artDir := filepath.Join(dir, "builds", "up", "5",
        "artifacts")
    if err := os.MkdirAll(artDir, 0o755); err != nil {
        t.Fatalf("mkdir: %v", err)
    }
    /* 70 KiB so the response spans at least two chunks. */
    payload := bytes.Repeat([]byte("ABCD"), 70*1024/4)
    if err := os.WriteFile(
        filepath.Join(artDir, "bundle.bin"), payload,
        0o644); err != nil {
        t.Fatalf("write: %v", err)
    }

    /* Register the agent so the authz gate lets it through. */
    ctx, cancel := context.WithTimeout(context.Background(),
        3*time.Second)
    defer cancel()
    if _, err := svc.Register(ctx, &wolfciv1.AgentInfo{
        AgentId:   "agent-down",
        Executors: 1,
    }); err != nil {
        t.Fatalf("Register: %v", err)
    }

    client := wolfciv1.NewAgentServiceClient(conn)
    callCtx := metadata.AppendToOutgoingContext(ctx,
        "wolfci-agent-id", "agent-down")
    stream, err := client.GetArtifact(callCtx,
        &wolfciv1.GetArtifactRequest{
            UpstreamJob:      "up",
            UpstreamBuild:    5,
            ArtifactBasename: "bundle.bin",
        })
    if err != nil {
        t.Fatalf("GetArtifact: %v", err)
    }
    var got bytes.Buffer
    for {
        chunk, err := stream.Recv()
        if err == io.EOF {
            break
        }
        if err != nil {
            t.Fatalf("Recv: %v", err)
        }
        got.Write(chunk.Data)
    }
    if !bytes.Equal(got.Bytes(), payload) {
        t.Errorf("streamed %d bytes, want %d (or content mismatch)",
            got.Len(), len(payload))
    }
}

func TestGetArtifact_RejectsUnauthorizedAgent(t *testing.T) {
    _, _, conn := standUpGetArtifactServer(t)
    defer conn.Close()

    client := wolfciv1.NewAgentServiceClient(conn)
    ctx, cancel := context.WithTimeout(context.Background(),
        3*time.Second)
    defer cancel()

    /* Two failure cases: no metadata at all, and metadata
     * pointing at an unregistered agent.
     */
    cases := []struct {
        name string
        ctx  context.Context
    }{
        {"no metadata", ctx},
        {"unregistered",
            metadata.AppendToOutgoingContext(ctx,
                "wolfci-agent-id", "ghost-agent")},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            stream, err := client.GetArtifact(tc.ctx,
                &wolfciv1.GetArtifactRequest{
                    UpstreamJob:      "up",
                    UpstreamBuild:    1,
                    ArtifactBasename: "anything.bin",
                })
            if err != nil {
                if !isCode(err, codes.PermissionDenied) {
                    t.Errorf("GetArtifact err = %v, want "+
                        "PermissionDenied", err)
                }
                return
            }
            _, err = stream.Recv()
            if !isCode(err, codes.PermissionDenied) {
                t.Errorf("Recv err = %v, want "+
                    "PermissionDenied", err)
            }
        })
    }
}

func TestGetArtifact_RejectsTraversal(t *testing.T) {
    dir, svc, conn := standUpGetArtifactServer(t)
    defer conn.Close()

    /* Plant a sibling file the handler MUST NOT serve. */
    sibling := filepath.Join(dir, "builds", "up", "1",
        "result.json")
    if err := os.MkdirAll(filepath.Dir(sibling), 0o755); err != nil {
        t.Fatalf("mkdir: %v", err)
    }
    if err := os.WriteFile(sibling,
        []byte(`{"secret":"SENTINEL"}`), 0o644); err != nil {
        t.Fatalf("write sibling: %v", err)
    }

    /* Register the agent so we get past the authz gate -
     * we want the traversal guard to fire on its own merits.
     */
    ctx, cancel := context.WithTimeout(context.Background(),
        3*time.Second)
    defer cancel()
    if _, err := svc.Register(ctx, &wolfciv1.AgentInfo{
        AgentId: "trav", Executors: 1,
    }); err != nil {
        t.Fatalf("Register: %v", err)
    }
    client := wolfciv1.NewAgentServiceClient(conn)
    callCtx := metadata.AppendToOutgoingContext(ctx,
        "wolfci-agent-id", "trav")

    bad := []string{
        "../result.json",
        "..%2Fresult.json",
        "subdir/file.bin",
        "..",
        "/etc/passwd",
        `dir\file`,
    }
    for _, b := range bad {
        t.Run(b, func(t *testing.T) {
            stream, err := client.GetArtifact(callCtx,
                &wolfciv1.GetArtifactRequest{
                    UpstreamJob:      "up",
                    UpstreamBuild:    1,
                    ArtifactBasename: b,
                })
            var lastErr error
            if err != nil {
                lastErr = err
            } else {
                for {
                    chunk, err := stream.Recv()
                    if err != nil {
                        lastErr = err
                        break
                    }
                    if bytes.Contains(chunk.Data,
                        []byte("SENTINEL")) {
                        t.Errorf("traversal %q leaked the "+
                            "sibling SENTINEL", b)
                    }
                }
            }
            if !isCode(lastErr, codes.InvalidArgument) {
                t.Errorf("traversal %q err = %v, "+
                    "want InvalidArgument", b, lastErr)
            }
        })
    }
}

/* standUpGetArtifactServer brings up an in-memory
 * AgentService on a local TCP port with the given WorkDir
 * set, returns the dir + svc + connected client conn for
 * the test to use. Caller defer-closes the conn; the
 * grpc.Server is stopped via t.Cleanup.
 */
func standUpGetArtifactServer(t *testing.T) (
    string, *agentsvc.Server, *grpc.ClientConn) {

    t.Helper()
    dir := t.TempDir()
    svc := agentsvc.New("gad-test")
    svc.WorkDir = dir

    grpcSrv := grpc.NewServer()
    wolfciv1.RegisterAgentServiceServer(grpcSrv, svc)

    lis, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil {
        t.Fatalf("net.Listen: %v", err)
    }
    go func() { _ = grpcSrv.Serve(lis) }()
    t.Cleanup(func() {
        grpcSrv.Stop()
        _ = lis.Close()
    })
    conn, err := grpc.Dial(lis.Addr().String(),
        grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        t.Fatalf("grpc.Dial: %v", err)
    }
    return dir, svc, conn
}

func isCode(err error, want codes.Code) bool {
    if err == nil {
        return false
    }
    if errors.Is(err, io.EOF) {
        return false
    }
    st, ok := status.FromError(err)
    if !ok {
        return false
    }
    return st.Code() == want
}
