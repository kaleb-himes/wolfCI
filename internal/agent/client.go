package agent

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/scheduler"
	"github.com/kaleb-himes/wolfCI/internal/storage"
	"github.com/kaleb-himes/wolfCI/internal/tlsutil"
)

// Client is the agent-side runtime. It dials the wolfCI server
// via wolfSSL mTLS, registers, opens the Connect stream, and
// dispatches received JobAssignments to a LocalExecutor whose
// build outputs land under cfg.WorkDir.
type Client struct {
	cfg      *Config
	store    *storage.Storage
	executor scheduler.Executor

	streamMu sync.Mutex // gRPC server-stream Send is single-threaded
}

// NewClient constructs a Client. The work dir from cfg becomes
// the storage root for the LocalExecutor.
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
		cfg:      cfg,
		store:    store,
		executor: scheduler.NewLocalExecutor(store),
	}, nil
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

	return c.processStream(ctx, stream)
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
		MinVersion:  tls.VersionTLS13,
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
	result := c.executor.Execute(ctx, job, int(a.BuildNumber))

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
