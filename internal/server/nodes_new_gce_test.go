package server_test

/* internal/server/nodes_new_gce_test.go - PLAN.md 19.6
 * gating tests.
 *
 * TestNodesNewGCE_FormHasFields GETs /nodes/new/gce and
 * asserts the seven named inputs are present in the
 * rendered form.
 *
 * TestNodesNewGCE_PostCreatesConfig POSTs a complete config,
 * asserts the response 303s to /nodes/gce/<name>, and reads
 * the saved YAML back through gce.LoadConfig.
 *
 * TestNodesNewGCE_PostRejectsMissingProject confirms a
 * missing required field re-renders the form with the
 * validation diagnostic.
 */

import (
    "net/http"
    "net/url"
    "path/filepath"
    "reflect"
    "strings"
    "testing"

    "github.com/kaleb-himes/wolfCI/internal/nodes/gce"
)

func TestNodesNewGCE_FormHasFields(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar}
    resp := mustGet(t, client, ts.URL+"/nodes/new/gce")
    if resp.StatusCode != http.StatusOK {
        t.Fatalf("GET status = %d, want 200",
            resp.StatusCode)
    }
    body := readBody(t, resp)
    for _, want := range []string{
        `name="name"`,
        `name="project_id"`,
        `name="zone"`,
        `name="machine_type"`,
        `name="image"`,
        `name="service_account_key"`,
        `name="network"`,
        `name="labels"`,
        `name="max_instances"`,
        `action="/nodes/new/gce"`,
    } {
        if !strings.Contains(body, want) {
            t.Errorf("form body missing %q", want)
        }
    }
}

func TestNodesNewGCE_PostCreatesConfig(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(req *http.Request,
            via []*http.Request) error {
            return http.ErrUseLastResponse
        }}
    form := url.Values{
        "name":                {"linux-cloud-pool"},
        "project_id":          {"my-gcp-project-12345"},
        "zone":                {"us-central1-a"},
        "machine_type":        {"e2-medium"},
        "image":               {"projects/debian-cloud/global/images/family/debian-12"},
        "service_account_key": {"/etc/wolfci/sa.json"},
        "network":             {"default"},
        "labels":              {"linux-cloud-node\nlinux\nbuild"},
        "max_instances":       {"10"},
    }
    resp := mustPostForm(t, client,
        ts.URL+"/nodes/new/gce", form)
    if resp.StatusCode != http.StatusSeeOther {
        body := readBody(t, resp)
        t.Fatalf("POST status = %d, want 303; body:\n%s",
            resp.StatusCode, body)
    }
    if got := resp.Header.Get("Location"); got !=
        "/nodes/gce/linux-cloud-pool" {
        t.Errorf("Location = %q, want "+
            "/nodes/gce/linux-cloud-pool", got)
    }
    st := storageFromServer(ts)
    path := filepath.Join(st.Root(), "nodes", "gce",
        "linux-cloud-pool.yaml")
    cfg, err := gce.LoadConfig(path)
    if err != nil {
        t.Fatalf("gce.LoadConfig: %v", err)
    }
    if cfg.ProjectID != "my-gcp-project-12345" {
        t.Errorf("ProjectID = %q", cfg.ProjectID)
    }
    if cfg.Zone != "us-central1-a" {
        t.Errorf("Zone = %q", cfg.Zone)
    }
    if cfg.MachineType != "e2-medium" {
        t.Errorf("MachineType = %q", cfg.MachineType)
    }
    if !reflect.DeepEqual(cfg.Labels,
        []string{"linux-cloud-node", "linux", "build"}) {
        t.Errorf("Labels = %v", cfg.Labels)
    }
    if cfg.MaxInstances != 10 {
        t.Errorf("MaxInstances = %d, want 10",
            cfg.MaxInstances)
    }
}

func TestNodesNewGCE_PostRejectsMissingProject(t *testing.T) {
    ts, jar := newAuthedUI(t)
    defer ts.Close()
    client := &http.Client{Jar: jar,
        CheckRedirect: func(req *http.Request,
            via []*http.Request) error {
            return http.ErrUseLastResponse
        }}
    form := url.Values{
        "name":                {"missing-project"},
        "zone":                {"us-central1-a"},
        "machine_type":        {"e2-medium"},
        "image":               {"projects/debian-cloud/global/images/family/debian-12"},
        "service_account_key": {"/etc/wolfci/sa.json"},
    }
    resp := mustPostForm(t, client,
        ts.URL+"/nodes/new/gce", form)
    if resp.StatusCode != http.StatusOK {
        body := readBody(t, resp)
        t.Fatalf("POST status = %d, want 200 (form rerender)"+
            "; body:\n%s", resp.StatusCode, body)
    }
    body := readBody(t, resp)
    if !strings.Contains(body, "project_id") {
        t.Errorf("error body should mention project_id "+
            "diagnostic; got:\n%s", body)
    }
}
