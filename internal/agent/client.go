package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/nodeinfo"
	"github.com/kaleb-himes/wolfCI/internal/scheduler"
	"github.com/kaleb-himes/wolfCI/internal/storage"
	"github.com/kaleb-himes/wolfCI/internal/tlsutil"
)

// defaultAgentVersion is the value Client reports in
// NodeStatus.agent_version when nothing else has been set. Phase
// 12.8 will inject a build-stamped version via -ldflags into
// cmd/wolfci-agent's main, which calls Client.SetVersion to
// override this default.
const defaultAgentVersion = "dev"

// Client is the agent-side runtime. It dials the wolfCI server
// via wolfSSL mTLS, registers, opens the Connect stream, and
// dispatches received JobAssignments to a LocalExecutor whose
// build outputs land under cfg.WorkDir. Step output streams
// back to the server as LogChunks while the build runs (Phase
// 5.7).
type Client struct {
	cfg   *Config
	store *storage.Storage

	streamMu sync.Mutex // gRPC server-stream Send is single-threaded

	// version is reported in NodeStatus.agent_version on every
	// heartbeat. Defaults to "dev"; cmd/wolfci-agent's main
	// overrides it via SetVersion with the -ldflags-injected
	// build stamp (Phase 12.8).
	version string
}

// NewClient constructs a Client. The work dir from cfg becomes
// the storage root for the per-assignment LocalExecutor.
func NewClient(cfg *Config) (*Client, error) {
	if cfg == nil {
		return nil, errors.New("agent.NewClient: nil Config")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("agent.NewClient: %w", err)
	}
	store, err := storage.New(cfg.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("agent.NewClient: storage.New: %w", err)
	}
	if err := os.MkdirAll(cfg.WorkDir, 0o755); err != nil {
		return nil, fmt.Errorf("agent.NewClient: mkdir work_dir: %w", err)
	}
	return &Client{
		cfg:     cfg,
		store:   store,
		version: defaultAgentVersion,
	}, nil
}

// SetVersion overrides the agent_version string reported on
// every NodeStatus heartbeat. cmd/wolfci-agent's main calls this
// with the -ldflags-injected build stamp (Phase 12.8); tests
// typically leave the default "dev".
func (c *Client) SetVersion(v string) {
	if v == "" {
		return
	}
	c.version = v
}

// Run dials the server, registers, opens the Connect stream,
// and processes JobAssignments until ctx is cancelled or the
// stream ends.
func (c *Client) Run(ctx context.Context) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return fmt.Errorf("agent.Run: dial: %w", err)
	}
	defer conn.Close()

	grpcClient := wolfciv1.NewAgentServiceClient(conn)

	regResp, err := grpcClient.Register(ctx, &wolfciv1.AgentInfo{
		AgentId:   c.cfg.AgentID,
		Labels:    c.cfg.Labels,
		Executors: int32(c.cfg.Executors),
	})
	if err != nil {
		return fmt.Errorf("agent.Run: Register: %w", err)
	}
	if !regResp.Accepted {
		return fmt.Errorf("agent.Run: server rejected agent %q", c.cfg.AgentID)
	}

	streamCtx := metadata.NewOutgoingContext(ctx, metadata.New(map[string]string{
		agentsvc.AgentIDMetadataKey: c.cfg.AgentID,
	}))
	stream, err := grpcClient.Connect(streamCtx)
	if err != nil {
		return fmt.Errorf("agent.Run: Connect: %w", err)
	}

	/* Heartbeat emitter (PLAN.md 12.3). The goroutine sends a
	 * NodeStatus snapshot on stream.Send under the same
	 * streamMu the assignment-completion path uses; gRPC client
	 * streams are not concurrency-safe for Send. The first
	 * heartbeat fires immediately so the server stamps a
	 * fresh "last seen" before the first ticker interval
	 * elapses; subsequent beats happen on the ticker.
	 */
	go c.heartbeatLoop(ctx, stream)

	return c.processStream(ctx, stream)
}

// heartbeatLoop runs until ctx is cancelled, emitting a NodeStatus
// Heartbeat on stream every HeartbeatTickInterval. It tolerates a
// failing internal/nodeinfo read (unsupported GOOS, missing
// /proc, ...) by sending the partial snapshot the package
// returned alongside the error - the server still sees the
// agent's wall clock and "I am alive" signal.
func (c *Client) heartbeatLoop(ctx context.Context, stream wolfciv1.AgentService_ConnectClient) {
	interval, _ := c.cfg.HeartbeatTickInterval()
	c.sendHeartbeat(stream)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.sendHeartbeat(stream)
		}
	}
}

// sendHeartbeat takes a fresh nodeinfo.Snapshot (rooted at the
// agent's work directory so FreeDiskBytes statfs's the wolfCI
// build partition) and ships it inside an AgentMessage_Heartbeat.
// Send errors are swallowed because the server-side stream
// teardown reaches processStream first via stream.Recv returning
// io.EOF; a Send race with that teardown is expected, not an
// error worth surfacing.
func (c *Client) sendHeartbeat(stream wolfciv1.AgentService_ConnectClient) {
	snap, _ := nodeinfo.Take(c.cfg.WorkDir)
	status := &wolfciv1.NodeStatus{
		Architecture:        snap.Architecture,
		GoVersion:           snap.GoVersion,
		FreeDiskBytes:       snap.FreeDiskBytes,
		FreeSwapBytes:       snap.FreeSwapBytes,
		FreeTempBytes:       snap.FreeTempBytes,
		HostUptimeSeconds:   int64(snap.HostUptime / time.Second),
		WallClockUnixMicros: snap.Now.UnixMicro(),
		AgentVersion:        c.version,
	}
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	_ = stream.Send(&wolfciv1.AgentMessage{
		Body: &wolfciv1.AgentMessage_Heartbeat{
			Heartbeat: &wolfciv1.Heartbeat{Status: status},
		},
	})
}

func (c *Client) dial(ctx context.Context) (*grpc.ClientConn, error) {
	cert, err := os.ReadFile(c.cfg.Certificate)
	if err != nil {
		return nil, fmt.Errorf("read certificate %s: %w", c.cfg.Certificate, err)
	}
	key, err := os.ReadFile(c.cfg.Key)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %w", c.cfg.Key, err)
	}
	ca, err := os.ReadFile(c.cfg.CACertificate)
	if err != nil {
		return nil, fmt.Errorf("read ca_certificate %s: %w", c.cfg.CACertificate, err)
	}

	tlsCfg := &tlsutil.Config{
		Certificate: cert,
		Key:         key,
		RootCAs:     ca,
		MinVersion:  tlsutil.VersionTLS13,
	}

	return grpc.DialContext(ctx,
		c.cfg.ServerAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return tlsutil.Dial("tcp", addr, tlsCfg)
		}),
	)
}

func (c *Client) processStream(ctx context.Context, stream wolfciv1.AgentService_ConnectClient) error {
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("agent.processStream: Recv: %w", err)
		}

		if assign := msg.GetAssignment(); assign != nil {
			go c.runAssignment(ctx, stream, assign)
		}
	}
}

func (c *Client) runAssignment(ctx context.Context, stream wolfciv1.AgentService_ConnectClient, a *wolfciv1.JobAssignment) {
	job := protoToStorageJob(a)

	onLog := func(_ string, buildNum int, data []byte) {
		c.streamMu.Lock()
		defer c.streamMu.Unlock()
		_ = stream.Send(&wolfciv1.AgentMessage{
			Body: &wolfciv1.AgentMessage_Log{
				Log: &wolfciv1.LogChunk{
					BuildNumber: int32(buildNum),
					Data:        append([]byte(nil), data...),
				},
			},
		})
	}
	exec := scheduler.NewLocalExecutorWithLogSink(c.store, onLog)
	result := exec.Execute(ctx, job, int(a.BuildNumber))

	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	_ = stream.Send(&wolfciv1.AgentMessage{
		Body: &wolfciv1.AgentMessage_Complete{
			Complete: &wolfciv1.BuildComplete{
				BuildNumber: a.BuildNumber,
				Status:      string(result.Status),
				ExitCode:    int32(result.ExitCode),
				Error:       result.Error,
			},
		},
	})
}

func protoToStorageJob(a *wolfciv1.JobAssignment) *storage.Job {
	job := &storage.Job{Name: a.JobName}
	for _, s := range a.Steps {
		job.Steps = append(job.Steps, storage.Step{
			Name:  s.Name,
			Shell: s.Shell,
			Env:   s.Env,
		})
	}
	return job
}
