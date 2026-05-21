package plugin_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kaleb-himes/wolfCI/internal/plugin"
)

// TestHost_LoadAndDispatchHello is the gating test for PLAN.md
// tasks 7.2 and 7.3. It builds plugins/examples/hello into a
// temp plugins-root, loads it via Host.Load, dispatches a
// BuildCompleteEvent, and asserts the plugin wrote the event
// to disk via WOLFCI_HELLO_OUT.
func TestHost_LoadAndDispatchHello(t *testing.T) {
	dir := t.TempDir()
	pluginsRoot := filepath.Join(dir, "plugins-root")
	pluginDir := filepath.Join(pluginsRoot, "installed", "hello")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Build the hello binary into the install path.
	binPath := filepath.Join(pluginDir, "hello")
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Dir = filepath.Join("..", "..", "plugins", "examples", "hello")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build hello: %v\n%s", err, out)
	}

	// The plugin writes its received event to this file.
	eventFile := filepath.Join(dir, "event.json")
	t.Setenv("WOLFCI_HELLO_OUT", eventFile)

	host := plugin.NewHost(pluginsRoot)
	loaded, err := host.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer host.Stop()
	if len(loaded) != 1 || loaded[0] != "hello" {
		t.Fatalf("loaded = %v, want [hello]", loaded)
	}

	event := plugin.BuildCompleteEvent{
		JobName:     "demo",
		BuildNumber: 7,
		Status:      "success",
		ExitCode:    0,
	}
	host.OnBuildComplete(context.Background(), event)

	// Plugin writes synchronously inside the RPC; the host
	// returns after that. Read should find the file.
	var got plugin.BuildCompleteEvent
	deadline := time.Now().Add(3 * time.Second)
	var data []byte
	for time.Now().Before(deadline) {
		var readErr error
		data, readErr = os.ReadFile(eventFile)
		if readErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(data) == 0 {
		t.Fatalf("plugin never wrote %s", eventFile)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal event.json: %v", err)
	}
	if got.JobName != "demo" || got.BuildNumber != 7 || got.Status != "success" {
		t.Errorf("event = %+v, want JobName=demo BuildNumber=7 Status=success", got)
	}
}

// TestHost_LoadEmpty: a Host pointed at a nonexistent or empty
// installed dir loads zero plugins without erroring.
func TestHost_LoadEmpty(t *testing.T) {
	dir := t.TempDir()
	host := plugin.NewHost(dir)
	defer host.Stop()
	loaded, err := host.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 0 {
		t.Errorf("loaded = %v, want empty", loaded)
	}
}
