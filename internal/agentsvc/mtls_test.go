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

// TestAgentService_RegisterOverMTLS is the gating test for
// PLAN.md task 5.2c. It stands up a wolfSSL-backed gRPC server
// that REQUIRES client certificates signed by a known CA, then
// has a wolfSSL-backed gRPC client (the agent's side) complete
// the mTLS handshake and issue a Register RPC.
//
// The dial path goes via grpc.WithContextDialer so gRPC's
// transport sees an already-encrypted net.Conn; gRPC itself uses
// insecure credentials because the encryption is below it.
func TestAgentService_RegisterOverMTLS(t *testing.T) {
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

	grpcSrv := grpc.NewServer()
	wolfciv1.RegisterAgentServiceServer(grpcSrv, agentsvc.New("v1-mtls-test"))
	go func() { _ = grpcSrv.Serve(serverLn) }()
	defer grpcSrv.Stop()

	dialer := func(ctx context.Context, addr string) (net.Conn, error) {
		return tlsutil.Dial("tcp", addr, &tlsutil.Config{
			Certificate: chain.AgentCert,
			Key:         chain.AgentKey,
			RootCAs:     chain.CACert,
			MinVersion:  tls.VersionTLS13,
		})
	}

	conn, err := grpc.Dial(
		inner.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer),
	)
	if err != nil {
		t.Fatalf("grpc.Dial: %v", err)
	}
	defer conn.Close()

	client := wolfciv1.NewAgentServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.Register(ctx, &wolfciv1.AgentInfo{
		AgentId:   "agent-mtls-1",
		Labels:    []string{"linux"},
		Executors: 2,
	})
	if err != nil {
		t.Fatalf("Register over mTLS: %v", err)
	}
	if !resp.Accepted {
		t.Error("Accepted = false, want true")
	}
	if resp.ServerVersion != "v1-mtls-test" {
		t.Errorf("ServerVersion = %q, want v1-mtls-test", resp.ServerVersion)
	}
}
