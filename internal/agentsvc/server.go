// Package agentsvc implements the wolfCI server's side of the
// AgentService gRPC interface defined in api/v1/agent.proto.
//
// Per-agent routing (Phase 5.5a): each open Connect stream is
// keyed on the agent_id metadata the client sends with the call.
// AssignJob targets a specific agent's stream; IdleAgentWithLabel
// helps the scheduler pick one. QueueJob remains a broadcast
// fallback that the first available stream drains.
package agentsvc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
)

// AgentIDMetadataKey is the gRPC metadata key the agent uses to
// identify itself when opening a Connect stream. The value must
// match the AgentId the agent passed to Register.
const AgentIDMetadataKey = "agent-id"

// Server implements wolfciv1.AgentServiceServer.
type Server struct {
	wolfciv1.UnimplementedAgentServiceServer

	version string

	mu     sync.Mutex
	agents map[string]*wolfciv1.AgentInfo

	pendingJobs chan *wolfciv1.JobAssignment

	streamsMu sync.Mutex
	streams   map[string]*agentStream

	completedMu sync.Mutex
	completed   []*wolfciv1.BuildComplete
}

// agentStream is the server-side handle on one connected agent.
type agentStream struct {
	info *wolfciv1.AgentInfo
	send chan *wolfciv1.ServerMessage
	ctx  context.Context
}

// New constructs a Server announcing serverVersion to agents
// that successfully Register.
func New(serverVersion string) *Server {
	return &Server{
		version:     serverVersion,
		agents:      make(map[string]*wolfciv1.AgentInfo),
		pendingJobs: make(chan *wolfciv1.JobAssignment, 64),
		streams:     make(map[string]*agentStream),
	}
}

// QueueJob makes job available for delivery to the next agent
// that opens a Connect stream and drains the global queue.
// AssignJob is the targeted equivalent; prefer it for
// label-aware routing.
func (s *Server) QueueJob(job *wolfciv1.JobAssignment) {
	s.pendingJobs <- job
}

// AssignJob sends job specifically to the named agent's Connect
// stream. Returns an error if the agent has no open stream.
func (s *Server) AssignJob(agentID string, job *wolfciv1.JobAssignment) error {
	s.streamsMu.Lock()
	st, ok := s.streams[agentID]
	s.streamsMu.Unlock()
	if !ok {
		return fmt.Errorf("agentsvc.AssignJob: agent %q not connected", agentID)
	}
	msg := &wolfciv1.ServerMessage{
		Body: &wolfciv1.ServerMessage_Assignment{Assignment: job},
	}
	select {
	case st.send <- msg:
		return nil
	case <-st.ctx.Done():
		return fmt.Errorf("agentsvc.AssignJob: agent %q stream closed: %w", agentID, st.ctx.Err())
	}
}

// IdleAgentWithLabel returns the agent_id of any currently-
// connected agent that advertises the given label. Returns the
// empty string if no match. Phase 5.5a treats "connected" as
// "idle"; per-agent job-busy tracking lands in 5.5b.
func (s *Server) IdleAgentWithLabel(label string) string {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	for id, st := range s.streams {
		if label == "" {
			return id
		}
		for _, l := range st.info.Labels {
			if l == label {
				return id
			}
		}
	}
	return ""
}

// ConnectedAgents returns a snapshot of every agent that
// currently has an open Connect stream.
func (s *Server) ConnectedAgents() []*wolfciv1.AgentInfo {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	out := make([]*wolfciv1.AgentInfo, 0, len(s.streams))
	for _, st := range s.streams {
		out = append(out, st.info)
	}
	return out
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
// returns the server's response.
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
// BuildComplete messages back. The agent must include its
// agent_id in gRPC metadata under AgentIDMetadataKey; the
// agent_id must have previously called Register.
func (s *Server) Connect(stream wolfciv1.AgentService_ConnectServer) error {
	ctx := stream.Context()

	agentID, err := agentIDFromMetadata(ctx)
	if err != nil {
		return status.Error(codes.Unauthenticated, err.Error())
	}

	s.mu.Lock()
	info, registered := s.agents[agentID]
	s.mu.Unlock()
	if !registered {
		return status.Errorf(codes.FailedPrecondition,
			"agentsvc.Connect: agent %q must Register before Connect", agentID)
	}

	st := &agentStream{
		info: info,
		send: make(chan *wolfciv1.ServerMessage, 16),
		ctx:  ctx,
	}

	s.streamsMu.Lock()
	if _, exists := s.streams[agentID]; exists {
		s.streamsMu.Unlock()
		return status.Errorf(codes.AlreadyExists,
			"agentsvc.Connect: agent %q already has an open stream", agentID)
	}
	s.streams[agentID] = st
	s.streamsMu.Unlock()
	defer func() {
		s.streamsMu.Lock()
		delete(s.streams, agentID)
		s.streamsMu.Unlock()
	}()

	done := make(chan struct{})
	defer close(done)

	// Sender goroutine: pump the per-agent send channel AND the
	// global broadcast queue (so QueueJob keeps working for
	// "deliver to any" callers).
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case msg := <-st.send:
				if err := stream.Send(msg); err != nil {
					return
				}
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
		// LogChunks are accepted but currently dropped; live log
		// streaming lands in Phase 5.7.
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

func agentIDFromMetadata(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", errors.New("missing gRPC metadata")
	}
	values := md.Get(AgentIDMetadataKey)
	if len(values) == 0 || values[0] == "" {
		return "", fmt.Errorf("missing or empty %q metadata", AgentIDMetadataKey)
	}
	return values[0], nil
}
