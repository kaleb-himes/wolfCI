package agentsvc_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
)

// TestAgentService_RegisterRoundtrip is the gating test for
// PLAN.md task 5.2b. It stands up a gRPC server on an ephemeral
// port (no TLS yet; the wolfSSL mTLS bridge is task 5.2c), dials
// it with the generated AgentServiceClient, and asserts that
// Register returns an accepted response carrying the server
// version. The server-side state must also reflect the new
// agent in its in-memory registry.
func TestAgentService_RegisterRoundtrip(t *testing.T) {
	svc := agentsvc.New("v1-test")

	grpcSrv := grpc.NewServer()
	wolfciv1.RegisterAgentServiceServer(grpcSrv, svc)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer lis.Close()

	serverErr := make(chan error, 1)
	go func() { serverErr <- grpcSrv.Serve(lis) }()
	defer grpcSrv.Stop()

	conn, err := grpc.Dial(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.Dial: %v", err)
	}
	defer conn.Close()

	client := wolfciv1.NewAgentServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := client.Register(ctx, &wolfciv1.AgentInfo{
		AgentId:   "agent-1",
		Labels:    []string{"linux", "x86_64"},
		Executors: 4,
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !resp.Accepted {
		t.Error("Accepted = false, want true")
	}
	if resp.ServerVersion != "v1-test" {
		t.Errorf("ServerVersion = %q, want v1-test", resp.ServerVersion)
	}

	agents := svc.Agents()
	if len(agents) != 1 || agents[0].AgentId != "agent-1" {
		t.Errorf("Agents() = %v, want one agent-1", agents)
	}
	if len(agents[0].Labels) != 2 || agents[0].Labels[0] != "linux" || agents[0].Labels[1] != "x86_64" {
		t.Errorf("Agents()[0].Labels = %v, want [linux x86_64]", agents[0].Labels)
	}
}

// TestAgentService_Register_Validation goes through Register
// directly (no gRPC round trip) and verifies each invalid-input
// path returns an error.
func TestAgentService_Register_Validation(t *testing.T) {
	svc := agentsvc.New("v1-test")
	ctx := context.Background()

	cases := []struct {
		name string
		info *wolfciv1.AgentInfo
	}{
		{"nil info", nil},
		{"empty agent_id", &wolfciv1.AgentInfo{Executors: 1}},
		{"zero executors", &wolfciv1.AgentInfo{AgentId: "a", Executors: 0}},
		{"negative executors", &wolfciv1.AgentInfo{AgentId: "a", Executors: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Register(ctx, tc.info); err == nil {
				t.Errorf("Register(%+v): nil error, want error", tc.info)
			}
		})
	}
}
