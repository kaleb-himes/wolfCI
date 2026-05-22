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

// TestServer_RecordHeartbeat gates PLAN.md 12.4. After
// RecordHeartbeat, LastHeartbeat returns the same NodeStatus
// the caller stored plus a receive timestamp within a small
// window of the wall clock.
func TestServer_RecordHeartbeat(t *testing.T) {
	svc := agentsvc.New("hb-test")
	status := &wolfciv1.NodeStatus{
		Architecture:        "darwin/arm64",
		GoVersion:           "go1.22",
		FreeDiskBytes:       1_000_000,
		HostUptimeSeconds:   3600,
		WallClockUnixMicros: time.Now().UnixMicro(),
		AgentVersion:        "v0.1.0",
	}
	before := time.Now()
	svc.RecordHeartbeat("agent-a", status)
	after := time.Now()

	got, received, ok := svc.LastHeartbeat("agent-a")
	if !ok {
		t.Fatal("LastHeartbeat ok = false after RecordHeartbeat; want true")
	}
	if got != status {
		t.Errorf("LastHeartbeat returned %p, want stored pointer %p",
			got, status)
	}
	if received.Before(before) || received.After(after) {
		t.Errorf("receive timestamp %v outside [%v, %v]",
			received, before, after)
	}
}

// TestServer_LastHeartbeatStale: an agent whose most recent
// heartbeat is older than StaleThreshold is still reachable
// via LastHeartbeat (ok=true, the data is still on file) but
// no longer appears in ConnectedAgents - the Phase 12 Nodes
// view uses ConnectedAgents for the live/offline filter.
func TestServer_LastHeartbeatStale(t *testing.T) {
	svc := agentsvc.New("hb-test")
	svc.StaleThreshold = 10 * time.Millisecond

	/* Register the agent so the heartbeat lookup has an
	 * AgentInfo to correlate with. ConnectedAgents() returns
	 * the AgentInfo, not raw heartbeat records, so an
	 * unregistered agent would be invisible regardless of
	 * heartbeat freshness.
	 */
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := svc.Register(ctx, &wolfciv1.AgentInfo{
		AgentId: "agent-stale", Executors: 1,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	svc.RecordHeartbeat("agent-stale", &wolfciv1.NodeStatus{
		Architecture: "linux/amd64",
	})

	/* LastHeartbeat must still return ok=true even after the
	 * threshold passes - the record is on file, just stale.
	 */
	time.Sleep(60 * time.Millisecond)
	if _, _, ok := svc.LastHeartbeat("agent-stale"); !ok {
		t.Error("LastHeartbeat ok = false for an agent with a stale " +
			"heartbeat; want true (record is still on file)")
	}
	for _, a := range svc.ConnectedAgents() {
		if a.AgentId == "agent-stale" {
			t.Errorf("ConnectedAgents included agent-stale despite " +
				"a stale (>StaleThreshold) heartbeat")
		}
	}
}

// TestServer_LastHeartbeatUnknownAgent: LastHeartbeat returns
// ok=false for an agent that has never sent a heartbeat,
// regardless of whether the agent has Register'd or not.
func TestServer_LastHeartbeatUnknownAgent(t *testing.T) {
	svc := agentsvc.New("hb-test")
	if _, _, ok := svc.LastHeartbeat("agent-never-here"); ok {
		t.Error("LastHeartbeat ok = true for an unknown agent; want false")
	}
}

// TestAgentSvc_BuiltInNodeRegistered gates PLAN.md 12.5. After
// RegisterBuiltInNode, Agents() must include the synthetic
// wolfci-master entry with label "master" and a single executor.
// The refresh goroutine must also publish a fresh heartbeat so
// LastHeartbeat returns ok=true and ConnectedAgents includes the
// master under the default StaleThreshold.
func TestAgentSvc_BuiltInNodeRegistered(t *testing.T) {
	svc := agentsvc.New("hb-test")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	/* Use a short refresh interval so the test does not wait
	 * the full 30s production default for the first beat.
	 */
	svc.RegisterBuiltInNode(ctx, 50*time.Millisecond, t.TempDir())

	/* AgentInfo registration is synchronous; assert immediately. */
	found := false
	for _, a := range svc.Agents() {
		if a.AgentId == agentsvc.BuiltInNodeAgentID {
			found = true
			if a.Executors != 1 {
				t.Errorf("master Executors = %d, want 1", a.Executors)
			}
			if len(a.Labels) != 1 || a.Labels[0] != "master" {
				t.Errorf("master Labels = %v, want [master]", a.Labels)
			}
		}
	}
	if !found {
		t.Fatalf("Agents() missing %q after RegisterBuiltInNode",
			agentsvc.BuiltInNodeAgentID)
	}

	/* The first heartbeat fires immediately inside the refresh
	 * goroutine, so a short poll is plenty.
	 */
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, _, ok := svc.LastHeartbeat(agentsvc.BuiltInNodeAgentID); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	status, _, ok := svc.LastHeartbeat(agentsvc.BuiltInNodeAgentID)
	if !ok {
		t.Fatal("RegisterBuiltInNode refresh goroutine did not record " +
			"a heartbeat within 1s")
	}
	if status.Architecture == "" {
		t.Error("master NodeStatus.Architecture is empty; " +
			"refresh did not call nodeinfo.Take")
	}

	/* ConnectedAgents should include the master because its
	 * heartbeat is fresh (well within the default 90s
	 * StaleThreshold).
	 */
	inConnected := false
	for _, a := range svc.ConnectedAgents() {
		if a.AgentId == agentsvc.BuiltInNodeAgentID {
			inConnected = true
		}
	}
	if !inConnected {
		t.Error("ConnectedAgents missing master despite fresh heartbeat")
	}
}
