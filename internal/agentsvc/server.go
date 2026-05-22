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
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
	"github.com/kaleb-himes/wolfCI/internal/nodeinfo"
)

// BuiltInNodeAgentID is the synthetic agent_id for the wolfCI
// master node (PLAN.md 12.5). Kept as a wire identifier so the
// authz matrix and scheduler do not need a special case; the
// /nodes UI renders the row with BuiltInNodeDisplayName instead.
const BuiltInNodeAgentID = "wolfci-master"

// BuiltInNodeDisplayName is the human-readable label the Phase
// 12.6 Nodes UI renders for the master row.
const BuiltInNodeDisplayName = "wolfCI Master Node"

// BuiltInNodeLabel is the label the master row advertises. The
// scheduler's Router (Phase 5.5) can match jobs that explicitly
// require running on the master via this label.
const BuiltInNodeLabel = "master"

// DefaultStaleThreshold is how long ConnectedAgents waits before
// dropping an agent whose heartbeat has gone silent. Matches the
// Jenkins "offline" badge heuristic closely enough to read the
// same way to operators familiar with Jenkins's UI; 90s also
// gives the agent's default 30s heartbeat interval (Phase 12.3)
// two missed beats of grace.
const DefaultStaleThreshold = 90 * time.Second

// AgentIDMetadataKey is the gRPC metadata key the agent uses to
// identify itself when opening a Connect stream. The value must
// match the AgentId the agent passed to Register.
const AgentIDMetadataKey = "agent-id"

// LogSink receives LogChunk bytes streamed from agents during
// build execution. Implementations are typically file-backed
// (one log.live per build) or in-memory (for tests).
type LogSink interface {
	WriteLogChunk(jobName string, buildNum int, data []byte)
}

// Server implements wolfciv1.AgentServiceServer.
type Server struct {
	wolfciv1.UnimplementedAgentServiceServer

	version string
	logSink LogSink

	// StaleThreshold is the maximum age of a heartbeat record
	// before ConnectedAgents drops the agent from its result.
	// Zero means use DefaultStaleThreshold (90s). Exported so
	// tests can shorten the window without sleeping for 90s.
	StaleThreshold time.Duration

	// WorkDir is the wolfCI storage root (same path passed to
	// storage.New). GetArtifact resolves
	// builds/<job>/<n>/artifacts/<basename> against it. Empty
	// disables GetArtifact (the handler returns
	// FailedPrecondition). Set by cmd/wolfci at startup.
	// Phase 15.5.
	WorkDir string

	mu     sync.Mutex
	agents map[string]*wolfciv1.AgentInfo

	pendingJobs chan *wolfciv1.JobAssignment

	streamsMu sync.Mutex
	streams   map[string]*agentStream

	pendingMu sync.Mutex
	pending   map[pendingKey]*assignmentInFlight

	completedMu sync.Mutex
	completed   []*wolfciv1.BuildComplete

	heartbeatsMu sync.Mutex
	heartbeats   map[string]heartbeatRecord

	disabledMu sync.Mutex
	disabled   map[string]bool
}

// heartbeatRecord is the per-agent latest NodeStatus + the
// wall-clock time the server received it.
type heartbeatRecord struct {
	status   *wolfciv1.NodeStatus
	received time.Time
}

// pendingKey identifies an outstanding SubmitAndWait that wants
// the BuildComplete for (agent, build_number) when it arrives.
type pendingKey struct {
	agentID     string
	buildNumber int32
}

// assignmentInFlight is the per-pending-job record: the job_name
// (so LogChunks can be routed to the right log file) and the
// channel SubmitAndWait blocks on.
type assignmentInFlight struct {
	jobName string
	done    chan *wolfciv1.BuildComplete
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
		pending:     make(map[pendingKey]*assignmentInFlight),
		heartbeats:  make(map[string]heartbeatRecord),
		disabled:    make(map[string]bool),
	}
}

// SetDisabled marks (or unmarks) agentID as administratively
// offline. Disabled agents are skipped by IdleAgentWithLabel
// and by the scheduler Router's local-path master check
// (PLAN.md 12.7); the AgentInfo stays in the registry and on
// the /nodes view so the operator can see and reverse the
// state. Idempotent.
func (s *Server) SetDisabled(agentID string, disabled bool) {
	s.disabledMu.Lock()
	defer s.disabledMu.Unlock()
	if disabled {
		s.disabled[agentID] = true
	} else {
		delete(s.disabled, agentID)
	}
}

// IsDisabled reports whether agentID has been administratively
// taken offline via SetDisabled. Always false for agents that
// have never been disabled.
func (s *Server) IsDisabled(agentID string) bool {
	s.disabledMu.Lock()
	defer s.disabledMu.Unlock()
	return s.disabled[agentID]
}

// RegisterBuiltInNode inserts the synthetic wolfCI master node
// into the agent registry and starts a goroutine that refreshes
// its NodeStatus from internal/nodeinfo every interval. The
// goroutine takes the first snapshot synchronously before
// returning so callers (notably the Phase 12.5 cmd/wolfci main)
// can rely on LastHeartbeat being populated immediately.
//
// ctx scopes the refresh goroutine; when ctx fires the goroutine
// exits and no further heartbeats are recorded. The master entry
// itself stays in Agents() (matches Jenkins's "Built-In Node"
// row, which is visible even when the controller is shutting
// down).
//
// statfsRoot is the path the inner nodeinfo.Take statfs against
// for FreeDiskBytes. cmd/wolfci passes its work_dir; tests pass
// t.TempDir.
func (s *Server) RegisterBuiltInNode(ctx context.Context, interval time.Duration, statfsRoot string) {
	info := &wolfciv1.AgentInfo{
		AgentId:   BuiltInNodeAgentID,
		Labels:    []string{BuiltInNodeLabel},
		Executors: 1,
	}
	s.mu.Lock()
	s.agents[BuiltInNodeAgentID] = info
	s.mu.Unlock()

	/* Synchronous first beat so callers see a populated
	 * LastHeartbeat immediately on return; subsequent beats
	 * happen on the ticker.
	 */
	s.refreshBuiltInNode(statfsRoot)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.refreshBuiltInNode(statfsRoot)
			}
		}
	}()
}

// refreshBuiltInNode takes a fresh nodeinfo snapshot, maps it to
// NodeStatus, and records it via the same path agents use. A
// partial snapshot (statfs failure on root, sysctl error, etc.)
// still gets recorded because the master row should never look
// silently dead just because one metric was unreadable.
func (s *Server) refreshBuiltInNode(statfsRoot string) {
	snap, _ := nodeinfo.Take(statfsRoot)
	status := &wolfciv1.NodeStatus{
		Architecture:        snap.Architecture,
		GoVersion:           snap.GoVersion,
		FreeDiskBytes:       snap.FreeDiskBytes,
		FreeSwapBytes:       snap.FreeSwapBytes,
		FreeTempBytes:       snap.FreeTempBytes,
		HostUptimeSeconds:   int64(snap.HostUptime / time.Second),
		WallClockUnixMicros: snap.Now.UnixMicro(),
		AgentVersion:        s.version,
	}
	s.RecordHeartbeat(BuiltInNodeAgentID, status)
}

// RecordHeartbeat stores the latest NodeStatus for agentID and
// stamps the receive time at the current wall clock. PLAN.md
// 12.4: the server's Connect recv loop calls this on every
// AgentMessage_Heartbeat; cmd/wolfci's built-in master node
// (12.5) calls it directly from a self-refresh goroutine.
// Passing a nil status is a no-op rather than an error: the
// only documented caller is the recv loop, which already
// nil-checks the oneof variant.
func (s *Server) RecordHeartbeat(agentID string, status *wolfciv1.NodeStatus) {
	if status == nil {
		return
	}
	s.heartbeatsMu.Lock()
	defer s.heartbeatsMu.Unlock()
	s.heartbeats[agentID] = heartbeatRecord{
		status:   status,
		received: time.Now(),
	}
}

// LastHeartbeat returns the most recent NodeStatus stored for
// agentID along with the wall-clock time it was received. ok is
// false when the agent has never sent (or had recorded for it)
// a heartbeat. The record is preserved past StaleThreshold so
// callers can still display the last-known metrics on an
// offline-badged row.
func (s *Server) LastHeartbeat(agentID string) (*wolfciv1.NodeStatus, time.Time, bool) {
	s.heartbeatsMu.Lock()
	defer s.heartbeatsMu.Unlock()
	rec, ok := s.heartbeats[agentID]
	if !ok {
		return nil, time.Time{}, false
	}
	return rec.status, rec.received, true
}

// staleThreshold returns the configured StaleThreshold or the
// default if the field was left as the zero value.
func (s *Server) staleThreshold() time.Duration {
	if s.StaleThreshold > 0 {
		return s.StaleThreshold
	}
	return DefaultStaleThreshold
}

// SetLogSink registers a sink for LogChunk messages received
// from agents. nil disables streaming (the current default).
// Set this before agents start connecting if you care about
// every chunk.
func (s *Server) SetLogSink(sink LogSink) {
	s.logSink = sink
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

// SubmitAndWait sends job to agentID and blocks until the agent
// reports a matching BuildComplete or ctx is cancelled. The
// caller is responsible for ensuring job.BuildNumber is unique
// per (agent, in-flight build) so the pending map can route the
// response back.
func (s *Server) SubmitAndWait(ctx context.Context, agentID string, job *wolfciv1.JobAssignment) (*wolfciv1.BuildComplete, error) {
	if job == nil {
		return nil, errors.New("agentsvc.SubmitAndWait: nil JobAssignment")
	}
	key := pendingKey{agentID: agentID, buildNumber: job.BuildNumber}
	af := &assignmentInFlight{
		jobName: job.JobName,
		done:    make(chan *wolfciv1.BuildComplete, 1),
	}

	s.pendingMu.Lock()
	if _, exists := s.pending[key]; exists {
		s.pendingMu.Unlock()
		return nil, fmt.Errorf("agentsvc.SubmitAndWait: already waiting on (%s, %d)",
			agentID, job.BuildNumber)
	}
	s.pending[key] = af
	s.pendingMu.Unlock()

	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, key)
		s.pendingMu.Unlock()
	}()

	if err := s.AssignJob(agentID, job); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case bc := <-af.done:
		return bc, nil
	}
}

// lookupJobName returns the job_name registered for the given
// (agent_id, build_number) in-flight assignment, or "" if no
// SubmitAndWait is tracking that pair.
func (s *Server) lookupJobName(agentID string, buildNum int32) string {
	key := pendingKey{agentID: agentID, buildNumber: buildNum}
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if af, ok := s.pending[key]; ok {
		return af.jobName
	}
	return ""
}

// IdleAgentWithLabel returns the agent_id of any currently-
// connected agent that advertises the given label, skipping
// any agent that has been administratively disabled via
// SetDisabled (PLAN.md 12.7). Returns the empty string if no
// match. Phase 5.5a treats "connected" as "idle"; per-agent
// job-busy tracking lands in 5.5b.
func (s *Server) IdleAgentWithLabel(label string) string {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	for id, st := range s.streams {
		if s.IsDisabled(id) {
			continue
		}
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

// ConnectedAgents returns a snapshot of every agent whose most
// recent heartbeat is younger than StaleThreshold. PLAN.md 12.4
// switched this from "open stream right now" to a heartbeat-
// derived liveness check so the result survives transient gRPC
// stream reconnects and matches Jenkins's "offline" badge
// heuristic. Agents that have not yet recorded a heartbeat are
// also absent; the UI handles that case via LastHeartbeat
// returning ok=false (rendering an "N/A" status badge instead
// of dropping the row entirely).
func (s *Server) ConnectedAgents() []*wolfciv1.AgentInfo {
	cutoff := time.Now().Add(-s.staleThreshold())

	s.heartbeatsMu.Lock()
	fresh := make(map[string]struct{}, len(s.heartbeats))
	for id, rec := range s.heartbeats {
		if rec.received.After(cutoff) {
			fresh[id] = struct{}{}
		}
	}
	s.heartbeatsMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*wolfciv1.AgentInfo, 0, len(fresh))
	for id := range fresh {
		if info, ok := s.agents[id]; ok {
			out = append(out, info)
		}
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

// deliverCompletion routes a BuildComplete to any waiting
// SubmitAndWait call. If no caller is waiting, the message is
// dropped silently (recordCompletion already stored it for the
// audit-style Completed() snapshot).
func (s *Server) deliverCompletion(agentID string, c *wolfciv1.BuildComplete) {
	key := pendingKey{agentID: agentID, buildNumber: c.BuildNumber}
	s.pendingMu.Lock()
	af, ok := s.pending[key]
	if ok {
		delete(s.pending, key)
	}
	s.pendingMu.Unlock()
	if ok {
		af.done <- c
	}
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
			s.deliverCompletion(agentID, c)
		}
		if l := msg.GetLog(); l != nil && s.logSink != nil {
			jobName := s.lookupJobName(agentID, l.BuildNumber)
			s.logSink.WriteLogChunk(jobName, int(l.BuildNumber), l.Data)
		}
		if hb := msg.GetHeartbeat(); hb != nil {
			s.RecordHeartbeat(agentID, hb.Status)
		}
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
