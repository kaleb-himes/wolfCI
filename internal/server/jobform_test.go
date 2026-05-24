package server_test

import (
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

// TestUI_JobCreateAndEdit is the gating test for PLAN.md task
// 6.3. It exercises:
//   - GET  /jobs/new                renders the form.
//   - POST /jobs/new                with valid YAML creates the job.
//   - POST /jobs/new                with a duplicate name errors.
//   - GET  /jobs/{name}/edit        pre-fills the textarea.
//   - POST /jobs/{name}/edit        with a name-change errors.
//   - POST /jobs/{name}/edit        with valid update modifies the job.
func TestUI_JobCreateAndEdit(t *testing.T) {
	ts, jar := newAuthedUI(t)
	defer ts.Close()
	client := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	// GET /jobs/new -> 3-card picker (feedback follow-up).
	// The operator picks a kind, then GET the form. The
	// dedicated TestUI_JobsNew_LandingHasThreeCards test
	// covers the picker shape; here we just assert the
	// landing renders so the rest of this test can proceed
	// to POST against /jobs/new without first having to
	// pick a kind (POSTs to /jobs/new still route to
	// handleJobCreate regardless of the kind hint).
	resp := mustGet(t, client, ts.URL+"/jobs/new")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /jobs/new status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Inline script") {
		t.Errorf("GET /jobs/new body missing the picker's " +
			"Inline script card; the picker may have regressed")
	}

	// POST /jobs/new (good)
	spec := "name: build-foo\ndescription: builds foo\nsteps:\n  - shell: \"make foo\"\n"
	resp = mustPostForm(t, client, ts.URL+"/jobs/new", url.Values{"spec": {spec}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /jobs/new (good) status = %d, want 303", resp.StatusCode)
	}

	// Verify the job landed on disk.
	jobs, err := storageFromServer(ts).ListJobs()
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Name != "build-foo" {
		t.Fatalf("after create: jobs = %+v", jobs)
	}

	// POST /jobs/new again with the same name -> error.
	resp = mustPostForm(t, client, ts.URL+"/jobs/new", url.Values{"spec": {spec}})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /jobs/new (duplicate) status = %d, want 200 (form re-render)", resp.StatusCode)
	}
	if !strings.Contains(body, "already exists") {
		t.Errorf("duplicate POST body missing 'already exists':\n%s", body)
	}

	// GET /jobs/build-foo/edit (default view = form after
	// the feedback follow-up). The raw spec content lives
	// under ?view=raw now; this test asserts the form view
	// pre-fills the Name input.
	resp = mustGet(t, client, ts.URL+"/jobs/build-foo/edit")
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET edit status = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{"<textarea", `value="build-foo"`, "/jobs/build-foo/edit"} {
		if !strings.Contains(body, want) {
			t.Errorf("GET edit body missing %q", want)
		}
	}
	// And the raw view is still reachable via ?view=raw.
	resp = mustGet(t, client,
		ts.URL+"/jobs/build-foo/edit?view=raw")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET edit?view=raw status = %d, want 200",
			resp.StatusCode)
	}
	rawBody := readBody(t, resp)
	if !strings.Contains(rawBody, "name: build-foo") {
		t.Errorf("?view=raw body missing the raw " +
			"spec content")
	}

	// POST edit with a renamed YAML -> error.
	renamed := "name: build-bar\ndescription: rename attempt\nsteps:\n  - shell: \"true\"\n"
	resp = mustPostForm(t, client, ts.URL+"/jobs/build-foo/edit", url.Values{"spec": {renamed}})
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST edit rename status = %d, want 200 (form re-render)", resp.StatusCode)
	}
	if !strings.Contains(body, "rename is not supported") {
		t.Errorf("rename attempt body missing the message: %q", body)
	}

	// POST edit with valid update.
	updated := "name: build-foo\ndescription: now updated\nnode_label: linux\nsteps:\n  - shell: \"make all\"\n"
	resp = mustPostForm(t, client, ts.URL+"/jobs/build-foo/edit", url.Values{"spec": {updated}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST edit (good) status = %d, want 303", resp.StatusCode)
	}

	final, err := storageFromServer(ts).LoadJob("build-foo")
	if err != nil {
		t.Fatalf("LoadJob: %v", err)
	}
	if final.Description != "now updated" {
		t.Errorf("Description = %q, want %q", final.Description, "now updated")
	}
	if final.NodeLabel != "linux" {
		t.Errorf("NodeLabel = %q, want linux", final.NodeLabel)
	}
}

// newAuthedUI stands up a wolfCI HTTP server with one
// pre-registered password user "alice"/"hunter2" and returns
// it along with a cookie jar that has already signed in.
func newAuthedUI(t *testing.T) (*httptest.Server, http.CookieJar) {
	t.Helper()
	dir := t.TempDir()
	st, err := storage.New(dir)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	authCfg := &auth.Config{PasswordEnabled: true, PBKDF2Iterations: 1000}
	pwStore := auth.NewPasswordStore(filepath.Join(dir, "passwords"), authCfg)
	if err := pwStore.SetPassword("alice", "hunter2"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	sessions := server.NewSessionStore(filepath.Join(dir, "sessions"), time.Hour)

	srv := server.New(server.Options{
		Storage:      st,
		Auth:         authCfg,
		Passwords:    pwStore,
		Sessions:     sessions,
		CookieSecure: false,
	})
	ts := httptest.NewServer(srv)
	// Hang the storage off the server for test introspection.
	testStorageByURL[ts.URL] = st

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.PostForm(ts.URL+"/login", url.Values{
		"username": {"alice"},
		"password": {"hunter2"},
	})
	if err != nil {
		ts.Close()
		t.Fatalf("login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		ts.Close()
		t.Fatalf("login status = %d, want 303", resp.StatusCode)
	}
	return ts, jar
}

var testStorageByURL = map[string]*storage.Storage{}

func storageFromServer(ts *httptest.Server) *storage.Storage {
	return testStorageByURL[ts.URL]
}

func mustGet(t *testing.T, c *http.Client, url string) *http.Response {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func mustPostForm(t *testing.T, c *http.Client, url string, form url.Values) *http.Response {
	t.Helper()
	resp, err := c.PostForm(url, form)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	return bodyString(t, resp.Body)
}
