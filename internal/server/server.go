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
	tmpl := template.Must(template.ParseFS(templatesFS, "templates/*.html"))
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
			s.renderJobForm(w, "", "", "")
		case http.MethodPost:
			s.handleJobCreate(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	default:
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) < 2 {
			http.NotFound(w, r)
			return
		}
		name := parts[0]
		if !validJobName(name) {
			http.Error(w, "invalid job name", http.StatusBadRequest)
			return
		}
		switch {
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
		case len(parts) == 3 && parts[1] == "builds":
			num, err := strconv.Atoi(parts[2])
			if err != nil || num < 1 {
				http.Error(w, "invalid build number", http.StatusBadRequest)
				return
			}
			s.handleBuildLogPage(w, r, name, num)
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
	})
}

func (s *Server) handleJobCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	spec := r.FormValue("spec")
	job, err := parseJobSpec(spec)
	if err != nil {
		s.renderJobForm(w, "", spec, err.Error())
		return
	}
	if job.Name == "" {
		s.renderJobForm(w, "", spec, "spec must include 'name'")
		return
	}
	// Refuse to overwrite an existing job from the "new" route.
	if existing, _ := s.opts.Storage.LoadJob(job.Name); existing != nil {
		s.renderJobForm(w, "", spec, fmt.Sprintf("job %q already exists; use /jobs/%s/edit", job.Name, job.Name))
		return
	}
	if err := s.opts.Storage.SaveJob(job); err != nil {
		s.renderJobForm(w, "", spec, "save: "+err.Error())
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
	s.renderJobForm(w, name, string(out), "")
}

func (s *Server) handleJobEditPost(w http.ResponseWriter, r *http.Request, name string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	spec := r.FormValue("spec")
	job, err := parseJobSpec(spec)
	if err != nil {
		s.renderJobForm(w, name, spec, err.Error())
		return
	}
	if job.Name != name {
		s.renderJobForm(w, name, spec, fmt.Sprintf("name in spec (%q) does not match URL (%q); rename is not supported via the edit form", job.Name, name))
		return
	}
	if err := s.opts.Storage.SaveJob(job); err != nil {
		s.renderJobForm(w, name, spec, "save: "+err.Error())
		return
	}
	http.Redirect(w, r, "/jobs", http.StatusSeeOther)
}

// renderJobForm writes the create/edit form. name=="" means
// "new"; otherwise it's an edit screen for that job.
func (s *Server) renderJobForm(w http.ResponseWriter, name, spec, errMsg string) {
	isNew := name == ""
	action := "/jobs/new"
	title := "New job"
	if !isNew {
		action = "/jobs/" + name + "/edit"
		title = "Edit job"
	}
	s.render(w, "jobedit.html", map[string]interface{}{
		"Title":  title,
		"IsNew":  isNew,
		"Name":   name,
		"Spec":   spec,
		"Error":  errMsg,
		"Action": action,
	})
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
	s.render(w, "jobs.html", map[string]interface{}{
		"Title":  "Jobs",
		"Jobs":   jobs,
		"CanRun": s.opts.JobRunner != nil,
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
