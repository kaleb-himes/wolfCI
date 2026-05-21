// Package cliservice implements the server side of the
// CLIService gRPC interface used by wolfci-ctl. It is distinct
// from agentsvc so the cert-CN -> matrix permission check can
// gate humans separately from build agents.
package cliservice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	cliv1 "github.com/kaleb-himes/wolfCI/api/v1/cli"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/storage"
)

// Enqueuer is the surface cliservice needs from the scheduler
// to satisfy RunJob. Returns the assigned build number; the
// completion channel from scheduler.Scheduler.Enqueue is not
// needed here because RunJob is fire-and-forget. Use
// EnqueuerFunc to adapt scheduler.Scheduler.Enqueue.
type Enqueuer interface {
	Enqueue(job *storage.Job) (buildNumber int, err error)
}

// EnqueuerFunc adapts a function into an Enqueuer.
type EnqueuerFunc func(job *storage.Job) (int, error)

// Enqueue satisfies Enqueuer.
func (f EnqueuerFunc) Enqueue(job *storage.Job) (int, error) { return f(job) }

// Server implements cliv1.CLIServiceServer.
type Server struct {
	cliv1.UnimplementedCLIServiceServer

	storage  *storage.Storage
	agentSvc *agentsvc.Server
	enqueuer Enqueuer

	// LogPollInterval is how often StreamBuildLog checks for new
	// bytes when the file is currently at EOF. Defaults to
	// 100ms; tests may override.
	LogPollInterval time.Duration

	// LogIdleTimeout closes a StreamBuildLog stream after this
	// long with no new output. Defaults to 5 minutes; zero
	// disables the auto-close.
	LogIdleTimeout time.Duration
}

// New constructs a CLIService backed by the given on-disk
// storage and agent registry. agentSvc may be nil; ListNodes
// returns an empty response in that case.
func New(st *storage.Storage, svc *agentsvc.Server) *Server {
	return &Server{
		storage:         st,
		agentSvc:        svc,
		LogPollInterval: 100 * time.Millisecond,
		LogIdleTimeout:  5 * time.Minute,
	}
}

// WithEnqueuer registers a scheduler-like Enqueuer so RunJob
// works. Returns the server for chaining.
func (s *Server) WithEnqueuer(e Enqueuer) *Server {
	s.enqueuer = e
	return s
}

// ListJobs walks storage.ListJobs and projects each Job onto
// the wire shape (name + description + node_label + step
// count).
func (s *Server) ListJobs(ctx context.Context, _ *cliv1.Empty) (*cliv1.ListJobsResponse, error) {
	jobs, err := s.storage.ListJobs()
	if err != nil {
		return nil, err
	}
	resp := &cliv1.ListJobsResponse{}
	for _, j := range jobs {
		resp.Jobs = append(resp.Jobs, &cliv1.Job{
			Name:        j.Name,
			Description: j.Description,
			NodeLabel:   j.NodeLabel,
			StepCount:   int32(len(j.Steps)),
		})
	}
	return resp, nil
}

// ListNodes returns every Register'd agent annotated with
// whether they currently have an open Connect stream.
func (s *Server) ListNodes(ctx context.Context, _ *cliv1.Empty) (*cliv1.ListNodesResponse, error) {
	resp := &cliv1.ListNodesResponse{}
	if s.agentSvc == nil {
		return resp, nil
	}
	registered := s.agentSvc.Agents()
	connectedSet := make(map[string]bool)
	for _, a := range s.agentSvc.ConnectedAgents() {
		connectedSet[a.AgentId] = true
	}
	for _, a := range registered {
		resp.Nodes = append(resp.Nodes, &cliv1.Node{
			AgentId:   a.AgentId,
			Labels:    a.Labels,
			Executors: a.Executors,
			Connected: connectedSet[a.AgentId],
		})
	}
	return resp, nil
}

// RunJob loads the named Job and asks the configured Enqueuer
// to schedule a build. Returns the assigned build number.
func (s *Server) RunJob(ctx context.Context, req *cliv1.RunJobRequest) (*cliv1.RunJobResponse, error) {
	if s.enqueuer == nil {
		return nil, fmt.Errorf("cliservice.RunJob: no scheduler configured")
	}
	if err := validateJobName(req.JobName); err != nil {
		return nil, err
	}
	job, err := s.storage.LoadJob(req.JobName)
	if err != nil {
		return nil, fmt.Errorf("cliservice.RunJob: load %q: %w", req.JobName, err)
	}
	num, err := s.enqueuer.Enqueue(job)
	if err != nil {
		return nil, fmt.Errorf("cliservice.RunJob: enqueue: %w", err)
	}
	return &cliv1.RunJobResponse{BuildNumber: int32(num)}, nil
}

// StreamBuildLog opens a server-streaming RPC that reads
// builds/<job>/<n>/log.live and emits each chunk to the
// client as it lands. Polling-based; the client cancels ctx
// to terminate.
func (s *Server) StreamBuildLog(req *cliv1.BuildLogRequest, stream cliv1.CLIService_StreamBuildLogServer) error {
	if err := validateJobName(req.JobName); err != nil {
		return err
	}
	if req.BuildNumber < 1 {
		return fmt.Errorf("cliservice.StreamBuildLog: build_number must be >= 1")
	}
	path := filepath.Join(s.storage.Root(), "builds", req.JobName, strconv.Itoa(int(req.BuildNumber)), "log.live")

	if err := waitForFile(stream.Context(), path, s.LogPollInterval, 2*time.Second); err != nil {
		// Log never appeared; return an empty stream rather than
		// an error so the client can decide what to do.
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cliservice.StreamBuildLog: open %s: %w", path, err)
	}
	defer f.Close()

	buf := make([]byte, 4096)
	lastActivity := time.Now()
	for {
		select {
		case <-stream.Context().Done():
			return nil
		default:
		}
		n, readErr := f.Read(buf)
		if n > 0 {
			if err := stream.Send(&cliv1.LogLine{Data: append([]byte(nil), buf[:n]...)}); err != nil {
				return err
			}
			lastActivity = time.Now()
			continue
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if s.LogIdleTimeout > 0 && time.Since(lastActivity) > s.LogIdleTimeout {
			return nil
		}
		select {
		case <-stream.Context().Done():
			return nil
		case <-time.After(s.LogPollInterval):
		}
	}
}

func waitForFile(ctx context.Context, path string, poll, deadline time.Duration) error {
	until := time.Now().Add(deadline)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if time.Now().After(until) {
			return fmt.Errorf("waitForFile: %s never appeared", path)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

func validateJobName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("cliservice: invalid job_name %q", name)
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("cliservice: invalid job_name %q", name)
	}
	return nil
}
