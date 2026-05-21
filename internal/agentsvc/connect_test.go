package agentsvc_test

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/testcerts"
	"github.com/kaleb-himes/wolfCI/internal/tlsutil"
)

// TestAgentService_ConnectStream is the gating test for the
// PLAN.md 5.3 Connect bidirectional stream. It sets up the same
// wolfSSL mTLS gRPC bridge as TestAgentService_RegisterOverMTLS,
// pre-queues a JobAssignment on the server, opens the Connect
// stream from the client, expects the assignment to arrive,
// sends back a BuildComplete, and verifies the server recorded
// the completion.
func TestAgentService_ConnectStream(t *testing.T) {
	chain := testcerts.NewMTLSChain(t)

	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer inner.Close()

	serverLn, err := tlsutil.NewListener(inner, &tlsutil.Config{
		Certificate: chain.ServerCert,
		Key:         chain.ServerKey,
		ClientCAs:   chain.CACert,
		MinVersion:  tls.VersionTLS13,
	})
	if err != nil {
		t.Fatalf("tlsutil.NewListener: %v", err)
	}
	defer serverLn.Close()

	svc := agentsvc.New("v1-stream-test")

	job := &wolfciv1.JobAssignment{
		JobName:     "demo",
		BuildNumber: 1,
		Steps:       []*wolfciv1.Step{{Shell: "echo hello"}},
	}
	svc.QueueJob(job)

	grpcSrv := grpc.NewServer()
	wolfciv1.RegisterAgentServiceServer(grpcSrv, svc)
	go func() { _ = grpcSrv.Serve(serverLn) }()
	defer grpcSrv.Stop()

	conn, err := grpc.Dial(
		inner.Addr().String(),
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
	defer conn.Close()

	client := wolfciv1.NewAgentServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("first Recv: %v", err)
	}
	assign := msg.GetAssignment()
	if assign == nil {
		t.Fatalf("expected ServerMessage_Assignment, got %+v", msg)
	}
	if assign.JobName != "demo" {
		t.Errorf("JobName = %q, want demo", assign.JobName)
	}
	if assign.BuildNumber != 1 {
		t.Errorf("BuildNumber = %d, want 1", assign.BuildNumber)
	}
	if len(assign.Steps) != 1 || assign.Steps[0].Shell != "echo hello" {
		t.Errorf("Steps = %+v, want one step running 'echo hello'", assign.Steps)
	}

	if err := stream.Send(&wolfciv1.AgentMessage{
		Body: &wolfciv1.AgentMessage_Complete{
			Complete: &wolfciv1.BuildComplete{
				BuildNumber: 1,
				Status:      "success",
				ExitCode:    0,
			},
		},
	}); err != nil {
		t.Fatalf("Send BuildComplete: %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("CloseSend: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		completed := svc.Completed()
		if len(completed) == 1 && completed[0].BuildNumber == 1 && completed[0].Status == "success" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server did not record completion within timeout; got %d", len(svc.Completed()))
}
