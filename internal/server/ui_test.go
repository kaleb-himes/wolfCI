package server_test

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kaleb-himes/wolfCI/internal/auth"
	"github.com/kaleb-himes/wolfCI/internal/server"
	"github.com/kaleb-himes/wolfCI/internal/storage"
)

// TestUI_LoginPageAndJobsList is the gating test for PLAN.md
// task 6.1. It verifies:
//   - GET / returns a login page (form, hand-written CSS link).
//   - POST /login with valid password credentials creates a
//     session and redirects to /jobs.
//   - GET /jobs with the session cookie returns the job list.
//   - GET /jobs without a session redirects back to /.
func TestUI_LoginPageAndJobsList(t *testing.T) {
	dir := t.TempDir()
	st, err := storage.New(dir)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	if err := st.SaveJob(&storage.Job{
		Name:        "build-all",
		Description: "compile everything",
		Steps:       []storage.Step{{Shell: "make"}},
	}); err != nil {
		t.Fatalf("SaveJob: %v", err)
	}

	authCfg := &auth.Config{PasswordEnabled: true, BcryptCost: 4}
	pwDir := filepath.Join(dir, "passwords")
	pwStore := auth.NewPasswordStore(pwDir, authCfg)
	if err := pwStore.SetPassword("alice", "hunter2"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	sessionsDir := filepath.Join(dir, "sessions")
	sessions := server.NewSessionStore(sessionsDir, 1*time.Hour)

	srv := server.New(server.Options{
		Storage:      st,
		Auth:         authCfg,
		Passwords:    pwStore,
		Sessions:     sessions,
		CookieSecure: false, // httptest uses HTTP
	})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	// 1) Login page.
	resp, err := client.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	loginBody := bodyString(t, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{"<form", "name=\"username\"", "name=\"password\"", "/static/app.css"} {
		if !strings.Contains(loginBody, want) {
			t.Errorf("GET / body missing %q", want)
		}
	}

	// 2) Unauthenticated GET /jobs redirects to /.
	resp, err = client.Get(ts.URL + "/jobs")
	if err != nil {
		t.Fatalf("GET /jobs (unauth): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound {
		t.Fatalf("GET /jobs (unauth) status = %d, want 303 or 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("GET /jobs (unauth) Location = %q, want /", loc)
	}

	// 3) POST /login with bad creds.
	resp, err = client.PostForm(ts.URL+"/login", url.Values{
		"username": {"alice"},
		"password": {"wrong"},
	})
	if err != nil {
		t.Fatalf("POST /login (bad): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("POST /login (bad) status = %d, want 401", resp.StatusCode)
	}

	// 4) POST /login with good creds: redirect + cookie.
	resp, err = client.PostForm(ts.URL+"/login", url.Values{
		"username": {"alice"},
		"password": {"hunter2"},
	})
	if err != nil {
		t.Fatalf("POST /login (good): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /login (good) status = %d, want 303", resp.StatusCode)
	}
	cookies := jar.Cookies(mustParseURL(ts.URL))
	hasSession := false
	for _, c := range cookies {
		if c.Name == "wolfci_session" && c.Value != "" {
			hasSession = true
		}
	}
	if !hasSession {
		t.Fatalf("expected wolfci_session cookie, got %v", cookies)
	}

	// 5) Authenticated GET /jobs.
	resp, err = client.Get(ts.URL + "/jobs")
	if err != nil {
		t.Fatalf("GET /jobs (auth): %v", err)
	}
	body := bodyString(t, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /jobs status = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{"build-all", "compile everything", "/static/app.css"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET /jobs body missing %q", want)
		}
	}
}

func bodyString(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
