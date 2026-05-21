package server

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

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

	// Auth holds the auth config (password toggle, bcrypt cost).
	// Required.
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
}

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
		"Title": "Jobs",
		"Jobs":  jobs,
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
