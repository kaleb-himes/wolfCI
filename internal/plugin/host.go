package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	hcplugin "github.com/hashicorp/go-plugin"
)

// Host manages a pool of plugin subprocesses. It loads every
// binary under <pluginsDir>/installed/<name>/<name> on Load,
// keeps each subprocess alive until Stop, and dispatches
// lifecycle hooks to all of them.
type Host struct {
	pluginsDir string

	mu      sync.Mutex
	clients map[string]*hcplugin.Client
}

// NewHost returns a Host rooted at pluginsDir. Load is the
// only call that touches disk; NewHost itself is cheap.
func NewHost(pluginsDir string) *Host {
	return &Host{
		pluginsDir: pluginsDir,
		clients:    make(map[string]*hcplugin.Client),
	}
}

// Load discovers and launches every plugin under
// <pluginsDir>/installed/. A subdirectory's binary must share
// the directory name (e.g. installed/hello/hello). Returns the
// names of plugins that successfully connected.
func (h *Host) Load() ([]string, error) {
	installedDir := filepath.Join(h.pluginsDir, "installed")
	entries, err := os.ReadDir(installedDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("plugin.Host.Load: read %s: %w", installedDir, err)
	}

	var loaded []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		binPath := filepath.Join(installedDir, name, name)
		if _, err := os.Stat(binPath); err != nil {
			continue
		}

		client := hcplugin.NewClient(&hcplugin.ClientConfig{
			HandshakeConfig:  Handshake,
			Plugins:          PluginMap(nil),
			Cmd:              exec.Command(binPath),
			AllowedProtocols: []hcplugin.Protocol{hcplugin.ProtocolGRPC},
		})

		// Verify the handshake succeeds.
		if _, err := client.Client(); err != nil {
			client.Kill()
			continue
		}

		h.mu.Lock()
		h.clients[name] = client
		h.mu.Unlock()
		loaded = append(loaded, name)
	}
	return loaded, nil
}

// OnBuildComplete dispatches the event to every loaded plugin.
// Errors are swallowed so one misbehaving plugin does not block
// the others. Plugins that need to surface errors should log
// them on their own side.
func (h *Host) OnBuildComplete(ctx context.Context, event BuildCompleteEvent) {
	h.mu.Lock()
	clients := make(map[string]*hcplugin.Client, len(h.clients))
	for k, v := range h.clients {
		clients[k] = v
	}
	h.mu.Unlock()

	for _, c := range clients {
		protocolClient, err := c.Client()
		if err != nil {
			continue
		}
		raw, err := protocolClient.Dispense(PluginName)
		if err != nil {
			continue
		}
		impl, ok := raw.(WolfCIPlugin)
		if !ok {
			continue
		}
		_ = impl.OnBuildComplete(ctx, event)
	}
}

// Loaded returns the names of currently-loaded plugins.
func (h *Host) Loaded() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.clients))
	for k := range h.clients {
		out = append(out, k)
	}
	return out
}

// Stop kills every plugin subprocess. Idempotent.
func (h *Host) Stop() {
	h.mu.Lock()
	clients := h.clients
	h.clients = make(map[string]*hcplugin.Client)
	h.mu.Unlock()
	for _, c := range clients {
		c.Kill()
	}
}
