package agentsvc_test

import (
	"context"
	"crypto/tls"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/testcerts"
	"github.com/kaleb-himes/wolfCI/internal/tlsutil"
)

// TestServer_AssignJob_TargetsAgent verifies that AssignJob
// delivers a job to the specific agent it names, not to any
// random connected agent.
func TestServer_AssignJob_TargetsAgent(t *testing.T) {
	svc, addr, cleanup := dispatchTestServer(t)
	defer cleanup()

	conn, client := dispatchTestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.Register(ctx, &wolfciv1.AgentInfo{
		AgentId: "linux-agent", Executors: 1, Labels: []string{"linux"},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	streamCtx := metadata.NewOutgoingContext(ctx, metadata.New(map[string]string{
		agentsvc.AgentIDMetadataKey: "linux-agent",
	}))
	stream, err := client.Connect(streamCtx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Give the server a moment to register the stream.
	if err := waitFor(2*time.Second, func() bool {
		return len(svc.ConnectedAgents()) == 1
	}); err != nil {
		t.Fatal(err)
	}

	job := &wolfciv1.JobAssignment{JobName: "demo", BuildNumber: 1}
	if err := svc.AssignJob("linux-agent", job); err != nil {
		t.Fatalf("AssignJob: %v", err)
	}

	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if got := msg.GetAssignment(); got == nil || got.JobName != "demo" {
		t.Fatalf("expected JobAssignment(demo), got %+v", msg)
	}
}

// TestServer_AssignJob_UnknownAgent returns an error for an
// agent_id with no open stream.
func TestServer_AssignJob_UnknownAgent(t *testing.T) {
	svc := agentsvc.New("dispatch-test")
	err := svc.AssignJob("ghost", &wolfciv1.JobAssignment{JobName: "demo", BuildNumber: 1})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("AssignJob(ghost): %v, want error mentioning ghost", err)
	}
}

// TestServer_IdleAgentWithLabel selects agents by their declared
// labels.
func TestServer_IdleAgentWithLabel(t *testing.T) {
	svc, addr, cleanup := dispatchTestServer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type wired struct {
		agentID string
		labels  []string
		conn    *grpc.ClientConn
		stream  wolfciv1.AgentService_ConnectClient
	}
	wireAgent := func(agentID string, labels []string) *wired {
		conn, client := dispatchTestClient(t, addr)
		if _, err := client.Register(ctx, &wolfciv1.AgentInfo{
			AgentId: agentID, Executors: 1, Labels: labels,
		}); err != nil {
			conn.Close()
			t.Fatalf("Register(%s): %v", agentID, err)
		}
		streamCtx := metadata.NewOutgoingContext(ctx, metadata.New(map[string]string{
			agentsvc.AgentIDMetadataKey: agentID,
		}))
		stream, err := client.Connect(streamCtx)
		if err != nil {
			conn.Close()
			t.Fatalf("Connect(%s): %v", agentID, err)
		}
		return &wired{agentID: agentID, labels: labels, conn: conn, stream: stream}
	}

	a := wireAgent("agent-linux", []string{"linux", "x86_64"})
	b := wireAgent("agent-macos", []string{"macos", "arm64"})
	defer a.conn.Close()
	defer b.conn.Close()

	if err := waitFor(2*time.Second, func() bool {
		return len(svc.ConnectedAgents()) == 2
	}); err != nil {
		t.Fatal(err)
	}

	if got := svc.IdleAgentWithLabel("linux"); got != "agent-linux" {
		t.Errorf("IdleAgentWithLabel(linux) = %q, want agent-linux", got)
	}
	if got := svc.IdleAgentWithLabel("macos"); got != "agent-macos" {
		t.Errorf("IdleAgentWithLabel(macos) = %q, want agent-macos", got)
	}
	if got := svc.IdleAgentWithLabel("windows"); got != "" {
		t.Errorf("IdleAgentWithLabel(windows) = %q, want empty", got)
	}
}

// TestServer_ConnectRequiresMetadata: opening Connect without
// the agent-id metadata returns Unauthenticated.
func TestServer_ConnectRequiresMetadata(t *testing.T) {
	_, addr, cleanup := dispatchTestServer(t)
	defer cleanup()

	conn, client := dispatchTestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect (call): %v", err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("Recv: expected error from missing metadata, got nil")
	} else if st, ok := status.FromError(err); ok && st.Code() != codes.Unauthenticated {
		t.Errorf("Recv: code = %s, want Unauthenticated", st.Code())
	}
}

// TestServer_ConnectRequiresPriorRegister: opening Connect for
// an agent_id that did not Register returns FailedPrecondition.
func TestServer_ConnectRequiresPriorRegister(t *testing.T) {
	_, addr, cleanup := dispatchTestServer(t)
	defer cleanup()

	conn, client := dispatchTestClient(t, addr)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	streamCtx := metadata.NewOutgoingContext(ctx, metadata.New(map[string]string{
		agentsvc.AgentIDMetadataKey: "never-registered",
	}))
	stream, err := client.Connect(streamCtx)
	if err != nil {
		t.Fatalf("Connect (call): %v", err)
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("Recv: expected error from un-registered agent, got nil")
	} else if st, ok := status.FromError(err); ok && st.Code() != codes.FailedPrecondition {
		t.Errorf("Recv: code = %s, want FailedPrecondition", st.Code())
	}
}

func dispatchTestServer(t *testing.T) (*agentsvc.Server, string, func()) {
	t.Helper()
	chain := testcerts.NewMTLSChain(t)
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	serverLn, err := tlsutil.NewListener(inner, &tlsutil.Config{
		Certificate: chain.ServerCert,
		Key:         chain.ServerKey,
		ClientCAs:   chain.CACert,
		MinVersion:  tls.VersionTLS13,
	})
	if err != nil {
		t.Fatalf("tlsutil.NewListener: %v", err)
	}
	svc := agentsvc.New("dispatch-test")
	grpcSrv := grpc.NewServer()
	wolfciv1.RegisterAgentServiceServer(grpcSrv, svc)
	go func() { _ = grpcSrv.Serve(serverLn) }()

	addr := inner.Addr().String()
	// Stash the chain on the server so test clients can dial.
	dispatchTestChain[svc] = chain

	cleanup := func() {
		grpcSrv.Stop()
		_ = serverLn.Close()
		_ = inner.Close()
		delete(dispatchTestChain, svc)
	}
	return svc, addr, cleanup
}

// dispatchTestChain ties a Server back to the MTLSChain used to
// stand it up, so the matching client helper can grab the right
// certificate material without each test passing it explicitly.
var dispatchTestChain = map[*agentsvc.Server]testcerts.MTLSChain{}

func dispatchTestClient(t *testing.T, addr string) (*grpc.ClientConn, wolfciv1.AgentServiceClient) {
	t.Helper()
	var chain testcerts.MTLSChain
	for _, c := range dispatchTestChain {
		chain = c
		break
	}
	conn, err := grpc.Dial(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return tlsutil.Dial("tcp", addr, &tlsutil.Config{
				Certificate: chain.AgentCert,
				Key:         chain.AgentKey,
				RootCAs:     chain.CACert,
				MinVersion:  tls.VersionTLS13,
			})
		}),
	)
	if err != nil {
		t.Fatalf("grpc.Dial: %v", err)
	}
	return conn, wolfciv1.NewAgentServiceClient(conn)
}

func waitFor(timeout time.Duration, cond func() bool) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return context.DeadlineExceeded
}
