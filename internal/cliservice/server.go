// Package cliservice implements the server side of the
// CLIService gRPC interface used by wolfci-ctl. It is distinct
// from agentsvc so the cert-CN -> matrix permission check can
// gate humans separately from build agents.
package cliservice

import (
	"context"

	cliv1 "github.com/kaleb-himes/wolfCI/api/v1/cli"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/storage"
)

// Server implements cliv1.CLIServiceServer.
type Server struct {
	cliv1.UnimplementedCLIServiceServer

	storage  *storage.Storage
	agentSvc *agentsvc.Server
}

// New constructs a CLIService backed by the given on-disk
// storage and agent registry. agentSvc may be nil; ListNodes
// returns an empty response in that case.
func New(st *storage.Storage, svc *agentsvc.Server) *Server {
	return &Server{storage: st, agentSvc: svc}
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
