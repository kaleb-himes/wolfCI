// Package agentsvc implements the wolfCI server's side of the
// AgentService gRPC interface defined in api/v1/agent.proto.
//
// Phase 5.2b ships the Register unary RPC and an in-memory
// agent registry. Follow-on sub-tasks (5.2c, 5.2d, 5.3) wire in
// wolfSSL mTLS and the bidirectional Connect stream for job
// dispatch.
package agentsvc

import (
	"context"
	"io"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
)

// Server implements wolfciv1.AgentServiceServer.
type Server struct {
	wolfciv1.UnimplementedAgentServiceServer

	version string

	mu     sync.Mutex
	agents map[string]*wolfciv1.AgentInfo

	pendingJobs chan *wolfciv1.JobAssignment

	completedMu sync.Mutex
	completed   []*wolfciv1.BuildComplete
}

// New constructs a Server announcing serverVersion to agents
// that successfully Register.
func New(serverVersion string) *Server {
	return &Server{
		version:     serverVersion,
		agents:      make(map[string]*wolfciv1.AgentInfo),
		pendingJobs: make(chan *wolfciv1.JobAssignment, 64),
	}
}

// QueueJob makes job available for delivery to the next agent
// that opens a Connect stream. Calls are non-blocking up to the
// underlying channel capacity (64).
func (s *Server) QueueJob(job *wolfciv1.JobAssignment) {
	s.pendingJobs <- job
}

// Completed returns a snapshot of every BuildComplete the
// server has received from agents. Caller owns the slice.
func (s *Server) Completed() []*wolfciv1.BuildComplete {
	s.completedMu.Lock()
	defer s.completedMu.Unlock()
	out := make([]*wolfciv1.BuildComplete, len(s.completed))
	copy(out, s.completed)
	return out
}

func (s *Server) recordCompletion(c *wolfciv1.BuildComplete) {
	s.completedMu.Lock()
	defer s.completedMu.Unlock()
	s.completed = append(s.completed, c)
}

// Register records an agent's self-described capabilities and
// returns the server's response. Validation errors come back as
// gRPC InvalidArgument status codes; gRPC clients see them as
// typed errors via status.FromError.
func (s *Server) Register(ctx context.Context, info *wolfciv1.AgentInfo) (*wolfciv1.RegisterResponse, error) {
	if info == nil {
		return nil, status.Error(codes.InvalidArgument, "agentsvc.Register: AgentInfo is nil")
	}
	if info.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agentsvc.Register: AgentInfo.agent_id is required")
	}
	if info.Executors < 1 {
		return nil, status.Errorf(codes.InvalidArgument, "agentsvc.Register: AgentInfo.executors must be >= 1 (got %d)", info.Executors)
	}

	s.mu.Lock()
	// Store a defensive copy so later proto mutations by the caller
	// do not leak into the registry.
	stored := &wolfciv1.AgentInfo{
		AgentId:   info.AgentId,
		Executors: info.Executors,
		Labels:    append([]string(nil), info.Labels...),
	}
	s.agents[info.AgentId] = stored
	s.mu.Unlock()

	return &wolfciv1.RegisterResponse{
		ServerVersion: s.version,
		Accepted:      true,
	}, nil
}

// Connect is the bidirectional stream the server uses to push
// JobAssignments to an agent and to receive LogChunk and
// BuildComplete messages back. A sender goroutine pumps queued
// JobAssignments down the stream; the receiver loop demuxes
// incoming AgentMessages.
func (s *Server) Connect(stream wolfciv1.AgentService_ConnectServer) error {
	ctx := stream.Context()
	done := make(chan struct{})
	defer close(done)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case job := <-s.pendingJobs:
				msg := &wolfciv1.ServerMessage{
					Body: &wolfciv1.ServerMessage_Assignment{Assignment: job},
				}
				if err := stream.Send(msg); err != nil {
					return
				}
			}
		}
	}()

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if c := msg.GetComplete(); c != nil {
			s.recordCompletion(c)
		}
		// LogChunks are accepted but currently dropped; persistent
		// log routing lands in a follow-on iteration alongside
		// internal/storage builds/<job>/<n>/log streaming.
	}
}

// Agents returns a snapshot of every currently-registered agent,
// in non-deterministic order. The returned slice is owned by the
// caller; mutating it does not affect the registry.
func (s *Server) Agents() []*wolfciv1.AgentInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*wolfciv1.AgentInfo, 0, len(s.agents))
	for _, a := range s.agents {
		out = append(out, a)
	}
	return out
}
