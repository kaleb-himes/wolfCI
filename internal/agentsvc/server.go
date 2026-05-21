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
}

// New constructs a Server announcing serverVersion to agents
// that successfully Register.
func New(serverVersion string) *Server {
	return &Server{
		version: serverVersion,
		agents:  make(map[string]*wolfciv1.AgentInfo),
	}
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
