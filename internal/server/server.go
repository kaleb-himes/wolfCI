package server

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	wolfciv1 "github.com/kaleb-himes/wolfCI/api/v1"
	"github.com/kaleb-himes/wolfCI/internal/agentsvc"
	"github.com/kaleb-himes/wolfCI/internal/auth"
	"github.com/kaleb-himes/wolfCI/internal/credstore"
	"github.com/kaleb-himes/wolfCI/internal/storage"
)

// parseJobSpec is server.go's local helper rather than a method
// on Storage so the package boundary stays narrow.
func parseJobSpec(spec string) (*storage.Job, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, fmt.Errorf("spec is empty")
	}
	var j storage.Job
	if err := yaml.Unmarshal([]byte(spec), &j); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	return &j, nil
}

func yamlMarshal(j *storage.Job) ([]byte, error) {
	return yaml.Marshal(j)
}

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

const sessionCookieName = "wolfci_session"

// Options configures the wolfCI HTTP server.
type Options struct {
	// Storage is the on-disk Job persistence layer. Required.
	Storage *storage.Storage

	// Auth holds the auth config (password toggle, PBKDF2
	// iteration count and salt length). Required.
	Auth *auth.Config

	// Passwords is the username + password store. Required when
	// Auth.PasswordEnabled is true.
	Passwords *auth.PasswordStore

	// Sessions holds active web sessions. Required.
	Sessions *SessionStore

	// CookieSecure controls the Secure flag on the session
	// cookie. True (the production default) means the browser
	// only sends the cookie over HTTPS. Set false for local
	// HTTP development and tests.
	CookieSecure bool

	// AgentSvc is the AgentService implementation; the /nodes
	// page lists agents from this registry. Optional; when nil
	// the /nodes page renders as empty.
	AgentSvc *agentsvc.Server

	// JobRunner enqueues a job into the scheduler. The Run button
	// on /jobs and the per-job page POST /jobs/<name>/run, which
	// calls Enqueue and redirects to /jobs/<name>/builds/<n>.
	// Optional; when nil the Run button is hidden and the POST
	// endpoint returns 501.
	JobRunner JobRunner

	// Credstore is the credential-store the 18.29+ job-edit
	// form consults to populate its api_credentials_id /
	// credentials_id <select> dropdowns. Optional; when nil
	// (tests that don't exercise creds, the
	// pre-credstore-era server config) the selects render
	// with just the empty "(none)" option.
	Credstore *credstore.Store

	// AgentEndpoint is the host:port a remote wolfci-agent
	// should pass to its --server-addr flag. The 19.5
	// connection-command page renders this verbatim so the
	// operator can copy the command without editing. Empty
	// is allowed (the page shows a placeholder + instruction
	// to fill it in manually) so dev-time servers without a
	// public address still surface the rest of the
	// instructions.
	AgentEndpoint string
}

// JobRunner is the surface server needs from the scheduler to wire
// the Run button. cmd/wolfci adapts scheduler.Scheduler.Enqueue to
// this shape; tests can substitute a fake.
type JobRunner interface {
	Enqueue(job *storage.Job) (buildNumber int, err error)
}

// JobRunnerFunc adapts a function into a JobRunner.
type JobRunnerFunc func(job *storage.Job) (int, error)

// Enqueue satisfies JobRunner.
func (f JobRunnerFunc) Enqueue(job *storage.Job) (int, error) { return f(job) }

// Server is the wolfCI HTTP handler tree. It satisfies
// http.Handler.
type Server struct {
	opts Options
	tmpl *template.Template
	mux  *http.ServeMux
}

// New constructs the Server.
func New(opts Options) *Server {
	tmpl := template.New("wolfci").Funcs(jobFormFuncs())
	tmpl = template.Must(tmpl.ParseFS(templatesFS, "templates/*.html"))
	s := &Server{opts: opts, tmpl: tmpl, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleRoot)
	s.mux.HandleFunc("/login", s.handleLogin)
	s.mux.HandleFunc("/logout", s.handleLogout)
	s.mux.HandleFunc("/jobs", s.requireSession(s.handleJobs))
	s.mux.HandleFunc("/jobs/", s.requireSession(s.handleJobRoutes))
	s.mux.HandleFunc("/nodes", s.requireSession(s.handleNodes))
	s.mux.HandleFunc("/nodes/", s.requireSession(s.handleNodeRoutes))
	logTail := &LogTailHandler{
		Root:         s.opts.Storage.Root(),
		PollInterval: 100 * time.Millisecond,
		IdleTimeout:  5 * time.Minute,
	}
	s.mux.HandleFunc("/api/v1/builds/", s.requireSession(logTail.ServeHTTP))
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(staticSub())))
}

// handleJobRoutes dispatches everything under /jobs/, which
// covers /jobs/new (create) and /jobs/{name}/edit (modify).
func (s *Server) handleJobRoutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/jobs/")
	switch {
	case rest == "new":
		switch r.Method {
		case http.MethodGet:
			view := normalizeView(r.URL.Query().Get("view"))
			s.renderJobForm(w, "", "", "", view, nil)
		case http.MethodPost:
			s.handleJobCreate(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	default:
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) == 0 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		name := parts[0]
		if !validJobName(name) {
			http.Error(w, "invalid job name", http.StatusBadRequest)
			return
		}
		switch {
		case len(parts) == 1:
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.handleJobDetail(w, r, name)
		case len(parts) == 2 && parts[1] == "edit":
			switch r.Method {
			case http.MethodGet:
				s.handleJobEditGet(w, r, name)
			case http.MethodPost:
				s.handleJobEditPost(w, r, name)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		case len(parts) == 2 && parts[1] == "run":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.handleJobRun(w, r, name)
		case len(parts) == 2 && parts[1] == "delete":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.handleJobDelete(w, r, name)
		case len(parts) == 2 && parts[1] == "rebuild":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.handleJobRebuild(w, r, name)
		case len(parts) == 2 && parts[1] == "rename":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.handleJobRename(w, r, name)
		case len(parts) == 2 && parts[1] == "builds":
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.handleBuildsIndex(w, r, name)
		case len(parts) == 3 && parts[1] == "builds":
			/* parts[2] may be just "<n>" (live-log page) or
			 * "<n>/ws[/...]" (workspace browser). Split on
			 * the first slash to tell them apart.
			 */
			tail := parts[2]
			slash := strings.IndexByte(tail, '/')
			numStr := tail
			sub := ""
			if slash >= 0 {
				numStr = tail[:slash]
				sub = tail[slash+1:]
			}
			num, err := strconv.Atoi(numStr)
			if err != nil || num < 1 {
				http.Error(w, "invalid build number", http.StatusBadRequest)
				return
			}
			switch {
			case sub == "":
				s.handleBuildLogPage(w, r, name, num)
			case sub == "ws" || sub == "ws/":
				s.handleWorkspace(w, r, name, num, "")
			case strings.HasPrefix(sub, "ws/"):
				s.handleWorkspace(w, r, name, num,
					strings.TrimPrefix(sub, "ws/"))
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	var registered []*wolfciv1.AgentInfo
	var connected []*wolfciv1.AgentInfo
	if s.opts.AgentSvc != nil {
		registered = s.opts.AgentSvc.Agents()
		connected = s.opts.AgentSvc.ConnectedAgents()
	}
	connectedSet := make(map[string]bool, len(connected))
	for _, a := range connected {
		connectedSet[a.AgentId] = true
	}
	rows := make([]nodeRow, 0, len(registered))
	for _, a := range registered {
		rows = append(rows, buildNodeRow(s.opts.AgentSvc, a, connectedSet))
	}
	/* Master row first, then everything else in registry order.
	 * The registry order is non-deterministic (map iteration);
	 * "master first" is the only cross-row ordering rule.
	 */
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].IsMaster != rows[j].IsMaster {
			return rows[i].IsMaster
		}
		return false
	})
	s.render(w, "nodes.html", map[string]interface{}{
		"Title": "Nodes",
		"Nodes": rows,
	})
}

// handleNodeRoutes dispatches everything under /nodes/, which
// covers /nodes/<agent-id> (detail GET), /nodes/<agent-id>/disable
// (POST), /nodes/<agent-id>/enable (POST), and the 19.2+
// /nodes/new* family (landing page, permanent-agent form,
// GCE form, copy form). PLAN.md 12.7 + 19.2.
func (s *Server) handleNodeRoutes(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/nodes/")
	if rest == "" {
		http.Redirect(w, r, "/nodes", http.StatusSeeOther)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	first := parts[0]
	if first == "" {
		http.NotFound(w, r)
		return
	}
	rest2 := ""
	if len(parts) == 2 {
		rest2 = parts[1]
	}

	/* 19.2+ /nodes/new family. Routed before agent-id
	 * detail so a literal pending agent named "new" cannot
	 * be created (validatePendingAgentName allows "new" as
	 * a shape, but the route would shadow this handler and
	 * confuse operators; reserve it). */
	if first == "new" {
		s.handleNodesNew(w, r, rest2)
		return
	}

	agentID := first
	action := rest2
	switch {
	case action == "" && r.Method == http.MethodGet:
		s.handleNodeDetail(w, r, agentID)
	case action == "disable" && r.Method == http.MethodPost:
		s.handleNodeDisable(w, r, agentID, true)
	case action == "enable" && r.Method == http.MethodPost:
		s.handleNodeDisable(w, r, agentID, false)
	default:
		http.NotFound(w, r)
	}
}

// handleNodesNew dispatches the /nodes/new* family. sub is
// the path tail after /nodes/new (empty for the landing
// page, "permanent" for the Permanent Agent form, "gce"
// for the GCE form when 19.6 lands).
func (s *Server) handleNodesNew(w http.ResponseWriter,
	r *http.Request, sub string) {

	switch sub {
	case "":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed",
				http.StatusMethodNotAllowed)
			return
		}
		s.render(w, "nodes_new.html",
			map[string]interface{}{
				"Title": "New node",
			})
	case "permanent":
		s.handleNodesNewPermanent(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleNodesNewPermanent renders + processes the
// /nodes/new/permanent form (PLAN.md 19.3). GET renders the
// form; POST validates the inputs, saves a PendingAgent,
// and redirects to /nodes/<name>.
func (s *Server) handleNodesNewPermanent(
	w http.ResponseWriter, r *http.Request) {

	switch r.Method {
	case http.MethodGet:
		s.renderNodesNewPermanent(w, "", "", nil, 1, "")
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request",
				http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		labelsRaw := r.FormValue("labels")
		execStr := strings.TrimSpace(
			r.FormValue("executors"))
		description := r.FormValue("description")

		executors := 1
		if execStr != "" {
			n, err := strconv.Atoi(execStr)
			if err != nil || n < 1 {
				s.renderNodesNewPermanent(w,
					"executors must be a positive integer",
					name, splitLines(labelsRaw),
					1, description)
				return
			}
			executors = n
		}
		labels := splitLines(labelsRaw)
		pa := &storage.PendingAgent{
			Name:        name,
			Labels:      labels,
			Executors:   executors,
			Description: description,
		}
		if err := s.opts.Storage.SaveAgent(pa); err != nil {
			s.renderNodesNewPermanent(w, err.Error(), name,
				labels, executors, description)
			return
		}
		http.Redirect(w, r, "/nodes/"+name,
			http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed",
			http.StatusMethodNotAllowed)
	}
}

// renderNodesNewPermanent renders the Permanent Agent form
// with optional error + pre-filled values (used on POST
// validation failure so the operator's typed input survives
// the re-render).
func (s *Server) renderNodesNewPermanent(
	w http.ResponseWriter, errMsg, name string,
	labels []string, executors int, description string) {

	s.render(w, "nodes_new_permanent.html",
		map[string]interface{}{
			"Title":       "New permanent agent",
			"Error":       errMsg,
			"Name":        name,
			"LabelsText":  strings.Join(labels, "\n"),
			"Executors":   executors,
			"Description": description,
		})
}

// splitLines breaks a multi-line textarea value into a list
// of trimmed non-empty entries. Operators paste with extra
// blank lines or trailing whitespace; the storage layer
// expects clean strings, so the trim happens here once.
func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// handleNodeDetail renders the per-agent page: display name,
// labels, executor count, full NodeStatus from LastHeartbeat,
// and a Take-offline / Bring-online toggle form. Recent build
// history is left to a follow-up commit; the PLAN.md 12.7 gate
// only asserts on the status fields.
func (s *Server) handleNodeDetail(w http.ResponseWriter, r *http.Request, agentID string) {
	/* PLAN.md 19.5: route pending (pre-registered but not
	 * yet connected) agents to the connection-command page.
	 * A connected agent is always present in AgentSvc.Agents
	 * after it Register()s; if the name only exists on disk
	 * as a PendingAgent, render the command + instructions
	 * the operator needs to bring it online. */
	if pa, err := s.opts.Storage.LoadPendingAgent(
		agentID); err == nil && pa != nil {
		s.handlePendingNodeDetail(w, r, pa)
		return
	}
	if s.opts.AgentSvc == nil {
		http.NotFound(w, r)
		return
	}
	var info *wolfciv1.AgentInfo
	for _, a := range s.opts.AgentSvc.Agents() {
		if a.AgentId == agentID {
			info = a
			break
		}
	}
	if info == nil {
		http.NotFound(w, r)
		return
	}
	displayName := info.AgentId
	isMaster := info.AgentId == agentsvc.BuiltInNodeAgentID
	if isMaster {
		displayName = agentsvc.BuiltInNodeDisplayName
	}

	status, received, hasHeartbeat := s.opts.AgentSvc.LastHeartbeat(agentID)
	view := map[string]interface{}{
		"Title":        displayName,
		"AgentID":      info.AgentId,
		"DisplayName":  displayName,
		"IsMaster":     isMaster,
		"Labels":       info.Labels,
		"Executors":    info.Executors,
		"Disabled":     s.opts.AgentSvc.IsDisabled(agentID),
		"HasHeartbeat": hasHeartbeat,
	}
	if hasHeartbeat && status != nil {
		view["Architecture"] = status.Architecture
		view["GoVersion"] = status.GoVersion
		view["AgentVersion"] = status.AgentVersion
		view["FreeDisk"] = humanBytes(status.FreeDiskBytes)
		view["FreeSwap"] = humanBytes(status.FreeSwapBytes)
		view["FreeTemp"] = humanBytes(status.FreeTempBytes)
		view["HostUptime"] = formatUptime(status.HostUptimeSeconds)
		view["ClockDiff"] = formatClockDiff(
			status.WallClockUnixMicros, received)
	}
	s.render(w, "node_detail.html", view)
}

// handleNodeDisable toggles the in-memory disabled flag for
// agentID and redirects back to the detail page. POST-only so a
// bookmark or accidental GET cannot flip the flag.
//
// Permission note: the Phase 12.7 spec calls for the
// nodes.configure permission to gate this endpoint; matrix-
// driven HTTP authz has not landed yet (the matrix exists in
// internal/authz but is not wired into requireSession), so for
// now requireSession is the only gate. Wiring NodesConfigure is
// tracked under the broader authz follow-up.
// handlePendingNodeDetail renders the
// /nodes/<name>-when-pending page from PLAN.md 19.5. The
// page shows the wolfci-agent command line the operator
// copies onto the remote machine plus a short paragraph on
// how to provision the cert material the agent needs to
// authenticate to the server's mTLS listener.
func (s *Server) handlePendingNodeDetail(
	w http.ResponseWriter, r *http.Request,
	pa *storage.PendingAgent) {

	endpoint := s.opts.AgentEndpoint
	endpointMissing := endpoint == ""
	if endpointMissing {
		/* Placeholder shown literally in the command; the
		 * SERVER_HOST:PORT bracket-shape names the variable
		 * without bringing in HTML-special characters that
		 * html/template would escape into the rendered
		 * page. */
		endpoint = "SERVER_HOST:PORT"
	}
	command := fmt.Sprintf(
		"wolfci-agent --server-addr %s --agent-id %s "+
			"--cert-dir /etc/wolfci-agent",
		endpoint, pa.Name)
	s.render(w, "nodes_pending_detail.html",
		map[string]interface{}{
			"Title":           pa.Name,
			"Name":            pa.Name,
			"Labels":          pa.Labels,
			"Executors":       pa.Executors,
			"Description":     pa.Description,
			"CreatedAt":       pa.CreatedAt,
			"AgentEndpoint":   endpoint,
			"EndpointMissing": endpointMissing,
			"Command":         command,
		})
}

func (s *Server) handleNodeDisable(w http.ResponseWriter, r *http.Request,
	agentID string, disable bool) {

	if s.opts.AgentSvc == nil {
		http.NotFound(w, r)
		return
	}
	found := false
	for _, a := range s.opts.AgentSvc.Agents() {
		if a.AgentId == agentID {
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	s.opts.AgentSvc.SetDisabled(agentID, disable)
	http.Redirect(w, r, "/nodes/"+agentID, http.StatusSeeOther)
}

// formatUptime renders host_uptime_seconds in the dominant unit
// for readability: "5d 2h", "2h 14m", "47m", "9s". The detail
// page shows this on the master row and on agent rows that have
// reported a heartbeat.
func formatUptime(secs int64) string {
	if secs <= 0 {
		return ""
	}
	d := secs / 86400
	secs -= d * 86400
	h := secs / 3600
	secs -= h * 3600
	m := secs / 60
	secs -= m * 60
	if d > 0 {
		return fmt.Sprintf("%dd %dh", d, h)
	}
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%ds", secs)
}

/* nodeRow is the per-row view model the /nodes template renders.
 * One per registered agent. Empty-string columns mean "metric
 * unavailable" and render as an em-dash in the template.
 */
type nodeRow struct {
	AgentID      string
	DisplayName  string
	IsMaster     bool

	/* Status is "ok" (connected, fresh heartbeat), "offline"
	 * (registered but heartbeat is stale or never arrived), or
	 * "na" (no heartbeat ever recorded - pre-first-beat state
	 * that should be visually distinct from a true offline).
	 */
	Status string

	Architecture string
	ClockDiff    string
	FreeDisk     string
	FreeSwap     string
	FreeTemp     string
	GoVersion    string
	ResponseTime string
	AgentVersion string
}

/* buildNodeRow fills a nodeRow for one registered agent. Reads
 * LastHeartbeat off the AgentSvc to populate the per-snapshot
 * columns; missing data renders as an em-dash via the template's
 * "or" pipeline rather than a literal "0" that could read as a
 * legitimate metric value.
 */
func buildNodeRow(svc *agentsvc.Server, a *wolfciv1.AgentInfo,
	connectedSet map[string]bool) nodeRow {

	row := nodeRow{
		AgentID:     a.AgentId,
		DisplayName: a.AgentId,
	}
	if a.AgentId == agentsvc.BuiltInNodeAgentID {
		row.DisplayName = agentsvc.BuiltInNodeDisplayName
		row.IsMaster = true
	}

	var status *wolfciv1.NodeStatus
	var received time.Time
	var hasHeartbeat bool
	if svc != nil {
		status, received, hasHeartbeat = svc.LastHeartbeat(a.AgentId)
	}

	switch {
	case !hasHeartbeat:
		row.Status = "na"
	case connectedSet[a.AgentId]:
		row.Status = "ok"
	default:
		row.Status = "offline"
	}

	if status != nil {
		row.Architecture = status.Architecture
		row.GoVersion = status.GoVersion
		row.AgentVersion = status.AgentVersion
		row.FreeDisk = humanBytes(status.FreeDiskBytes)
		row.FreeSwap = humanBytes(status.FreeSwapBytes)
		row.FreeTemp = humanBytes(status.FreeTempBytes)
		row.ClockDiff = formatClockDiff(status.WallClockUnixMicros,
			received)
	}
	if row.IsMaster {
		/* Master heartbeat is in-process; no round-trip, no
		 * clock skew.
		 */
		row.ClockDiff = "in sync"
		row.ResponseTime = "0ms"
	}
	/* Non-master ResponseTime stays empty; the heartbeat protocol
	 * is one-way today, so a real round-trip latency requires
	 * the Phase 12-decisions ping/ack extension that has not
	 * landed yet. The template renders "" as an em-dash so the
	 * column reads as "metric not available" rather than 0ms.
	 */
	return row
}

/* humanBytes formats n as a binary-prefix byte count, e.g.
 *   512        -> "512 B"
 *   2048       -> "2.0 KiB"
 *   1500000000 -> "1.4 GiB"
 * Zero renders as an empty string so the template's missing-
 * metric path applies; a true zero free-bytes value is
 * indistinguishable from "metric unread" at the snapshot layer.
 */
func humanBytes(n int64) string {
	if n <= 0 {
		return ""
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for cur := n / unit; cur >= unit; cur /= unit {
		div *= unit
		exp++
	}
	suffix := "KMGTPE"[exp]
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), suffix)
}

/* formatClockDiff computes the agent's wall clock minus the
 * server's receive time and renders it as a signed millisecond
 * value. |diff| < 2s renders as "in sync"; an unset (zero)
 * agent-side timestamp renders as an empty string so the
 * template falls through to the em-dash.
 */
func formatClockDiff(agentMicros int64, serverReceived time.Time) string {
	if agentMicros == 0 || serverReceived.IsZero() {
		return ""
	}
	diff := time.UnixMicro(agentMicros).Sub(serverReceived)
	ms := diff.Milliseconds()
	if ms > -2000 && ms < 2000 {
		return "in sync"
	}
	if ms >= 0 {
		return fmt.Sprintf("+%dms", ms)
	}
	return fmt.Sprintf("%dms", ms)
}

// handleJobRun enqueues the named job and redirects to the live
// build log page. The Run button on jobs.html POSTs here.
func (s *Server) handleJobRun(w http.ResponseWriter, r *http.Request, name string) {
	if s.opts.JobRunner == nil {
		http.Error(w, "job runner not configured", http.StatusNotImplemented)
		return
	}
	job, err := s.opts.Storage.LoadJob(name)
	if err != nil {
		http.Error(w, "load job: "+err.Error(), http.StatusNotFound)
		return
	}
	num, err := s.opts.JobRunner.Enqueue(job)
	if err != nil {
		http.Error(w, "enqueue: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r,
		fmt.Sprintf("/jobs/%s/builds/%d", name, num),
		http.StatusSeeOther)
}

// handleBuildLogPage renders the live-tailing page for a
// specific (job, build) pair. The page embeds JavaScript that
// connects to /api/v1/builds/{job}/{n}/log via EventSource and
// appends decoded chunks to a <pre> element.
func (s *Server) handleBuildLogPage(w http.ResponseWriter, r *http.Request, name string, num int) {
	s.render(w, "buildlog.html", map[string]interface{}{
		"Title":     fmt.Sprintf("Build %d - %s", num, name),
		"JobName":   name,
		"BuildNum":  num,
		"StreamURL": fmt.Sprintf("/api/v1/builds/%s/%d/log", name, num),
		"CanRun":    s.opts.JobRunner != nil,
	})
}

func (s *Server) handleJobCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	view := normalizeView(r.FormValue("view"))
	job, spec, err := s.parseJobFromRequest(r, view)
	if err != nil {
		s.renderJobForm(w, "", spec, err.Error(), view, job)
		return
	}
	if job.Name == "" {
		s.renderJobForm(w, "", spec, "spec must include 'name'", view, job)
		return
	}
	// Refuse to overwrite an existing job from the "new" route.
	if existing, _ := s.opts.Storage.LoadJob(job.Name); existing != nil {
		s.renderJobForm(w, "", spec,
			fmt.Sprintf("job %q already exists; use /jobs/%s/edit", job.Name, job.Name),
			view, job)
		return
	}
	if err := s.opts.Storage.SaveJob(job); err != nil {
		s.renderJobForm(w, "", spec, "save: "+err.Error(), view, job)
		return
	}
	http.Redirect(w, r, "/jobs", http.StatusSeeOther)
}

func (s *Server) handleJobEditGet(w http.ResponseWriter, r *http.Request, name string) {
	job, err := s.opts.Storage.LoadJob(name)
	if err != nil {
		http.Error(w, "load job: "+err.Error(), http.StatusNotFound)
		return
	}
	out, err := yamlMarshal(job)
	if err != nil {
		http.Error(w, "marshal job: "+err.Error(), http.StatusInternalServerError)
		return
	}
	view := normalizeView(r.URL.Query().Get("view"))
	s.renderJobForm(w, name, string(out), "", view, job)
}

func (s *Server) handleJobEditPost(w http.ResponseWriter, r *http.Request, name string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	view := normalizeView(r.FormValue("view"))
	job, spec, err := s.parseJobFromRequest(r, view)
	if err != nil {
		s.renderJobForm(w, name, spec, err.Error(), view, job)
		return
	}
	if job.Name != name {
		s.renderJobForm(w, name, spec,
			fmt.Sprintf("name in spec (%q) does not match URL (%q); rename is not supported via the edit form", job.Name, name),
			view, job)
		return
	}
	if err := s.opts.Storage.SaveJob(job); err != nil {
		s.renderJobForm(w, name, spec, "save: "+err.Error(), view, job)
		return
	}
	http.Redirect(w, r, "/jobs", http.StatusSeeOther)
}

// parseJobFromRequest reads either the raw YAML textarea
// (view == "raw") or the per-field form inputs (view ==
// "form") and returns the assembled storage.Job + the
// canonical YAML form (for re-rendering the raw textarea on
// validation errors). Form view marshals the assembled Job
// to YAML so the raw tab stays in sync when an operator
// flips between tabs after a failed save.
func (s *Server) parseJobFromRequest(r *http.Request,
	view string) (*storage.Job, string, error) {

	if view == "form" {
		job, err := buildJobFromForm(r)
		if err != nil {
			return job, "", err
		}
		spec, marshErr := yamlMarshal(job)
		if marshErr != nil {
			return job, "", marshErr
		}
		return job, string(spec), nil
	}
	spec := r.FormValue("spec")
	job, err := parseJobSpec(spec)
	if err != nil {
		return nil, spec, err
	}
	return job, spec, nil
}

// normalizeView clamps the ?view= query / hidden-field
// value to the two we render. Anything unrecognized -> raw.
func normalizeView(v string) string {
	if v == "form" {
		return "form"
	}
	return "raw"
}

// renderJobForm writes the create/edit form. name=="" means
// "new"; otherwise it's an edit screen for that job. view is
// "raw" or "form"; job is the parsed Job for form-view
// prefill (nil for /jobs/new on first paint).
func (s *Server) renderJobForm(w http.ResponseWriter, name,
	spec, errMsg, view string, job *storage.Job) {

	isNew := name == ""
	action := "/jobs/new"
	title := "New job"
	if !isNew {
		action = "/jobs/" + name + "/edit"
		title = "Edit job"
	}
	/* Slots is a fixed-length range the form view iterates
	 * over to render trigger rows. Kept here rather than as
	 * a template helper so the row count is a single
	 * constant shared with buildJobFromForm.
	 */
	slots := make([]int, formTriggerRows)
	for i := range slots {
		slots[i] = i
	}
	credOptions := s.credentialOptions()
	s.render(w, "jobedit.html", map[string]interface{}{
		"Title":              title,
		"IsNew":              isNew,
		"Name":               name,
		"Spec":               spec,
		"Error":              errMsg,
		"Action":             action,
		"View":               view,
		"Job":                job,
		"TriggerSlots":       slots,
		"TriggerTypeOptions": knownTriggerTypes,
		"CredentialOptions":  credOptions,
	})
}

// credentialOptions returns the sorted credential IDs the
// 18.29+ job-edit form's credential <select> dropdowns
// (api_credentials_id, credentials_id) render as options.
// Returns an empty slice when no credstore is wired or when
// the store is empty - the template still renders the select
// with the leading "(none)" option in that case.
func (s *Server) credentialOptions() []string {
	if s.opts.Credstore == nil {
		return nil
	}
	entries, err := s.opts.Credstore.List()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	sort.Strings(out)
	return out
}

// validJobName mirrors the file-system constraint storage uses
// when it builds jobs/<name>/job.yaml.
func validJobName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	return true
}

func staticSub() http.FileSystem {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// embed misconfig is a build-time bug.
		panic(err)
	}
	return http.FS(sub)
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// If already authenticated, send them straight to /jobs.
	if _, ok := s.sessionFromRequest(r); ok {
		http.Redirect(w, r, "/jobs", http.StatusSeeOther)
		return
	}
	s.renderLogin(w, "")
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	user := strings.TrimSpace(r.FormValue("username"))
	pw := r.FormValue("password")
	if user == "" || pw == "" {
		http.Error(w, "username and password are required", http.StatusBadRequest)
		return
	}
	if !s.opts.Auth.PasswordEnabled {
		http.Error(w, "password auth is disabled; contact an administrator", http.StatusUnauthorized)
		return
	}
	if err := s.opts.Passwords.VerifyPassword(user, pw); err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	sess, err := s.opts.Sessions.Create(user)
	if err != nil {
		http.Error(w, "session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sess.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.opts.CookieSecure,
		SameSite: http.SameSiteStrictMode,
		Expires:  sess.ExpiresAt,
	})
	http.Redirect(w, r, "/jobs", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookieName); err == nil {
		_ = s.opts.Sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:    sessionCookieName,
		Value:   "",
		Path:    "/",
		Expires: time.Unix(0, 0),
		MaxAge:  -1,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.opts.Storage.ListJobs()
	if err != nil {
		http.Error(w, "list jobs: "+err.Error(), http.StatusInternalServerError)
		return
	}
	counts := computeAllTriggerCounts(jobs)
	s.render(w, "jobs.html", map[string]interface{}{
		"Title":         "Jobs",
		"Jobs":          jobs,
		"CanRun":        s.opts.JobRunner != nil,
		"TriggerCounts": counts,
	})
}

func (s *Server) renderLogin(w http.ResponseWriter, errMsg string) {
	s.render(w, "login.html", map[string]interface{}{
		"Title": "Sign in",
		"Error": errMsg,
	})
}

func (s *Server) render(w http.ResponseWriter, page string, data interface{}) {
	t, err := s.tmpl.Clone()
	if err != nil {
		http.Error(w, fmt.Sprintf("template clone: %v", err), http.StatusInternalServerError)
		return
	}
	if _, err := t.ParseFS(templatesFS, "templates/"+page); err != nil {
		http.Error(w, fmt.Sprintf("template parse %s: %v", page, err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "base", data); err != nil {
		// Headers already sent; just log if needed. We can't HTTP error here.
		_ = err
	}
}

// requireSession wraps next so unauthenticated requests are
// redirected to /.
func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.sessionFromRequest(r); !ok {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// sessionFromRequest looks up the session cookie, validates it
// against the SessionStore, and returns the Session if present
// and unexpired.
func (s *Server) sessionFromRequest(r *http.Request) (*Session, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return nil, false
	}
	sess, err := s.opts.Sessions.Lookup(c.Value)
	if err != nil {
		return nil, false
	}
	return sess, true
}

// guard against errors.As unused-import lint when no errors
// usage appears above.
var _ = errors.New
