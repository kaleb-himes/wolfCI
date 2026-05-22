package wolfciv1_test

/* Gates for PLAN.md 12.2. NodeStatus carries the host-metrics
 * snapshot from internal/nodeinfo across the wire on every
 * agent's Connect heartbeat; the AgentMessage oneof grows a
 * heartbeat variant so the existing stream can carry it without
 * a second RPC.
 */

import (
    "testing"

    "google.golang.org/protobuf/proto"

    wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
)

func TestProto_NodeStatusRoundtrip(t *testing.T) {
    /* Every field set to a distinct non-zero value so a wire-
     * marshal regression that silently drops one shows up as a
     * concrete mismatch rather than a 0 == 0 false-positive.
     */
    src := &wolfciv1.NodeStatus{
        Architecture:        "darwin/arm64",
        GoVersion:           "go1.21.0",
        FreeDiskBytes:       12345678901,
        FreeSwapBytes:       987654321,
        FreeTempBytes:       456789012,
        HostUptimeSeconds:   86400,
        WallClockUnixMicros: 1735689600000000,
        AgentVersion:        "v0.1.0-7-gabcdef0",
    }

    wire, err := proto.Marshal(src)
    if err != nil {
        t.Fatalf("Marshal: %v", err)
    }

    var dst wolfciv1.NodeStatus
    if err := proto.Unmarshal(wire, &dst); err != nil {
        t.Fatalf("Unmarshal: %v", err)
    }

    if !proto.Equal(src, &dst) {
        t.Errorf("NodeStatus did not round-trip\nsrc: %+v\ndst: %+v",
            src, &dst)
    }
}

func TestProto_AgentMessageHeartbeatVariant(t *testing.T) {
    /* Wrap a Heartbeat in an AgentMessage and assert the oneof
     * selector survives wire encoding. A regression where the
     * variant tag drops would surface as a nil body or a
     * mis-tagged Log/Complete on the receiving end.
     */
    src := &wolfciv1.AgentMessage{
        Body: &wolfciv1.AgentMessage_Heartbeat{
            Heartbeat: &wolfciv1.Heartbeat{
                Status: &wolfciv1.NodeStatus{
                    Architecture:        "linux/amd64",
                    GoVersion:           "go1.22.0",
                    FreeDiskBytes:       1_000_000_000,
                    HostUptimeSeconds:   3600,
                    WallClockUnixMicros: 1735689600000000,
                    AgentVersion:        "v0.1.0",
                },
            },
        },
    }

    wire, err := proto.Marshal(src)
    if err != nil {
        t.Fatalf("Marshal: %v", err)
    }

    var dst wolfciv1.AgentMessage
    if err := proto.Unmarshal(wire, &dst); err != nil {
        t.Fatalf("Unmarshal: %v", err)
    }

    /* Oneof selector check: the body must be the Heartbeat
     * variant, not Log or Complete.
     */
    hb, ok := dst.Body.(*wolfciv1.AgentMessage_Heartbeat)
    if !ok {
        t.Fatalf("Body type = %T, want *AgentMessage_Heartbeat",
            dst.Body)
    }
    if hb.Heartbeat == nil {
        t.Fatal("Heartbeat is nil")
    }
    if hb.Heartbeat.Status == nil {
        t.Fatal("Heartbeat.Status is nil")
    }
    if got := hb.Heartbeat.Status.Architecture; got != "linux/amd64" {
        t.Errorf("Status.Architecture = %q, want linux/amd64", got)
    }
    if got := hb.Heartbeat.Status.AgentVersion; got != "v0.1.0" {
        t.Errorf("Status.AgentVersion = %q, want v0.1.0", got)
    }
}
